package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"calendium/backend/internal/domain"
)

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	// requireAuth already upserted the user for this request.
	writeJSON(w, http.StatusOK, userFrom(r))
}

func (s *server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.deps.Billing.GetSubscription(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// handleCreateCheckout takes no request body: success/cancel destinations
// are never client-supplied (the web /checkout page owns successUrl).
func (s *server) handleCreateCheckout(w http.ResponseWriter, r *http.Request) {
	url, err := s.deps.Billing.CreateCheckout(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleCreatePortal takes no request body and returns temporary portal
// links; cancelUrl/updatePaymentUrl are empty strings without a subscription.
func (s *server) handleCreatePortal(w http.ResponseWriter, r *http.Request) {
	urls, err := s.deps.Billing.CreatePortalSession(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"overviewUrl":      urls.Overview,
		"cancelUrl":        urls.Cancel,
		"updatePaymentUrl": urls.UpdatePayment,
	})
}

// handlePaddleWebhook is unauthenticated; the Paddle-Signature header is
// verified (HMAC-SHA256) and events are deduplicated/ordered inside Billing.
// Bodies are capped at 1 MB.
func (s *server) handlePaddleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeError(w, r, fmt.Errorf("%w: webhook body exceeds 1 MiB", errPayloadTooLarge))
			return
		}
		s.writeError(w, r, domain.ErrValidation)
		return
	}
	if err := s.deps.Billing.HandleWebhook(r.Context(), payload, r.Header.Get("Paddle-Signature")); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}
