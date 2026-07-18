// ai_prompts.go holds CompleteJSON contracts (the `xxxOut` structs decoded
// from the model's JSON response) and the system/user prompt builders for
// AIService/AIJobService use-cases whose generation logic lives in ai.go /
// ai_jobs.go. Each use-case appends its own section here, marked with a
// `// --- <use case> ---` header, so concurrent additions stay
// non-overlapping.
package service

import (
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

// --- event_proposal ---

// eventProposalOut is the CompleteJSON contract for Instant Event AI (POST
// /v1/ai/event-proposal): the model reads the thread transcript plus an
// AVAILABILITY block of real free slots and proposes one concrete event.
// PreferredSlot must echo one of the offered AVAILABILITY starts verbatim
// (RFC3339); ai.go's ProposeEvent validates this rather than trusting it.
type eventProposalOut struct {
	Title           string   `json:"title"`
	AttendeeEmails  []string `json:"attendeeEmails"`
	DurationMinutes int      `json:"durationMinutes"`
	PreferredSlot   string   `json:"preferredSlot"` // RFC3339 start of one AVAILABILITY slot
	Location        string   `json:"location"`
	Notes           string   `json:"notes"`
}

const eventProposalSystemPrompt = "You are Calendium's scheduling assistant. Read the email conversation and the offered AVAILABILITY slots, then propose one concrete calendar event as JSON matching the requested schema exactly. preferredSlot MUST be exactly one of the offered AVAILABILITY timestamps, verbatim. Return only JSON, no preamble or commentary."

// buildEventProposalUser renders the thread transcript (newest
// aiContextMessages, body-budgeted like Compose) followed by an
// "AVAILABILITY:" block listing each offered slot's RFC3339 start, one per
// line — the exact values a valid preferredSlot must echo.
func buildEventProposalUser(subject string, msgs []domain.Message, slots []domain.AvailabilitySlot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation subject: %s\n\n", subject)
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
	b.WriteString("AVAILABILITY:\n")
	for _, s := range slots {
		fmt.Fprintf(&b, "%s\n", s.Start.Format(time.RFC3339))
	}
	return strings.TrimSpace(b.String())
}
