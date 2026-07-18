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
	// Usage enforces the daily AI budget for interactive calls that
	// generate (InstantReplies et al.); it's the same per-user counter
	// AIJobService's background handlers charge, so interactive and
	// background AI usage share one cap.
	Usage port.AiUsageRepo
	// VoiceProfiles is wired now but unused until Task 11 (voice-matched
	// compose).
	VoiceProfiles port.VoiceProfileRepo
	AI            port.AI
	Clock         port.Clock
	// DailyLimit mirrors AIJobServiceDeps.DailyLimit (AI_DAILY_LIMIT,
	// default 300 when unset/non-positive).
	DailyLimit int
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// AIService implements port.AIService (OpenRouter-backed
// compose/reply/summarize/ask/instant-replies).
// voiceProfiles (AIServiceDeps.VoiceProfiles) is accepted now but not yet
// stored/used here: it's wired ahead of Task 11 (voice-matched compose) so
// that task's diff only adds behavior, not deps plumbing.
type AIService struct {
	ent        entitlement
	accounts   port.AccountRepo
	threads    port.ThreadRepo
	messages   port.MessageRepo
	drafts     port.DraftRepo
	usage      port.AiUsageRepo
	ai         port.AI
	clock      port.Clock
	dailyLimit int
}

var _ port.AIService = (*AIService)(nil)

func NewAIService(d AIServiceDeps) *AIService {
	limit := d.DailyLimit
	if limit <= 0 {
		limit = 300
	}
	return &AIService{
		ent:        entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:   d.Accounts,
		threads:    d.Threads,
		messages:   d.Messages,
		drafts:     d.Drafts,
		usage:      d.Usage,
		ai:         d.AI,
		clock:      d.Clock,
		dailyLimit: limit,
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

	text, model, err := s.ai.Complete(ctx, systemPromptFor(req.Action), strings.TrimSpace(b.String()))
	if err != nil {
		return zero, err
	}
	return domain.AiComposeResponse{Text: text, Model: model}, nil
}

// InstantReplies is the on-open fallback for the instant_replies worker job:
// threads on splits the sync loop skips enqueueing for (see sync.go) never
// get a background-generated cache, so the client hits this endpoint when it
// opens the thread and finds InstantReplies empty. A fresh cache
// (instant_replies_updated_at newer than the thread's last message) is
// returned as-is at no budget cost; otherwise this generates, persists via
// SetInstantReplies (the same targeted write the worker job uses), and
// returns the fresh suggestions -- charging the shared daily AI budget only
// on that generate path.
func (s *AIService) InstantReplies(ctx context.Context, userID, threadID string) ([]string, error) {
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return nil, err
	}
	if t.InstantRepliesUpdatedAt != nil && t.InstantRepliesUpdatedAt.After(t.LastMessageAt) {
		return t.InstantReplies, nil
	}
	if s.ai == nil {
		return nil, fmt.Errorf("%w: no AI provider configured", domain.ErrAIUnavailable)
	}
	allowed, err := s.usage.IncrementAndCheck(ctx, userID, s.clock.Now(), s.dailyLimit)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("%w: daily ai budget exhausted", domain.ErrRateLimited)
	}
	msgs, err := s.messages.ListByThread(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	var out instantRepliesOut
	if _, err := s.ai.CompleteJSON(ctx, instantRepliesSystem, instantRepliesUserPrompt(t.Subject, msgs), &out); err != nil {
		return nil, err
	}
	replies, err := normalizeInstantReplies(out.Replies)
	if err != nil {
		return nil, err
	}
	if err := s.threads.SetInstantReplies(ctx, t.ID, replies, s.clock.Now()); err != nil {
		return nil, err
	}
	return replies, nil
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
