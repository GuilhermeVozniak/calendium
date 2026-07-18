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

	askSearchThreadLimit = 6    // threads pulled from Threads.Search for a mailbox-wide ask
	askMaxMessagesPerHit = 4    // newest messages kept per matched thread
	askMaxMessages       = 24   // hard cap on total candidate messages sent to the model
	askBodyMax           = 2000 // per-message body budget, bytes
	askSnippetMax        = 140  // source snippet length, bytes
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

// askCandidate is one message eligible to be cited in an Ask answer, paired
// with the thread it belongs to (for subject/snippet on the resulting
// domain.AiSource).
type askCandidate struct {
	msg    domain.Message
	thread domain.Thread
}

// Ask answers a natural-language question over the user's mailbox (or one
// thread when req.ThreadID is set) and returns the answer plus the cited
// thread/message sources. It never fabricates citations: every id the model
// returns is checked against the retrieved candidate set and dropped if it
// doesn't match an actual message the service fetched.
func (s *AIService) Ask(ctx context.Context, userID string, req domain.AiAskRequest) (domain.AiAskResponse, error) {
	var zero domain.AiAskResponse
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return zero, fmt.Errorf("%w: question is required", domain.ErrValidation)
	}
	if s.ai == nil {
		return zero, domain.ErrAIUnavailable
	}

	candidates, err := s.askCandidates(ctx, userID, req)
	if err != nil {
		return zero, err
	}

	var b strings.Builder
	byID := make(map[string]askCandidate, len(candidates))
	for _, c := range candidates {
		byID[c.msg.ID] = c
		body := c.msg.BodyText
		if body == "" {
			body = c.msg.BodyHTML
		}
		fmt.Fprintf(&b, "[msg:%s] From %s at %s in %q:\n%s\n\n",
			c.msg.ID, c.msg.From.Email, c.msg.SentAt.Format(time.RFC3339), c.thread.Subject, truncate(body, askBodyMax))
	}

	var out askOut
	model, err := s.ai.CompleteJSON(ctx, askSystem, strings.TrimSpace(b.String()), &out)
	if err != nil {
		return zero, fmt.Errorf("ai ask: %w", err)
	}

	sources := make([]domain.AiSource, 0, len(out.SourceMessageIDs))
	for _, id := range out.SourceMessageIDs {
		c, ok := byID[id]
		if !ok {
			continue // hallucinated id: not in the candidate set we actually fetched
		}
		body := c.msg.BodyText
		if body == "" {
			body = c.msg.BodyHTML
		}
		sources = append(sources, domain.AiSource{
			ThreadID:  c.thread.ID,
			MessageID: c.msg.ID,
			Subject:   c.thread.Subject,
			Snippet:   truncate(body, askSnippetMax),
		})
	}

	return domain.AiAskResponse{Answer: out.Answer, Model: model, Sources: sources}, nil
}

// askCandidates retrieves the messages Ask may cite: one owned thread's
// messages when req.ThreadID is set, or the newest few messages from each of
// the top Threads.Search hits for a mailbox-wide question.
func (s *AIService) askCandidates(ctx context.Context, userID string, req domain.AiAskRequest) ([]askCandidate, error) {
	if req.ThreadID != "" {
		t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, req.ThreadID)
		if err != nil {
			return nil, err
		}
		msgs, err := s.messages.ListByThread(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		return newestAskCandidates(msgs, t, askMaxMessages), nil
	}

	hits, err := s.threads.Search(ctx, userID, req.Question, askSearchThreadLimit)
	if err != nil {
		return nil, err
	}
	var candidates []askCandidate
	for _, t := range hits {
		msgs, err := s.messages.ListByThread(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range newestAskCandidates(msgs, t, askMaxMessagesPerHit) {
			if len(candidates) >= askMaxMessages {
				return candidates, nil
			}
			candidates = append(candidates, c)
		}
	}
	return candidates, nil
}

// newestAskCandidates keeps the newest `limit` messages (ListByThread order
// is oldest-first) and pairs each with its thread.
func newestAskCandidates(msgs []domain.Message, t domain.Thread, limit int) []askCandidate {
	start := 0
	if len(msgs) > limit {
		start = len(msgs) - limit
	}
	out := make([]askCandidate, 0, len(msgs)-start)
	for _, m := range msgs[start:] {
		out = append(out, askCandidate{msg: m, thread: t})
	}
	return out
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
