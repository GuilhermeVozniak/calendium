package domain

import (
	"fmt"
	"time"
)

// AiAction selects the OpenRouter-backed assistance mode.
type AiAction string

const (
	AiCompose    AiAction = "compose"
	AiReply      AiAction = "reply"
	AiSummarize  AiAction = "summarize"
	AiAsk        AiAction = "ask"
	AiImprove    AiAction = "improve"
	AiShorten    AiAction = "shorten"
	AiSimplify   AiAction = "simplify"
	AiFixGrammar AiAction = "fix_grammar"
	AiChangeTone AiAction = "change_tone"
)

// ParseAiAction validates an AI action body parameter.
func ParseAiAction(s string) (AiAction, error) {
	switch AiAction(s) {
	case AiCompose, AiReply, AiSummarize, AiAsk, AiImprove, AiShorten, AiSimplify, AiFixGrammar, AiChangeTone:
		return AiAction(s), nil
	}
	return "", fmt.Errorf("%w: unknown ai action %q", ErrValidation, s)
}

// AiJobKind selects the background AI job type.
type AiJobKind string

const (
	AiJobThreadSummary  AiJobKind = "thread_summary"
	AiJobInstantReplies AiJobKind = "instant_replies"
	AiJobAutoDraft      AiJobKind = "auto_draft"
	AiJobClassify       AiJobKind = "classify"
	AiJobReminderDetect AiJobKind = "reminder_detect"
	AiJobVoiceProfile   AiJobKind = "voice_profile"
)

// AiJobKinds lists every AiJobKind (tests iterate it so a new kind must be
// classified as background or user-initiated).
var AiJobKinds = []AiJobKind{
	AiJobThreadSummary, AiJobInstantReplies, AiJobAutoDraft,
	AiJobClassify, AiJobReminderDetect, AiJobVoiceProfile,
}

// ParseAiJobKind validates an AI job kind parameter.
func ParseAiJobKind(s string) (AiJobKind, error) {
	for _, k := range AiJobKinds {
		if AiJobKind(s) == k {
			return k, nil
		}
	}
	return "", fmt.Errorf("%w: unknown ai job kind %q", ErrValidation, s)
}

// AiJob is one queued background AI task.
type AiJob struct {
	ID        string
	UserID    string
	AccountID string
	Kind      AiJobKind
	ThreadID  *string           // nil for voice_profile
	Payload   map[string]string // small kind-specific extras (e.g. draft id)
	Attempts  int
	RunAfter  time.Time
}

// AiClassifier is a user-defined natural-language mail classifier applied at
// ingest. When it matches, the thread is routed to TargetSplit (if set) and
// tagged with a local label named LabelName (if set).
type AiClassifier struct {
	ID          string     `json:"id"`
	UserID      string     `json:"-"`
	Name        string     `json:"name"`
	Prompt      string     `json:"prompt"`
	TargetSplit InboxSplit `json:"targetSplit,omitempty"`
	LabelName   string     `json:"labelName,omitempty"`
	Enabled     bool       `json:"enabled"`
}

// VoiceProfile is the learned writing-style profile injected into compose.
type VoiceProfile struct {
	UserID      string    `json:"-"`
	Profile     string    `json:"profile"`
	SampleCount int       `json:"sampleCount"`
	Model       string    `json:"model"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// AiComposeRequest is the POST /v1/ai/compose payload.
type AiComposeRequest struct {
	Action   AiAction `json:"action"`
	Prompt   string   `json:"prompt"`
	ThreadID string   `json:"threadId,omitempty"`
	DraftID  string   `json:"draftId,omitempty"`
	Tone     string   `json:"tone,omitempty"` // change_tone only
}

// AiComposeResponse is the generated text plus the model that produced it.
type AiComposeResponse struct {
	Text  string `json:"text"`
	Model string `json:"model"`
}

// AiAskRequest is the POST /v1/ai/ask contract.
type AiAskRequest struct {
	Question string `json:"question"`
	ThreadID string `json:"threadId,omitempty"` // scope to one thread; "" = whole mailbox
}

type AiSource struct {
	ThreadID  string `json:"threadId"`
	MessageID string `json:"messageId,omitempty"`
	Subject   string `json:"subject"`
	Snippet   string `json:"snippet"`
}

type AiAskResponse struct {
	Answer  string     `json:"answer"`
	Model   string     `json:"model"`
	Sources []AiSource `json:"sources"`
}

// AiEventProposal is Instant Event AI's proposed calendar event.
type AiEventProposal struct {
	Title     string    `json:"title"`
	Attendees []string  `json:"attendees"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Location  string    `json:"location,omitempty"`
	Notes     string    `json:"notes,omitempty"`
}
