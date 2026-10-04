package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func TestStatusFor(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation", fmt.Errorf("x: %w", domain.ErrValidation), http.StatusBadRequest, "validation_failed"},
		{"payload too large", fmt.Errorf("x: %w", errPayloadTooLarge), http.StatusRequestEntityTooLarge, "payload_too_large"},
		{"unauthorized", fmt.Errorf("x: %w", domain.ErrUnauthorized), http.StatusUnauthorized, "unauthorized"},
		{"forbidden", fmt.Errorf("x: %w", domain.ErrForbidden), http.StatusForbidden, "forbidden"},
		{"payment required", fmt.Errorf("x: %w", domain.ErrPaymentRequired), http.StatusPaymentRequired, "payment_required"},
		{"not found", fmt.Errorf("x: %w", domain.ErrNotFound), http.StatusNotFound, "not_found"},
		{"conflict", fmt.Errorf("x: %w", domain.ErrConflict), http.StatusConflict, "conflict"},
		{"self hosted", fmt.Errorf("x: %w", domain.ErrSelfHosted), http.StatusNotImplemented, "self_hosted"},
		{"ai output invalid", fmt.Errorf("x: %w", domain.ErrAIOutput), http.StatusBadGateway, "ai_output_invalid"},
		{"ai unavailable", fmt.Errorf("x: %w", domain.ErrAIUnavailable), http.StatusServiceUnavailable, "ai_unavailable"},
		{"rate limited", fmt.Errorf("x: %w", domain.ErrRateLimited), http.StatusTooManyRequests, "rate_limited"},
		{"unmapped error", errors.New("anything else"), http.StatusInternalServerError, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code := statusFor(tt.err)
			if status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("statusFor(%v) = (%d, %q), want (%d, %q)", tt.err, status, code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestSafeMessage(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"validation_failed", "The request was invalid."},
		{"timeout", "The request took too long to complete. Please try again."},
		{"payload_too_large", "The request body is too large."},
		{"unauthorized", "Authentication is required or has failed."},
		{"forbidden", "You do not have permission to perform this action."},
		{"payment_required", "An active subscription is required."},
		{"not_found", "The requested resource was not found."},
		{"conflict", "The request conflicts with the current state of the resource."},
		{"self_hosted", "Billing is disabled on self-hosted instances."},
		{"ai_output_invalid", "The AI returned an unexpected response."},
		{"ai_unavailable", "AI features are not available on this deployment."},
		{"rate_limited", "You have exceeded the usage limit. Please try again later."},
		{"internal", "Internal server error."},
		{"something_unrecognized", "Internal server error."},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := safeMessage(tt.code); got != tt.want {
				t.Fatalf("safeMessage(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// TestStatusForViaHandler drives statusFor's mapping end-to-end through a
// real handler (GET /v1/billing/subscription) so the sentinel-to-HTTP-status
// mapping is proven at the adapter boundary, not just in the unit function.
func TestStatusForViaHandler(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation", fmt.Errorf("wrapped: %w", domain.ErrValidation), http.StatusBadRequest, "validation_failed"},
		{"unauthorized", fmt.Errorf("wrapped: %w", domain.ErrUnauthorized), http.StatusUnauthorized, "unauthorized"},
		{"forbidden", fmt.Errorf("wrapped: %w", domain.ErrForbidden), http.StatusForbidden, "forbidden"},
		{"payment required", fmt.Errorf("wrapped: %w", domain.ErrPaymentRequired), http.StatusPaymentRequired, "payment_required"},
		{"not found", fmt.Errorf("wrapped: %w", domain.ErrNotFound), http.StatusNotFound, "not_found"},
		{"conflict", fmt.Errorf("wrapped: %w", domain.ErrConflict), http.StatusConflict, "conflict"},
		{"self hosted", fmt.Errorf("wrapped: %w", domain.ErrSelfHosted), http.StatusNotImplemented, "self_hosted"},
		{"ai output invalid", fmt.Errorf("wrapped: %w", domain.ErrAIOutput), http.StatusBadGateway, "ai_output_invalid"},
		{"ai unavailable", fmt.Errorf("wrapped: %w", domain.ErrAIUnavailable), http.StatusServiceUnavailable, "ai_unavailable"},
		{"rate limited", fmt.Errorf("wrapped: %w", domain.ErrRateLimited), http.StatusTooManyRequests, "rate_limited"},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.billing.subErr = tt.err
			rec := h.authed(http.MethodGet, "/v1/billing/subscription", nil)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if got := decodeErr(t, rec); got.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tt.wantCode)
			}
		})
	}
}

// TestWriteErrorNoLeak proves the internal wrapped error text (which may
// contain DSNs, credentials, etc.) is never echoed to the client.
func TestWriteErrorNoLeak(t *testing.T) {
	h := newHarness(t)
	h.billing.subErr = fmt.Errorf("db dial tcp 10.0.0.5: password=hunter2")
	rec := h.authed(http.MethodGet, "/v1/billing/subscription", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
	}
	got := decodeErr(t, rec)
	if got.Message != "Internal server error." {
		t.Fatalf("message = %q, want %q", got.Message, "Internal server error.")
	}
	body := rec.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "10.0.0.5") {
		t.Fatalf("response leaked internal error detail: %s", body)
	}
}
