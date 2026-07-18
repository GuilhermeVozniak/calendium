// Package openrouter implements port.AI against the OpenRouter
// chat-completions API with raw net/http, stdlib only.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const completionsURL = "https://openrouter.ai/api/v1/chat/completions"

// Client is the OpenRouter AI gateway.
type Client struct {
	apiKey string
	model  string
	hc     *http.Client
}

var _ port.AI = (*Client)(nil)

// NewClient builds the OpenRouter gateway. model is the configured model id
// (config.OpenRouter.Model, default "openrouter/auto"); hc may be nil, in
// which case http.DefaultClient is used.
func NewClient(apiKey, model string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	if model == "" {
		model = "openrouter/auto"
	}
	return &Client{apiKey: apiKey, model: model, hc: hc}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete sends one system+user exchange and returns the generated text
// plus the model that actually served it. Cancellation flows through ctx.
func (c *Client) Complete(ctx context.Context, system, user string) (string, string, error) {
	return c.complete(ctx, system, user, false)
}

// CompleteJSON sends one system+user exchange requesting a JSON-object
// response (OpenRouter response_format json_object) and decodes it into out.
// It returns domain.ErrAIOutput-wrapped errors when the model's reply is not
// valid JSON for out.
func (c *Client) CompleteJSON(ctx context.Context, system, user string, out any) (string, error) {
	text, model, err := c.complete(ctx, system, user, true)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(stripJSONFences(text)), out); err != nil {
		return "", fmt.Errorf("%w: openrouter: decode structured output: %w", domain.ErrAIOutput, err)
	}
	return model, nil
}

// stripJSONFences robustly extracts JSON from prose-wrapped LLM output.
// It tries in order:
// 1. Extract content from first ```json or ``` fenced block (anywhere in string)
// 2. If trimmed string starts with { or [, return as-is
// 3. Locate first { or [ and last } or ] and take that slice
// 4. Return trimmed input (decode will fail → ErrAIOutput path)
func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)

	// Strategy 1: Look for fenced blocks (```json or bare ```)
	// Find first opening fence
	jsonFenceIdx := strings.Index(s, "```json")
	bareFenceIdx := strings.Index(s, "```")

	// Determine which fence comes first (prefer ```json if at same position)
	var fenceStart int
	var jsonTyped bool
	if jsonFenceIdx >= 0 && (bareFenceIdx < 0 || jsonFenceIdx <= bareFenceIdx) {
		fenceStart = jsonFenceIdx
		jsonTyped = true
	} else if bareFenceIdx >= 0 {
		fenceStart = bareFenceIdx
		jsonTyped = false
	} else {
		fenceStart = -1
	}

	if fenceStart >= 0 {
		// Found a fence; extract its content
		var contentStart int
		if jsonTyped {
			contentStart = fenceStart + len("```json")
		} else {
			contentStart = fenceStart + len("```")
		}

		// Find the closing fence (next ``` line)
		closingFenceIdx := strings.Index(s[contentStart:], "```")
		if closingFenceIdx >= 0 {
			content := s[contentStart : contentStart+closingFenceIdx]
			return strings.TrimSpace(content)
		}
	}

	// Strategy 2: If trimmed string starts with { or [, use as-is
	if len(s) > 0 && (s[0] == '{' || s[0] == '[') {
		return s
	}

	// Strategy 3: Find first { or [ and last } or ]
	firstOpenIdx := -1
	openChar := byte(0)
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			firstOpenIdx = i
			openChar = s[i]
			break
		}
	}

	if firstOpenIdx >= 0 {
		// Determine closing character
		closeChar := byte('}')
		if openChar == '[' {
			closeChar = ']'
		}

		// Find last closing character
		lastCloseIdx := strings.LastIndexByte(s, closeChar)
		if lastCloseIdx > firstOpenIdx {
			return strings.TrimSpace(s[firstOpenIdx : lastCloseIdx+1])
		}
	}

	// Strategy 4: Return trimmed input (decode will fail)
	return s
}

// complete is the shared request path; jsonMode adds response_format.
func (c *Client) complete(ctx context.Context, system, user string, jsonMode bool) (text, model string, err error) {
	messages := make([]chatMessage, 0, 2)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, chatMessage{Role: "user", Content: user})

	body := map[string]any{
		"model":    c.model,
		"messages": messages,
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", "", fmt.Errorf("openrouter: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, completionsURL, bytes.NewReader(payload))
	if err != nil {
		return "", "", fmt.Errorf("openrouter: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("openrouter: request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", "", fmt.Errorf("openrouter: read response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var oe struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &oe)
		err := fmt.Errorf("openrouter: http %d: %s", res.StatusCode, oe.Error.Message)
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			return "", "", fmt.Errorf("%w: %w", domain.ErrUnauthorized, err)
		}
		return "", "", err
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("openrouter: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", "", fmt.Errorf("openrouter: response carried no choices")
	}
	model = out.Model
	if model == "" {
		model = c.model
	}
	return out.Choices[0].Message.Content, model, nil
}
