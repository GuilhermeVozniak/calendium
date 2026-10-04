package httpapi

import (
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
)

// requireIntegrations answers 501 when the integration service is not wired
// (the Deps-nil precedent: Collab/Delegations/TeamActivity). Returns true
// when the request was already answered.
func (s *server) requireIntegrations(w http.ResponseWriter, r *http.Request) bool {
	if s.deps.Integrations == nil {
		s.writeError(w, r, fmt.Errorf("%w: integrations are not available on this deployment", domain.ErrNotImplemented))
		return false
	}
	return true
}

func (s *server) handleListIntegrations(w http.ResponseWriter, r *http.Request) {
	if !s.requireIntegrations(w, r) {
		return
	}
	conns, err := s.deps.Integrations.List(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conns)
}

func (s *server) handleConnectIntegration(w http.ResponseWriter, r *http.Request) {
	if !s.requireIntegrations(w, r) {
		return
	}
	vendor, err := domain.ParseIntegrationVendor(r.PathValue("vendor"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var in struct {
		RedirectURL string `json:"redirectUrl"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	authURL, err := s.deps.Integrations.BeginConnect(
		r.Context(), userFrom(r).ID, vendor, in.RedirectURL, s.requestBaseURL(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": authURL})
}

// handleIntegrationCallback is the browser-facing vendor OAuth redirect
// target (unauthenticated — the one-time state authenticates the flow),
// mirroring handleAccountCallback: on success or failure it 302s the browser
// back to the client's stored redirectUrl with ?status=connected|error, and
// only when the state itself is invalid — so no client redirect is known —
// does it render a static page (fail closed, no token exchange happened).
func (s *server) handleIntegrationCallback(w http.ResponseWriter, r *http.Request) {
	if s.deps.Integrations == nil {
		writeCallbackPage(w, http.StatusNotImplemented, "Connection failed",
			"Integrations are not available on this deployment.")
		return
	}
	vendor, err := domain.ParseIntegrationVendor(r.PathValue("vendor"))
	if err != nil {
		writeCallbackPage(w, http.StatusBadRequest, "Connection failed", "Unknown integration vendor.")
		return
	}
	q := r.URL.Query()
	_, redirect, cerr := s.deps.Integrations.CompleteConnect(
		r.Context(), vendor, q.Get("state"), q.Get("code"), s.requestBaseURL(r))
	if cerr != nil {
		status, code := statusFor(cerr)
		if status == http.StatusInternalServerError {
			s.deps.Logger.Error("integration oauth callback failed", "vendor", vendor, "error", cerr)
		} else {
			s.deps.Logger.Info("integration oauth callback rejected", "vendor", vendor, "code", code, "error", cerr)
		}
		if redirect != "" {
			http.Redirect(w, r, withStatusParam(redirect, "error"), http.StatusFound)
			return
		}
		detail := safeMessage(code)
		if status == http.StatusInternalServerError {
			detail = "Something went wrong while connecting the integration. Please try again."
		}
		writeCallbackPage(w, status, "Connection failed", detail)
		return
	}
	http.Redirect(w, r, withStatusParam(redirect, "connected"), http.StatusFound)
}

func (s *server) handleDisconnectIntegration(w http.ResponseWriter, r *http.Request) {
	if !s.requireIntegrations(w, r) {
		return
	}
	if err := s.deps.Integrations.Disconnect(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
