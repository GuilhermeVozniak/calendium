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
func (c *Client) Complete(ctx context.Context, system, user string) (text, model string, err error) {
	messages := make([]chatMessage, 0, 2)
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}
	messages = append(messages, chatMessage{Role: "user", Content: user})

	payload, err := json.Marshal(map[string]any{
		"model":    c.model,
		"messages": messages,
	})
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
	defer res.Body.Close()

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
