// Package service (this file): prompt builders and structured LLM output
// types shared by cmd/worker's background AI job handlers (ai_jobs.go).
// Tasks 6-14 each append a self-contained, clearly-labeled section below —
// one per job kind — so unrelated additions land in different regions of
// the file and rarely conflict with each other.
package service

import (
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

// threadContext renders the newest aiContextMessages messages of a thread
// into a compact prompt block shared by every thread-scoped job handler
// (summary/replies/draft/ask/event). Reuses aiContextMessages,
// aiContextBodyMax (ai.go), and truncate (service.go) so the trimming rules
// stay identical to the interactive Compose path.
func threadContext(t domain.Thread, msgs []domain.Message) string {
	var b strings.Builder
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
	return b.String()
}

// --- thread_summary ---

// summaryOut is the structured CompleteJSON output for the thread_summary
// job kind (Task 6): a single present-tense line capturing what the
// conversation is about and its current state.
type summaryOut struct {
	Summary string `json:"summary"`
}

const summarySystem = "You are Calendium's email assistant. Produce a single, " +
	"specific, present-tense line (max 120 characters) capturing what this " +
	"conversation is about and its current state. Respond as JSON: " +
	`{"summary": "..."}`
