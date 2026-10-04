package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// CRM contact context + explicit email logging (M2.8 Task 16). Handlers only
// translate HTTP/JSON; entitlement, vendor fan-out, token refresh, and
// caching live in the CRM service. When the service is not wired (vendor
// unconfigured, or the integration-connection repo not composed yet) the
// surface answers 501 and GET /v1/instance advertises features.hubspot=false.

func (s *server) crm(w http.ResponseWriter, r *http.Request) (port.CrmService, bool) {
	if s.deps.Crm == nil {
		s.writeError(w, r, fmt.Errorf("%w: CRM integrations are not enabled on this instance", domain.ErrNotImplemented))
		return nil, false
	}
	return s.deps.Crm, true
}

// handleCrmContext serves GET /v1/crm/context?email= — everything the
// contact pane shows for one address, one entry per connected CRM vendor
// (empty array when none are connected; the pane hides its CRM section).
func (s *server) handleCrmContext(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.crm(w, r)
	if !ok {
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" {
		s.writeError(w, r, fmt.Errorf("%w: email query parameter is required", domain.ErrValidation))
		return
	}
	contexts, err := svc.ContactContext(r.Context(), userFrom(r).ID, email)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if contexts == nil {
		contexts = []domain.CrmContext{}
	}
	writeJSON(w, http.StatusOK, contexts)
}

// handleCrmLog serves POST /v1/crm/log — an explicit, per-message user
// action ("Log to HubSpot"). Mail is never exported to a CRM in bulk or
// implicitly; every log call corresponds to one deliberate click.
func (s *server) handleCrmLog(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.crm(w, r)
	if !ok {
		return
	}
	var in domain.CrmEmailLog
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	var fc fieldCheck
	fc.email("contactEmail", in.ContactEmail)
	fc.title("subject", in.Subject)
	fc.text("bodyText", in.BodyText)
	if err := fc.err(); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := svc.LogEmail(r.Context(), userFrom(r).ID, in); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
