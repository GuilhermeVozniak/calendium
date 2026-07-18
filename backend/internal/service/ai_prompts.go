// Package service (this file): prompts for AIJobService's background job
// handlers (ai_jobs.go). Each job kind appends a self-contained section
// below, marked `// --- <kind> ---`: the JSON output shape decoded via
// completeJSONBudgeted, the system prompt, and any user-prompt builder the
// handler needs. Sections are independent of one another so parallel work on
// different job kinds (Tasks 6-14) can each append here without touching
// another section.
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

// --- reminder_detect ---

// reminderOut is the CompleteJSON output shape for AiJobReminderDetect:
// whether the owner's sent mail still needs a reply, and a sensible
// follow-up horizon in days.
type reminderOut struct {
	AwaitingReply bool `json:"awaitingReply"` // the sent mail expects an answer
	RemindDays    int  `json:"remindDays"`    // sensible follow-up horizon, 1-14
}

const reminderSystem = "You judge whether the owner's sent email still needs " +
	"a reply from the recipient (a question, request, or proposal — not an " +
	"FYI or sign-off). Respond as JSON: {\"awaitingReply\": bool, \"remindDays\": int}"

// reminderUserPrompt builds the CompleteJSON user prompt for
// AiJobReminderDetect from the thread's subject and the snippet of its last
// message. runReminderDetect only reaches this point after confirming no
// inbound reply arrived following the send, so the snippet reflects the
// owner's own sent message.
func reminderUserPrompt(subject, snippet string) string {
	return fmt.Sprintf("Subject: %s\n\nSent message snippet: %s", subject, snippet)
}

// --- voice_profile ---

// voiceProfileOut is the CompleteJSON target for runVoiceProfile.
type voiceProfileOut struct {
	Profile string `json:"profile"` // 5-10 bullet style guide
}

const voiceProfileSystem = "Analyze these emails the user wrote and produce a " +
	"compact style profile (greeting/sign-off habits, formality, sentence " +
	"length, emoji/punctuation quirks, typical structure) as 5-10 terse " +
	"bullets an assistant can follow to write in their voice. Respond as " +
	`JSON: {"profile": "..."}`

const voiceSampleCount = 25 // newest sent messages fed to the profiler

// voiceMinSamples is the minimum sent-message count before runVoiceProfile
// bothers calling the LLM; below this there isn't enough signal to learn a
// style from yet (the 30-day re-enqueue retries once more mail exists).
const voiceMinSamples = 5

// --- ask ---

// askOut is the structured JSON contract for POST /v1/ai/ask: the model's
// prose answer plus the [msg:<id>] ids it actually drew on. Ask validates
// SourceMessageIDs against the candidate set it built, dropping any id the
// model hallucinated before mapping the rest to domain.AiSource.
type askOut struct {
	Answer           string   `json:"answer"`
	SourceMessageIDs []string `json:"sourceMessageIds"`
}

const askSystem = "You are Calendium's assistant. Answer the question using " +
	"ONLY the provided messages, each tagged [msg:<id>]. Cite the ids of the " +
	"messages you actually used. If the answer is not in the messages, say so " +
	`and cite nothing. Respond as JSON: {"answer": "...", "sourceMessageIds": ["..."]}`
