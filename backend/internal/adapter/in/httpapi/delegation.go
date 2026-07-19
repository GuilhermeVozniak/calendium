package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"calendium/backend/internal/domain"
)

// EA delegation mode (M2.7 Task 15): an assistant acts as a principal over
// the normal API by sending the principal's user id in X-Calendium-Act-As.
// The middleware authorizes the grant per request (fail closed, never
// cached — revocation is immediate), swaps the principal into the request
// context so handlers are untouched, and audit-logs every successful
// delegated mutation with the REAL actor (the assistant) and the principal.

// actAsHeader carries the principal user id the assistant acts for.
const actAsHeader = "X-Calendium-Act-As"

type actorCtxKey struct{}

// actorFrom returns the real authenticated actor (the assistant) when the
// request is delegated; ok=false on a normal, non-delegated request.
func actorFrom(r *http.Request) (domain.User, bool) {
	u, ok := r.Context().Value(actorCtxKey{}).(domain.User)
	return u, ok
}

// delegationScopeForRoute maps a route to the scope required to act-as
// through it plus the audited resource type ("mail", "calendar", "event" —
// following the scope map's route groups, recorded on the unified
// domain.AuditEntry). ok=false means the route is not delegable at all —
// teams, billing, accounts, devices, delegations, and anything not
// explicitly a mail/calendar surface reject act-as outright (fail closed).
func delegationScopeForRoute(method, path string) (domain.DelegationScope, string, bool) {
	if delegationDeniedSubpath(path) {
		return "", "", false
	}
	read := method == http.MethodGet || method == http.MethodHead
	switch {
	case path == "/v1/search":
		if read {
			return domain.ScopeMailRead, "mail", true
		}
		return "", "", false
	case path == "/v1/mail" || strings.HasPrefix(path, "/v1/mail/"):
		if read {
			return domain.ScopeMailRead, "mail", true
		}
		return domain.ScopeMailWrite, "mail", true
	case path == "/v1/events" || strings.HasPrefix(path, "/v1/events/"):
		if read {
			return domain.ScopeCalendarRead, "event", true
		}
		return domain.ScopeCalendarWrite, "event", true
	case path == "/v1/calendars" || strings.HasPrefix(path, "/v1/calendars/"),
		path == "/v1/availability":
		if read {
			return domain.ScopeCalendarRead, "calendar", true
		}
		return domain.ScopeCalendarWrite, "calendar", true
	}
	return "", "", false
}

// delegationDeniedSubpath reports whether path is a team/collaboration
// sub-surface that must never be reachable through act-as even though it
// sits under a delegable mail/calendar prefix. A delegated assistant must
// not mint external thread-share tokens (durable access surviving
// revocation), manage thread or calendar shares (incl. self-granting a
// calendar share), read or post team comments, read team activity, or read
// or mutate snippets (team snippet bodies are a team surface and the list
// endpoint cannot be split by scope at the path level — fail closed).
// /v1/comments/{id} is already non-delegable (no matching prefix).
func delegationDeniedSubpath(path string) bool {
	if path == "/v1/mail/snippets" || strings.HasPrefix(path, "/v1/mail/snippets/") {
		return true
	}
	if rest, ok := strings.CutPrefix(path, "/v1/mail/threads/"); ok {
		if _, sub, ok := strings.Cut(rest, "/"); ok {
			switch {
			case sub == "share", sub == "shares", strings.HasPrefix(sub, "shares/"),
				sub == "comments", sub == "team-activity":
				return true
			}
		}
	}
	if rest, ok := strings.CutPrefix(path, "/v1/calendars/"); ok {
		if _, sub, ok := strings.Cut(rest, "/"); ok {
			if sub == "shares" || strings.HasPrefix(sub, "shares/") {
				return true
			}
		}
	}
	return false
}

// auditRoute renders the matched route pattern ("/v1/mail/threads/{id}/actions")
// for audit attribution, falling back to the raw path.
func auditRoute(r *http.Request) string {
	if r.Pattern != "" {
		if _, route, ok := strings.Cut(r.Pattern, " "); ok {
			return route
		}
		return r.Pattern
	}
	return r.URL.Path
}

// withActAs runs between requireAuth and the route handler. Without the
// act-as header it is a no-op. With it, the request proceeds only when the
// route is delegable, the grant authorizes the mapped scope right now, and
// the principal resolves — anything else is 403. On success the principal
// replaces the user in context (handlers keep reading userFrom) while the
// assistant is retained as the actor; a successful mutation is then
// audit-logged with both identities.
func (s *server) withActAs(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principalID := strings.TrimSpace(r.Header.Get(actAsHeader))
		if principalID == "" {
			next(w, r)
			return
		}
		assistant := userFrom(r)
		if s.deps.Delegations == nil {
			s.writeError(w, r, fmt.Errorf("%w: delegation is not enabled on this instance", domain.ErrForbidden))
			return
		}
		scope, resourceType, ok := delegationScopeForRoute(r.Method, r.URL.Path)
		if !ok {
			s.writeError(w, r, fmt.Errorf("%w: this route cannot be used on behalf of another user", domain.ErrForbidden))
			return
		}
		if err := s.deps.Delegations.Authorize(r.Context(), assistant.ID, principalID, scope); err != nil {
			s.writeError(w, r, err)
			return
		}
		principal, err := s.deps.Users.GetUser(r.Context(), principalID)
		if err != nil {
			// Fail closed: an unresolvable principal is a forbidden act-as.
			s.writeError(w, r, fmt.Errorf("%w: principal not found", domain.ErrForbidden))
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, principal)
		ctx = context.WithValue(ctx, actorCtxKey{}, assistant)
		r = r.WithContext(ctx)

		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			// Reads are not audit-logged (by decision — volume vs. value;
			// the grant itself bounds read exposure).
			next(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if rec.status >= 400 {
			return // only successful mutations are audited
		}
		entry := domain.AuditEntry{
			PrincipalID:  principal.ID,
			ActorID:      assistant.ID, // the REAL actor stays attributable
			Action:       r.Method + " " + auditRoute(r),
			ResourceType: resourceType,
			ResourceID:   r.PathValue("id"),
			Metadata:     map[string]any{"route": auditRoute(r), "delegated": true},
		}
		if err := s.deps.Delegations.RecordAudit(r.Context(), entry); err != nil {
			s.deps.Logger.Error("delegated mutation succeeded but audit record failed",
				"principal", entry.PrincipalID, "actor", entry.ActorID,
				"action", entry.Action, "resource", entry.ResourceID, "error", err)
		}
	}
}

// --- handlers ----------------------------------------------------------------

func (s *server) handleCreateDelegation(w http.ResponseWriter, r *http.Request) {
	if s.deps.Delegations == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	var in struct {
		AssistantEmail string   `json:"assistantEmail"`
		Scopes         []string `json:"scopes"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	scopes := make([]domain.DelegationScope, 0, len(in.Scopes))
	for _, raw := range in.Scopes {
		scope, err := domain.ParseDelegationScope(raw)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		scopes = append(scopes, scope)
	}
	d, err := s.deps.Delegations.Create(r.Context(), userFrom(r).ID, in.AssistantEmail, scopes)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *server) handleListDelegations(w http.ResponseWriter, r *http.Request) {
	if s.deps.Delegations == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	asPrincipal, asAssistant, err := s.deps.Delegations.List(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asPrincipal": asPrincipal,
		"asAssistant": asAssistant,
	})
}

func (s *server) handleAcceptDelegation(w http.ResponseWriter, r *http.Request) {
	if s.deps.Delegations == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	d, err := s.deps.Delegations.Accept(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) handleRevokeDelegation(w http.ResponseWriter, r *http.Request) {
	if s.deps.Delegations == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	if err := s.deps.Delegations.Revoke(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDelegationAudit(w http.ResponseWriter, r *http.Request) {
	if s.deps.Delegations == nil {
		s.writeError(w, r, domain.ErrNotImplemented)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			s.writeError(w, r, fmt.Errorf("%w: limit must be a non-negative integer", domain.ErrValidation))
			return
		}
		limit = n
	}
	entries, err := s.deps.Delegations.Audit(r.Context(), userFrom(r).ID, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
