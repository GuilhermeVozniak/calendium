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

// --- auto_draft ---

// autoDraftOut is runAutoDraft's structured CompleteJSON output (both the
// no-availability first pass and the availability-aware second pass decode
// into this same shape).
type autoDraftOut struct {
	ShouldDraft      bool   `json:"shouldDraft"`      // false: no reply expected from the owner
	IsMeetingRequest bool   `json:"isMeetingRequest"` // sender is asking to meet/schedule
	Subject          string `json:"subject"`
	BodyHTML         string `json:"bodyHtml"`
}

const autoDraftSystem = "You are Calendium's email assistant drafting a reply " +
	"the owner will review before sending. Decide first whether the newest " +
	"message actually awaits a reply from the owner. If it proposes a meeting, " +
	"set isMeetingRequest and, when an AVAILABILITY block is provided, offer " +
	"2-3 of those exact times. Respond as JSON: " +
	`{"shouldDraft": bool, "isMeetingRequest": bool, "subject": "...", "bodyHtml": "..."}`

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

// --- classify ---

// classifyOut is the CompleteJSON decode target for runClassify: the ids of
// every user-defined classifier (from the numbered rule list
// buildClassifySystem sends) that genuinely matches the message. Ids the
// model returns that don't match a known classifier (hallucinated, or from a
// stale prompt) are dropped by the caller, not trusted here.
type classifyOut struct {
	MatchedIDs []string `json:"matchedClassifierIds"`
}

// buildClassifySystem lists the user's enabled classifiers by id and prompt,
// instructing the model to evaluate each rule against the message
// independently and report only genuine matches.
func buildClassifySystem(cs []domain.AiClassifier) string {
	var b strings.Builder
	b.WriteString("You are Calendium's mail classifier. The user has defined the natural-language " +
		"rules below, each identified by an id. Evaluate the message that follows against EVERY " +
		"rule independently and decide whether it genuinely matches - do not guess or include a " +
		"rule out of caution. Respond with a JSON object of the exact shape " +
		`{"matchedClassifierIds": ["<id>", ...]}` + " listing only the ids of rules that truly " +
		"match, using exactly the ids given below (never invent new ones); return an empty array " +
		"when nothing matches.\n\nRules:\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "- id %q: %s\n", c.ID, c.Prompt)
	}
	return b.String()
}
