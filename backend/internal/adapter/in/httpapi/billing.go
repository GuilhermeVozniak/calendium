package httpapi

import (
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

func (s *server) handleCreateCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SuccessURL string `json:"successUrl"`
		CancelURL  string `json:"cancelUrl"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	url, err := s.deps.Billing.CreateCheckoutSession(r.Context(), userFrom(r).ID, in.SuccessURL, in.CancelURL)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *server) handleCreatePortal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReturnURL string `json:"returnUrl"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	url, err := s.deps.Billing.CreatePortalSession(r.Context(), userFrom(r).ID, in.ReturnURL)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleStripeWebhook is unauthenticated; the Stripe-Signature header is
// verified (HMAC-SHA256) and events are deduplicated inside Billing.
func (s *server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.writeError(w, r, domain.ErrValidation)
		return
	}
	if err := s.deps.Billing.HandleWebhook(r.Context(), payload, r.Header.Get("Stripe-Signature")); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}
