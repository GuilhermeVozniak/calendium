// Package service (this file): system prompts and structured-output shapes
// shared by the AIJobService handlers in ai_jobs.go. Each job kind's section
// is self-contained (own type + own system prompt constant) so parallel
// tasks can append to this file without touching each other's blocks.
package service

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
