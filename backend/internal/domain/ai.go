package domain

import "fmt"

// AiAction selects the OpenRouter-backed assistance mode.
type AiAction string

const (
	AiCompose   AiAction = "compose"
	AiReply     AiAction = "reply"
	AiSummarize AiAction = "summarize"
	AiAsk       AiAction = "ask"
)

// ParseAiAction validates an AI action body parameter.
func ParseAiAction(s string) (AiAction, error) {
	switch AiAction(s) {
	case AiCompose, AiReply, AiSummarize, AiAsk:
		return AiAction(s), nil
	}
	return "", fmt.Errorf("%w: unknown ai action %q", ErrValidation, s)
}

// AiComposeRequest is the POST /v1/ai/compose payload.
type AiComposeRequest struct {
	Action   AiAction `json:"action"`
	Prompt   string   `json:"prompt"`
	ThreadID string   `json:"threadId,omitempty"`
	DraftID  string   `json:"draftId,omitempty"`
}

// AiComposeResponse is the generated text plus the model that produced it.
type AiComposeResponse struct {
	Text  string `json:"text"`
	Model string `json:"model"`
}
