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

	t.Run("editing action forwards draftId", func(t *testing.T) {
		h := newHarness(t)
		h.ai.ret = domain.AiComposeResponse{Text: "shortened", Model: "m1"}
		rec := h.authed(http.MethodPost, "/v1/ai/compose", jsonBody(t, map[string]string{"action": "shorten", "draftId": "d1"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.ai.gotReq.Action != domain.AiShorten {
			t.Fatalf("gotReq.Action = %q, want shorten", h.ai.gotReq.Action)
		}
		if h.ai.gotReq.DraftID != "d1" {
			t.Fatalf("gotReq.DraftID = %q, want d1", h.ai.gotReq.DraftID)
		}
	})

	t.Run("change_tone forwards tone", func(t *testing.T) {
		h := newHarness(t)
		h.ai.ret = domain.AiComposeResponse{Text: "retoned", Model: "m1"}
		rec := h.authed(http.MethodPost, "/v1/ai/compose", jsonBody(t, map[string]string{"action": "change_tone", "draftId": "d1", "tone": "more formal"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.ai.gotReq.Action != domain.AiChangeTone {
			t.Fatalf("gotReq.Action = %q, want change_tone", h.ai.gotReq.Action)
		}
		if h.ai.gotReq.Tone != "more formal" {
			t.Fatalf("gotReq.Tone = %q, want %q", h.ai.gotReq.Tone, "more formal")
		}
	})

	t.Run("editing action validation error mapped to 400", func(t *testing.T) {
		h := newHarness(t)
		h.ai.err = domain.ErrValidation
		rec := h.authed(http.MethodPost, "/v1/ai/compose", jsonBody(t, map[string]string{"action": "shorten"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
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
