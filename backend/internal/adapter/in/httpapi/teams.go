package httpapi

import (
	"net/http"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Teams, membership, and email invitations (M2.7). Handlers translate
// JSON <-> port.TeamService and nothing more: every role/authorization
// decision (non-member => ErrNotFound, under-privileged => ErrForbidden,
// last-owner => ErrConflict) is made in the service and passes through the
// error codec unchanged.

func (s *server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var in port.TeamInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	team, err := s.deps.Teams.Create(r.Context(), userFrom(r).ID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (s *server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := s.deps.Teams.List(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

func (s *server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	team, members, err := s.deps.Teams.Get(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team, "members": members})
}

func (s *server) handleRenameTeam(w http.ResponseWriter, r *http.Request) {
	var in port.TeamInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	team, err := s.deps.Teams.Rename(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (s *server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Teams.Delete(r.Context(), userFrom(r).ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSetMemberRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	member, err := s.deps.Teams.SetMemberRole(
		r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("userId"), domain.TeamRole(in.Role))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (s *server) handleSetShareReadStatuses(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Share bool `json:"share"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	member, err := s.deps.Teams.SetShareReadStatuses(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Share)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (s *server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Teams.RemoveMember(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("userId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	inv, err := s.deps.Teams.Invite(r.Context(), userFrom(r).ID, r.PathValue("id"), in.Email, domain.TeamRole(in.Role))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

func (s *server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	invs, err := s.deps.Teams.ListInvitations(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invs)
}

func (s *server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Teams.RevokeInvitation(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("invitationId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAcceptInvitation redeems the raw token from the emailed invite link
// for the AUTHENTICATED caller. The service hashes the token (SHA-256) for
// lookup; unknown, expired, revoked, and already-used tokens all fail closed
// as 404 (no oracle distinguishing them).
func (s *server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	team, err := s.deps.Teams.AcceptInvitation(r.Context(), userFrom(r).ID, in.Token)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}
