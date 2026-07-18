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
	AI            port.AI
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// AIService implements port.AIService (OpenRouter-backed
// compose/reply/summarize/ask).
type AIService struct {
	ent      entitlement
	accounts port.AccountRepo
	threads  port.ThreadRepo
	messages port.MessageRepo
	drafts   port.DraftRepo
	ai       port.AI
}

var _ port.AIService = (*AIService)(nil)

func NewAIService(d AIServiceDeps) *AIService {
	return &AIService{
		ent:      entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts: d.Accounts,
		threads:  d.Threads,
		messages: d.Messages,
		drafts:   d.Drafts,
		ai:       d.AI,
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
	switch req.Action {
	case domain.AiImprove, domain.AiShorten, domain.AiSimplify, domain.AiFixGrammar, domain.AiChangeTone:
		// Editing actions operate on the draft body (DraftID) or, absent a
		// draft, on Prompt used directly as the text to edit.
		if req.DraftID == "" && strings.TrimSpace(req.Prompt) == "" {
			return zero, fmt.Errorf("%w: editing actions require draftId or prompt", domain.ErrValidation)
		}
		if req.Action == domain.AiChangeTone && strings.TrimSpace(req.Tone) == "" {
			return zero, fmt.Errorf("%w: change_tone requires tone", domain.ErrValidation)
		}
	case domain.AiSummarize:
		// Prompt is optional; thread context alone is enough.
	default: // compose, reply, ask
		if strings.TrimSpace(req.Prompt) == "" {
			return zero, fmt.Errorf("%w: prompt is required", domain.ErrValidation)
		}
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

	text, model, err := s.ai.Complete(ctx, systemPromptFor(req), strings.TrimSpace(b.String()))
	if err != nil {
		return zero, err
	}
	return domain.AiComposeResponse{Text: text, Model: model}, nil
}

func systemPromptFor(req domain.AiComposeRequest) string {
	const base = "You are Calendium's email assistant. Be concise, warm, and professional. Return only the requested text with no preamble."
	switch req.Action {
	case domain.AiReply:
		return base + " Write a reply to the conversation, matching its tone."
	case domain.AiSummarize:
		return base + " Summarize the conversation in a few crisp bullet points."
	case domain.AiAsk:
		return base + " Answer the user's question, using the conversation as context when provided."
	case domain.AiImprove:
		return base + " Rewrite the draft to be clearer and more compelling. Preserve meaning, links, and facts. Return only the rewritten body."
	case domain.AiShorten:
		return base + " Rewrite the draft in at most half the words. Preserve every commitment and question. Return only the rewritten body."
	case domain.AiSimplify:
		return base + " Rewrite the draft in plain, simple language. Return only the rewritten body."
	case domain.AiFixGrammar:
		return base + " Fix spelling, grammar, and punctuation only; change nothing else. Return only the corrected body."
	case domain.AiChangeTone:
		return base + fmt.Sprintf(" Rewrite the draft with this tone: %s. Preserve meaning. Return only the rewritten body.", req.Tone)
	default: // compose
		return base + " Write a complete email following the user's instruction."
	}
}
