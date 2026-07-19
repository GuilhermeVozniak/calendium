package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

func TestTeamsCreate(t *testing.T) {
	t.Run("201 created", func(t *testing.T) {
		h := newHarness(t)
		h.teams.createRet = domain.Team{ID: "team_1", Name: "Ops", CreatedBy: defaultUserID}
		rec := h.authed(http.MethodPost, "/v1/teams", jsonBody(t, map[string]string{"name": "Ops"}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
		}
		var got domain.Team
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "team_1" || got.Name != "Ops" {
			t.Fatalf("team = %+v", got)
		}
		if h.teams.gotCreate.Name != "Ops" {
			t.Fatalf("gotCreate = %+v, want Name=Ops", h.teams.gotCreate)
		}
		if h.teams.gotUserID != defaultUserID {
			t.Fatalf("gotUserID = %q, want %q", h.teams.gotUserID, defaultUserID)
		}
	})

	t.Run("400 validation error from service", func(t *testing.T) {
		h := newHarness(t)
		h.teams.createErr = fmt.Errorf("%w: team name must be 1-120 characters", domain.ErrValidation)
		rec := h.authed(http.MethodPost, "/v1/teams", jsonBody(t, map[string]string{"name": ""}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("400 malformed JSON", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/teams", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("402 payment required propagated", func(t *testing.T) {
		h := newHarness(t)
		h.teams.createErr = domain.ErrPaymentRequired
		rec := h.authed(http.MethodPost, "/v1/teams", jsonBody(t, map[string]string{"name": "Ops"}))
		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsList(t *testing.T) {
	t.Run("200 list", func(t *testing.T) {
		h := newHarness(t)
		h.teams.setTeams([]domain.Team{{ID: "team_1", Name: "Ops"}, {ID: "team_2", Name: "Design"}})
		rec := h.authed(http.MethodGet, "/v1/teams", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got []domain.Team
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 2 || got[0].ID != "team_1" || got[1].ID != "team_2" {
			t.Fatalf("teams = %+v", got)
		}
	})

	t.Run("401 without bearer", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodGet, "/v1/teams", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

func TestTeamsGet(t *testing.T) {
	t.Run("200 team with members", func(t *testing.T) {
		h := newHarness(t)
		h.teams.getTeamRet = domain.Team{ID: "team_1", Name: "Ops"}
		h.teams.getMembersRet = []domain.TeamMember{
			{TeamID: "team_1", UserID: defaultUserID, Role: domain.TeamRoleOwner},
			{TeamID: "team_1", UserID: "user_2", Role: domain.TeamRoleMember, ShareReadStatuses: true},
		}
		rec := h.authed(http.MethodGet, "/v1/teams/team_1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got struct {
			Team    domain.Team         `json:"team"`
			Members []domain.TeamMember `json:"members"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Team.ID != "team_1" || len(got.Members) != 2 || got.Members[1].Role != domain.TeamRoleMember {
			t.Fatalf("got = %+v", got)
		}
		if h.teams.gotGetTeamID != "team_1" {
			t.Fatalf("gotGetTeamID = %q, want team_1", h.teams.gotGetTeamID)
		}
	})

	t.Run("404 non-member", func(t *testing.T) {
		h := newHarness(t)
		h.teams.getErr = fmt.Errorf("%w: team not found", domain.ErrNotFound)
		rec := h.authed(http.MethodGet, "/v1/teams/team_x", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})
}

func TestTeamsRename(t *testing.T) {
	t.Run("200 renamed", func(t *testing.T) {
		h := newHarness(t)
		h.teams.renameRet = domain.Team{ID: "team_1", Name: "Platform"}
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1", jsonBody(t, map[string]string{"name": "Platform"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.teams.gotRenameID != "team_1" || h.teams.gotRenameValue != "Platform" {
			t.Fatalf("gotRenameID=%q gotRenameValue=%q", h.teams.gotRenameID, h.teams.gotRenameValue)
		}
	})

	t.Run("403 insufficient role", func(t *testing.T) {
		h := newHarness(t)
		h.teams.renameErr = fmt.Errorf("%w: requires the admin role", domain.ErrForbidden)
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1", jsonBody(t, map[string]string{"name": "Platform"}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "forbidden" {
			t.Fatalf("code = %q, want forbidden", got.Code)
		}
	})
}

func TestTeamsDelete(t *testing.T) {
	t.Run("204 deleted", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.teams.gotDeleteID != "team_1" {
			t.Fatalf("gotDeleteID = %q, want team_1", h.teams.gotDeleteID)
		}
	})

	t.Run("403 non-owner", func(t *testing.T) {
		h := newHarness(t)
		h.teams.deleteErr = fmt.Errorf("%w: requires the owner role", domain.ErrForbidden)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsSetMemberRole(t *testing.T) {
	t.Run("200 role changed", func(t *testing.T) {
		h := newHarness(t)
		h.teams.setRoleRet = domain.TeamMember{TeamID: "team_1", UserID: "user_2", Role: domain.TeamRoleAdmin}
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1/members/user_2", jsonBody(t, map[string]string{"role": "admin"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got domain.TeamMember
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Role != domain.TeamRoleAdmin {
			t.Fatalf("member = %+v", got)
		}
		if h.teams.gotRoleTeamID != "team_1" || h.teams.gotRoleMemberID != "user_2" || h.teams.gotRole != domain.TeamRoleAdmin {
			t.Fatalf("got teamID=%q memberID=%q role=%q", h.teams.gotRoleTeamID, h.teams.gotRoleMemberID, h.teams.gotRole)
		}
	})

	t.Run("400 bad role", func(t *testing.T) {
		h := newHarness(t)
		h.teams.setRoleErr = fmt.Errorf("%w: unknown team role %q", domain.ErrValidation, "superuser")
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1/members/user_2", jsonBody(t, map[string]string{"role": "superuser"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "validation_failed" {
			t.Fatalf("code = %q, want validation_failed", got.Code)
		}
	})

	t.Run("409 last owner demotion", func(t *testing.T) {
		h := newHarness(t)
		h.teams.setRoleErr = fmt.Errorf("%w: a team must keep at least one owner", domain.ErrConflict)
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1/members/user_1", jsonBody(t, map[string]string{"role": "member"}))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "conflict" {
			t.Fatalf("code = %q, want conflict", got.Code)
		}
	})

	t.Run("403 only owners grant owner", func(t *testing.T) {
		h := newHarness(t)
		h.teams.setRoleErr = fmt.Errorf("%w: only owners may grant or revoke the owner role", domain.ErrForbidden)
		rec := h.authed(http.MethodPatch, "/v1/teams/team_1/members/user_2", jsonBody(t, map[string]string{"role": "owner"}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsShareReadStatuses(t *testing.T) {
	t.Run("200 opt-in recorded", func(t *testing.T) {
		h := newHarness(t)
		h.teams.shareRet = domain.TeamMember{TeamID: "team_1", UserID: defaultUserID, ShareReadStatuses: true}
		rec := h.authed(http.MethodPut, "/v1/teams/team_1/read-status-sharing", jsonBody(t, map[string]bool{"share": true}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got domain.TeamMember
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !got.ShareReadStatuses {
			t.Fatalf("member = %+v", got)
		}
		if h.teams.gotShareTeamID != "team_1" || !h.teams.gotShare {
			t.Fatalf("gotShareTeamID=%q gotShare=%v", h.teams.gotShareTeamID, h.teams.gotShare)
		}
	})

	t.Run("404 non-member", func(t *testing.T) {
		h := newHarness(t)
		h.teams.shareErr = fmt.Errorf("%w: team not found", domain.ErrNotFound)
		rec := h.authed(http.MethodPut, "/v1/teams/team_x/read-status-sharing", jsonBody(t, map[string]bool{"share": false}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsRemoveMember(t *testing.T) {
	t.Run("204 removed", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1/members/user_2", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.teams.gotRemoveTeamID != "team_1" || h.teams.gotRemoveUserID != "user_2" {
			t.Fatalf("gotRemoveTeamID=%q gotRemoveUserID=%q", h.teams.gotRemoveTeamID, h.teams.gotRemoveUserID)
		}
	})

	t.Run("409 last owner leaving", func(t *testing.T) {
		h := newHarness(t)
		h.teams.removeErr = fmt.Errorf("%w: a team must keep at least one owner", domain.ErrConflict)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1/members/"+defaultUserID, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("403 member removing another member", func(t *testing.T) {
		h := newHarness(t)
		h.teams.removeErr = fmt.Errorf("%w: requires the admin role", domain.ErrForbidden)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1/members/user_2", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsInvite(t *testing.T) {
	t.Run("201 invitation created", func(t *testing.T) {
		h := newHarness(t)
		h.teams.inviteRet = domain.TeamInvitation{
			ID: "inv_1", TeamID: "team_1", Email: "new@example.com",
			Role: domain.TeamRoleMember, Status: domain.InvitePending,
		}
		rec := h.authed(http.MethodPost, "/v1/teams/team_1/invitations",
			jsonBody(t, map[string]string{"email": "new@example.com", "role": "member"}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
		}
		var got domain.TeamInvitation
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "inv_1" || got.Status != domain.InvitePending {
			t.Fatalf("invitation = %+v", got)
		}
		if h.teams.gotInviteTeamID != "team_1" || h.teams.gotInviteEmail != "new@example.com" || h.teams.gotInviteRole != domain.TeamRoleMember {
			t.Fatalf("got teamID=%q email=%q role=%q", h.teams.gotInviteTeamID, h.teams.gotInviteEmail, h.teams.gotInviteRole)
		}
	})

	t.Run("token hash never serialized", func(t *testing.T) {
		h := newHarness(t)
		h.teams.inviteRet = domain.TeamInvitation{ID: "inv_1", TokenHash: "secret-hash"}
		rec := h.authed(http.MethodPost, "/v1/teams/team_1/invitations",
			jsonBody(t, map[string]string{"email": "new@example.com", "role": "member"}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret-hash") || strings.Contains(rec.Body.String(), "tokenHash") {
			t.Fatalf("token hash leaked: %s", rec.Body.String())
		}
	})

	t.Run("400 invalid email", func(t *testing.T) {
		h := newHarness(t)
		h.teams.inviteErr = fmt.Errorf("%w: invalid invitation email address", domain.ErrValidation)
		rec := h.authed(http.MethodPost, "/v1/teams/team_1/invitations",
			jsonBody(t, map[string]string{"email": "not-an-email", "role": "member"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("403 member cannot invite", func(t *testing.T) {
		h := newHarness(t)
		h.teams.inviteErr = fmt.Errorf("%w: requires the admin role", domain.ErrForbidden)
		rec := h.authed(http.MethodPost, "/v1/teams/team_1/invitations",
			jsonBody(t, map[string]string{"email": "new@example.com", "role": "member"}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsListInvitations(t *testing.T) {
	t.Run("200 list", func(t *testing.T) {
		h := newHarness(t)
		h.teams.listInvsRet = []domain.TeamInvitation{{ID: "inv_1", TeamID: "team_1"}}
		rec := h.authed(http.MethodGet, "/v1/teams/team_1/invitations", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got []domain.TeamInvitation
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 1 || got[0].ID != "inv_1" {
			t.Fatalf("invitations = %+v", got)
		}
		if h.teams.gotListInvsTeamID != "team_1" {
			t.Fatalf("gotListInvsTeamID = %q, want team_1", h.teams.gotListInvsTeamID)
		}
	})

	t.Run("404 non-member", func(t *testing.T) {
		h := newHarness(t)
		h.teams.listInvsErr = fmt.Errorf("%w: team not found", domain.ErrNotFound)
		rec := h.authed(http.MethodGet, "/v1/teams/team_x/invitations", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsRevokeInvitation(t *testing.T) {
	t.Run("204 revoked", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1/invitations/inv_1", nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if h.teams.gotRevokeTeamID != "team_1" || h.teams.gotRevokeInvID != "inv_1" {
			t.Fatalf("gotRevokeTeamID=%q gotRevokeInvID=%q", h.teams.gotRevokeTeamID, h.teams.gotRevokeInvID)
		}
	})

	t.Run("409 not pending", func(t *testing.T) {
		h := newHarness(t)
		h.teams.revokeErr = fmt.Errorf("%w: invitation is not pending", domain.ErrConflict)
		rec := h.authed(http.MethodDelete, "/v1/teams/team_1/invitations/inv_1", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestTeamsAcceptInvitation(t *testing.T) {
	t.Run("200 joined", func(t *testing.T) {
		h := newHarness(t)
		h.teams.acceptRet = domain.Team{ID: "team_1", Name: "Ops"}
		rec := h.authed(http.MethodPost, "/v1/invitations/accept", jsonBody(t, map[string]string{"token": "raw-token"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		var got domain.Team
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "team_1" {
			t.Fatalf("team = %+v", got)
		}
		if h.teams.gotAcceptUserID != defaultUserID || h.teams.gotAcceptToken != "raw-token" {
			t.Fatalf("gotAcceptUserID=%q gotAcceptToken=%q", h.teams.gotAcceptUserID, h.teams.gotAcceptToken)
		}
	})

	t.Run("404 invalid token fails closed", func(t *testing.T) {
		h := newHarness(t)
		h.teams.acceptErr = domain.ErrNotFound
		rec := h.authed(http.MethodPost, "/v1/invitations/accept", jsonBody(t, map[string]string{"token": "bogus"}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if got := decodeErr(t, rec); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found", got.Code)
		}
	})

	t.Run("400 malformed JSON", func(t *testing.T) {
		h := newHarness(t)
		rec := h.authed(http.MethodPost, "/v1/invitations/accept", strings.NewReader("{"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("401 without bearer", func(t *testing.T) {
		h := newHarness(t)
		rec := h.anon(http.MethodPost, "/v1/invitations/accept", jsonBody(t, map[string]string{"token": "raw-token"}))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}
