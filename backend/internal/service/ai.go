package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	aiContextMessages = 10   // newest messages included as context
	aiContextBodyMax  = 2000 // per-message body budget, bytes
)

// AIServiceDeps wires an AIService.
type AIServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Accounts      port.AccountRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	VoiceProfiles port.VoiceProfileRepo // optional: nil skips voice-style injection
	AI            port.AI
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// AIService implements port.AIService (OpenRouter-backed
// compose/reply/summarize/ask).
type AIService struct {
	ent           entitlement
	accounts      port.AccountRepo
	threads       port.ThreadRepo
	messages      port.MessageRepo
	drafts        port.DraftRepo
	voiceProfiles port.VoiceProfileRepo
	ai            port.AI
}

var _ port.AIService = (*AIService)(nil)

func NewAIService(d AIServiceDeps) *AIService {
	return &AIService{
		ent:           entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:      d.Accounts,
		threads:       d.Threads,
		messages:      d.Messages,
		drafts:        d.Drafts,
		voiceProfiles: d.VoiceProfiles,
		ai:            d.AI,
	}
}

func (s *AIService) Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error) {
	var zero domain.AiComposeResponse
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	if _, err := domain.ParseAiAction(string(req.Action)); err != nil {
		return zero, err
	}
	if strings.TrimSpace(req.Prompt) == "" && req.Action != domain.AiSummarize {
		return zero, fmt.Errorf("%w: prompt is required", domain.ErrValidation)
	}

	var b strings.Builder
	if req.ThreadID != "" {
		t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, req.ThreadID)
		if err != nil {
			return zero, err
		}
		msgs, err := s.messages.ListByThread(ctx, t.ID)
		if err != nil {
			return zero, err
		}
		fmt.Fprintf(&b, "Conversation subject: %s\n\n", t.Subject)
		start := 0
		if len(msgs) > aiContextMessages {
			start = len(msgs) - aiContextMessages
		}
		for _, m := range msgs[start:] {
			body := m.BodyText
			if body == "" {
				body = m.BodyHTML
			}
			fmt.Fprintf(&b, "From %s at %s:\n%s\n\n", m.From.Email, m.SentAt.Format(time.RFC3339), truncate(body, aiContextBodyMax))
		}
	}
	if req.DraftID != "" {
		d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, req.DraftID)
		if err != nil {
			return zero, err
		}
		fmt.Fprintf(&b, "Current draft (subject %q):\n%s\n\n", d.Subject, truncate(d.BodyHTML, aiContextBodyMax))
	}
	if req.Prompt != "" {
		fmt.Fprintf(&b, "Instruction: %s", req.Prompt)
	}

	system := systemPromptFor(req.Action)
	// Personal voice: only for generative actions (compose/reply), never for
	// summarize/ask/editing actions where matching the user's own style
	// isn't the point. Missing profile (no VoiceProfiles configured, or
	// ErrNotFound because none has been learned yet) silently leaves the
	// prompt unchanged.
	if s.voiceProfiles != nil && (req.Action == domain.AiCompose || req.Action == domain.AiReply) {
		if p, err := s.voiceProfiles.Get(ctx, userID); err == nil && p.Profile != "" {
			system += "\n\nWrite in the user's personal style:\n" + p.Profile
		}
	}

	text, model, err := s.ai.Complete(ctx, system, strings.TrimSpace(b.String()))
	if err != nil {
		return zero, err
	}
	return domain.AiComposeResponse{Text: text, Model: model}, nil
}

func systemPromptFor(a domain.AiAction) string {
	const base = "You are Calendium's email assistant. Be concise, warm, and professional. Return only the requested text with no preamble."
	switch a {
	case domain.AiReply:
		return base + " Write a reply to the conversation, matching its tone."
	case domain.AiSummarize:
		return base + " Summarize the conversation in a few crisp bullet points."
	case domain.AiAsk:
		return base + " Answer the user's question, using the conversation as context when provided."
	default: // compose
		return base + " Write a complete email following the user's instruction."
	}
}
