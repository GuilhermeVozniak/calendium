package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestWriteErrorPaymentRequiredDetails(t *testing.T) {
	s := &server{deps: Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	trialEnd := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

	t.Run("typed error renders details", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
		err := fmt.Errorf("mail: %w", &domain.PaymentRequiredError{Reason: domain.DenialTrialEnded, TrialEndsAt: &trialEnd})
		s.writeError(rec, req, err)
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", rec.Code)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Reason           string  `json:"reason"`
					TrialEndsAt      *string `json:"trialEndsAt"`
					CurrentPeriodEnd *string `json:"currentPeriodEnd"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		if body.Error.Code != "payment_required" || body.Error.Message != "An active subscription is required." {
			t.Fatalf("envelope = %+v", body.Error)
		}
		if body.Error.Details.Reason != "trial_ended" {
			t.Fatalf("reason = %q", body.Error.Details.Reason)
		}
		if body.Error.Details.TrialEndsAt == nil || *body.Error.Details.TrialEndsAt != "2026-10-18T09:00:00Z" {
			t.Fatalf("trialEndsAt = %v", body.Error.Details.TrialEndsAt)
		}
		var raw map[string]map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		var details map[string]json.RawMessage
		_ = json.Unmarshal(raw["error"]["details"], &details)
		if _, ok := details["currentPeriodEnd"]; ok {
			t.Fatal("nil currentPeriodEnd must be omitted")
		}
	})

	t.Run("bare sentinel has no details key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/mail/threads", nil)
		s.writeError(rec, req, domain.ErrPaymentRequired)
		var raw map[string]map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["error"]["details"]; ok {
			t.Fatalf("details must be omitted without a typed error: %s", rec.Body.String())
		}
	})

	t.Run("new sentinels map to their codes", func(t *testing.T) {
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{domain.ErrAlreadySubscribed, http.StatusConflict, "already_subscribed"},
			{domain.ErrNoBillingProfile, http.StatusBadRequest, "no_billing_profile"},
			{domain.ErrBillingUnavailable, http.StatusBadGateway, "billing_unavailable"},
		} {
			status, code := statusFor(fmt.Errorf("wrap: %w", tc.err))
			if status != tc.status || code != tc.code {
				t.Fatalf("statusFor(%v) = %d/%q, want %d/%q", tc.err, status, code, tc.status, tc.code)
			}
			if safeMessage(code) == "Internal server error." {
				t.Fatalf("safeMessage(%q) must have a dedicated message", code)
			}
		}
		if !errors.Is(fmt.Errorf("x: %w", domain.ErrAlreadySubscribed), domain.ErrAlreadySubscribed) {
			t.Fatal("sanity")
		}
	})
}
