// Package service (this file): ai_prompts.go holds every AI job's system
// prompt and structured-output shape in one place -- the closest this
// codebase gets to prompt fixtures. Each task appends its own section under
// a `// --- <job-kind> ---` header, self-contained (no shared helpers beyond
// what ai.go/ai_jobs.go already expose), so parallel work never collides.
package service

import (
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
)

// --- instant_replies ---

// instantRepliesOut is the structured output shape for both the
// instant_replies background job (AIJobService.runInstantReplies) and
// AIService's interactive on-open fallback (InstantReplies): 1-3 short,
// ready-to-send reply suggestions to the newest message in a thread.
type instantRepliesOut struct {
	Replies []string `json:"replies"`
}

const instantRepliesSystem = "You are Calendium's email assistant. Write 3 " +
	"short, distinct, ready-to-send replies to the newest message (max 2 " +
	"sentences each; vary intent: agree/decline/ask). Match the sender's " +
	`formality. Respond as JSON: {"replies": ["...", "...", "..."]}`

// instantRepliesUserPrompt builds the user message shared by the background
// job and the interactive fallback: the thread subject plus the newest
// message's sender and body (truncated to aiContextBodyMax, same budget as
// Compose's context assembly in ai.go) -- the actual message the 3 replies
// must respond to. msgs is expected oldest-first (ListByThread's order), so
// the newest message is the last element.
func instantRepliesUserPrompt(subject string, msgs []domain.Message) string {
	if len(msgs) == 0 {
		return fmt.Sprintf("Conversation subject: %s\n\n(no messages)", subject)
	}
	newest := msgs[len(msgs)-1]
	body := newest.BodyText
	if body == "" {
		body = newest.BodyHTML
	}
	return fmt.Sprintf("Conversation subject: %s\n\nNewest message from %s:\n%s",
		subject, newest.From.Email, truncate(body, aiContextBodyMax))
}

// normalizeInstantReplies validates the model's raw Replies into 1-3
// ready-to-cache suggestions: blank entries are dropped, and anything beyond
// the first 3 non-empty replies is clamped away. An AI response with zero
// non-empty replies is treated as malformed output (wrapped in
// domain.ErrAIOutput) so callers surface it as a normal error -- the job
// handler's generic backoff/retry path, or a 502 at the interactive layer.
func normalizeInstantReplies(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s := strings.TrimSpace(r); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: instant replies: no non-empty replies in AI output", domain.ErrAIOutput)
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out, nil
}
