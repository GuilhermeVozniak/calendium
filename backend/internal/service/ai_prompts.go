// Package service (this file): prompts for AIJobService's background job
// handlers (ai_jobs.go). Each job kind appends a self-contained section
// below, marked `// --- <kind> ---`: the JSON output shape decoded via
// completeJSONBudgeted, the system prompt, and any user-prompt builder the
// handler needs. Sections are independent of one another so parallel work on
// different job kinds (Tasks 6-14) can each append here without touching
// another section.
package service

import "fmt"

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
