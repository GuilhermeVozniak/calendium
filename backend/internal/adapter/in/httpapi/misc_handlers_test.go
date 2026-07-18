package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestHandleSearch(t *testing.T) {
	t.Run("forwards q and returns result", func(t *testing.T) {
		h := newHarness(t)
		h.search.ret = port.SearchResult{Threads: []domain.Thread{{ID: "th1"}}}
		rec := h.authed(http.MethodGet, "/v1/search?q=quarterly%20report", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.search.gotQ != "quarterly report" {
			t.Fatalf("gotQ = %q, want %q", h.search.gotQ, "quarterly report")
		}
		var got port.SearchResult
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got.Threads) != 1 || got.Threads[0].ID != "th1" {
			t.Fatalf("threads = %+v", got.Threads)
		}
	})

	t.Run("empty q forwarded, validation is service-side", func(t *testing.T) {
		h := newHarness(t)
		h.search.err = domain.ErrValidation
		rec := h.authed(http.MethodGet, "/v1/search", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.search.gotQ != "" {
			t.Fatalf("gotQ = %q, want empty (raw q forwarded as-is)", h.search.gotQ)
		}
	})
}

func TestHandleAiCompose(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.ai.ret = domain.AiComposeResponse{Text: "hello", Model: "m1"}
		rec := h.authed(http.MethodPost, "/v1/ai/compose", jsonBody(t, map[string]string{"action": "compose", "prompt": "draft a reply"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.ai.gotReq.Action != domain.AiCompose {
			t.Fatalf("gotReq.Action = %q, want compose", h.ai.gotReq.Action)
		}
		if h.ai.gotReq.Prompt != "draft a reply" {
			t.Fatalf("gotReq.Prompt = %q, want %q", h.ai.gotReq.Prompt, "draft a reply")
		}
		var got domain.AiComposeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Text != "hello" {
			t.Fatalf("text = %q, want hello", got.Text)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/ai/compose", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleAiAsk(t *testing.T) {
	t.Run("success shape", func(t *testing.T) {
		h := newHarness(t)
		h.ai.askRet = domain.AiAskResponse{
			Answer: "yes, Friday at 9am",
			Model:  "openrouter/auto",
			Sources: []domain.AiSource{
				{ThreadID: "t1", MessageID: "m1", Subject: "Travel plans", Snippet: "Flight leaves at 9am on Friday."},
			},
		}
		rec := h.authed(http.MethodPost, "/v1/ai/ask", jsonBody(t, map[string]string{"question": "when's the flight?", "threadId": "t1"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.ai.gotAskReq.Question != "when's the flight?" || h.ai.gotAskReq.ThreadID != "t1" {
			t.Fatalf("gotAskReq = %+v, want question+threadId forwarded", h.ai.gotAskReq)
		}
		var got domain.AiAskResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Answer != "yes, Friday at 9am" || got.Model != "openrouter/auto" {
			t.Fatalf("response = %+v, want answer/model echoed", got)
		}
		if len(got.Sources) != 1 || got.Sources[0].ThreadID != "t1" || got.Sources[0].MessageID != "m1" ||
			got.Sources[0].Subject != "Travel plans" || got.Sources[0].Snippet != "Flight leaves at 9am on Friday." {
			t.Fatalf("sources = %+v, want {threadId,messageId,subject,snippet}", got.Sources)
		}
	})

	t.Run("empty question is a 400", func(t *testing.T) {
		h := newHarness(t)
		h.ai.askErr = domain.ErrValidation
		rec := h.authed(http.MethodPost, "/v1/ai/ask", jsonBody(t, map[string]string{"question": ""}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("AI unavailable (no key) is a 503", func(t *testing.T) {
		h := newHarness(t)
		h.ai.askErr = domain.ErrAIUnavailable
		rec := h.authed(http.MethodPost, "/v1/ai/ask", jsonBody(t, map[string]string{"question": "anything?"}))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/ai/ask", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleRegisterDevice(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		h.devices.registerRet = domain.NotificationDevice{ID: "dev1"}
		rec := h.authed(http.MethodPost, "/v1/devices", jsonBody(t, map[string]string{"platform": "ios", "token": "abc"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.devices.gotPlatform != domain.PlatformIOS {
			t.Fatalf("gotPlatform = %q, want ios", h.devices.gotPlatform)
		}
		if h.devices.gotToken != "abc" {
			t.Fatalf("gotToken = %q, want abc", h.devices.gotToken)
		}
		var got domain.NotificationDevice
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "dev1" {
			t.Fatalf("id = %q, want dev1", got.ID)
		}
	})

	t.Run("unknown platform enum forwarded raw, rejected service-side", func(t *testing.T) {
		h := newHarness(t)
		h.devices.registerErr = domain.ErrValidation
		rec := h.authed(http.MethodPost, "/v1/devices", jsonBody(t, map[string]string{"platform": "blackberry", "token": "t"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
		if h.devices.gotPlatform != domain.DevicePlatform("blackberry") {
			t.Fatalf("gotPlatform = %q, want blackberry (raw value forwarded)", h.devices.gotPlatform)
		}
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/devices", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleUnregisterDevice(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/devices/dev1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.devices.gotUnregID != "dev1" {
			t.Fatalf("gotUnregID = %q, want dev1", h.devices.gotUnregID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newHarness(t)
		h.devices.unregisterErr = domain.ErrNotFound
		rec := h.authed(http.MethodDelete, "/v1/devices/dev1", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
