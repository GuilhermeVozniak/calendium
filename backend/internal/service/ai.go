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
	// Calendar supplies real availability for ProposeEvent (Instant Event AI).
	Calendar port.CalendarService
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// AIService implements port.AIService (OpenRouter-backed
// compose/reply/summarize/ask, plus Instant Event AI's ProposeEvent).
type AIService struct {
	ent      entitlement
	accounts port.AccountRepo
	threads  port.ThreadRepo
	messages port.MessageRepo
	drafts   port.DraftRepo
	ai       port.AI
	calendar port.CalendarService
	clock    port.Clock
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
		calendar: d.Calendar,
		clock:    d.Clock,
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

// --- ProposeEvent (Instant Event AI, Task 14) -------------------------------

const (
	eventProposalWindow         = 7 * 24 * time.Hour
	eventProposalSlotDuration   = 30 * time.Minute
	eventProposalSlotLimit      = 10
	eventProposalMinMinutes     = 15
	eventProposalMaxMinutes     = 480
	eventProposalDefaultMinutes = 30
)

// ProposeEvent reads the thread, computes real availability over the next 7
// days, and asks the model to propose a concrete event. Model output is
// validated against that context rather than passed through: an
// unparseable/unoffered preferredSlot falls back to the first offered slot,
// duration is clamped to [15, 480] minutes (default 30 when unset), and
// attendees are intersected with the thread's actual participants (plus the
// owning account, always included).
func (s *AIService) ProposeEvent(ctx context.Context, userID, threadID string) (domain.AiEventProposal, error) {
	var zero domain.AiEventProposal
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	t, account, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return zero, err
	}
	msgs, err := s.messages.ListByThread(ctx, t.ID)
	if err != nil {
		return zero, err
	}

	now := s.clock.Now()
	slots, err := s.calendar.Availability(ctx, userID, now, now.Add(eventProposalWindow), eventProposalSlotDuration)
	if err != nil {
		return zero, err
	}
	if len(slots) == 0 {
		return zero, fmt.Errorf("%w: no free slots", domain.ErrConflict)
	}
	if len(slots) > eventProposalSlotLimit {
		slots = slots[:eventProposalSlotLimit]
	}

	var out eventProposalOut
	if _, err := s.ai.CompleteJSON(ctx, eventProposalSystemPrompt, buildEventProposalUser(t.Subject, msgs, slots), &out); err != nil {
		return zero, err
	}

	start, ok := matchOfferedSlot(out.PreferredSlot, slots)
	if !ok {
		start = slots[0].Start
	}

	title := strings.TrimSpace(out.Title)
	if title == "" {
		title = t.Subject
	}

	return domain.AiEventProposal{
		Title:     title,
		Attendees: intersectAttendees(out.AttendeeEmails, t.Participants, account.Email),
		Start:     start,
		End:       start.Add(time.Duration(clampDurationMinutes(out.DurationMinutes)) * time.Minute),
		Location:  out.Location,
		Notes:     out.Notes,
	}, nil
}

// matchOfferedSlot parses candidate as RFC3339 and returns the offered slot
// start it exactly matches; ok is false when candidate doesn't parse or
// doesn't equal any offered slot (caller falls back to the first slot).
func matchOfferedSlot(candidate string, slots []domain.AvailabilitySlot) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, candidate)
	if err != nil {
		return time.Time{}, false
	}
	for _, s := range slots {
		if s.Start.Equal(parsed) {
			return s.Start, true
		}
	}
	return time.Time{}, false
}

// clampDurationMinutes bounds a model-proposed duration to [15, 480] minutes,
// defaulting to 30 when unset (zero or negative).
func clampDurationMinutes(m int) int {
	switch {
	case m <= 0:
		return eventProposalDefaultMinutes
	case m < eventProposalMinMinutes:
		return eventProposalMinMinutes
	case m > eventProposalMaxMinutes:
		return eventProposalMaxMinutes
	default:
		return m
	}
}

// intersectAttendees keeps only model-proposed emails that are actual thread
// participants (case-insensitive dedup), always including the account owner.
func intersectAttendees(proposed []string, participants []domain.EmailAddress, ownerEmail string) []string {
	allowed := make(map[string]bool, len(participants))
	for _, p := range participants {
		allowed[strings.ToLower(p.Email)] = true
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(email string) {
		key := strings.ToLower(email)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, email)
	}
	if ownerEmail != "" {
		add(ownerEmail)
	}
	for _, e := range proposed {
		if allowed[strings.ToLower(e)] {
			add(e)
		}
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
