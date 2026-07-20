package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseTeamRole(t *testing.T) {
	checkValidator(t, "ParseTeamRole", ParseTeamRole,
		[]string{"owner", "admin", "member"},
		[]string{"", "superadmin", "Owner", "OWNER", " owner", "guest"})
}

func TestTeamRoleAtLeast(t *testing.T) {
	tests := []struct {
		name string
		r    TeamRole
		min  TeamRole
		want bool
	}{
		{"owner at least owner", TeamRoleOwner, TeamRoleOwner, true},
		{"owner at least admin", TeamRoleOwner, TeamRoleAdmin, true},
		{"owner at least member", TeamRoleOwner, TeamRoleMember, true},
		{"admin not at least owner", TeamRoleAdmin, TeamRoleOwner, false},
		{"admin at least admin", TeamRoleAdmin, TeamRoleAdmin, true},
		{"admin at least member", TeamRoleAdmin, TeamRoleMember, true},
		{"member not at least owner", TeamRoleMember, TeamRoleOwner, false},
		{"member not at least admin", TeamRoleMember, TeamRoleAdmin, false},
		{"member at least member", TeamRoleMember, TeamRoleMember, true},
		{"unknown not at least member", TeamRole("superadmin"), TeamRoleMember, false},
		{"unknown not at least admin", TeamRole("superadmin"), TeamRoleAdmin, false},
		{"unknown not at least owner", TeamRole("superadmin"), TeamRoleOwner, false},
		{"empty not at least member", TeamRole(""), TeamRoleMember, false},
		{"member at least unknown (rank 0)", TeamRoleMember, TeamRole("superadmin"), true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.AtLeast(tt.min); got != tt.want {
				t.Fatalf("TeamRole(%q).AtLeast(%q) = %v, want %v", tt.r, tt.min, got, tt.want)
			}
		})
	}
}

func TestValidateTeamName(t *testing.T) {
	t.Run("valid simple", func(t *testing.T) {
		got, err := ValidateTeamName("Engineering")
		if err != nil {
			t.Fatalf("ValidateTeamName unexpected err: %v", err)
		}
		if got != "Engineering" {
			t.Fatalf("ValidateTeamName = %q, want %q", got, "Engineering")
		}
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		got, err := ValidateTeamName("  Core Team \t")
		if err != nil {
			t.Fatalf("ValidateTeamName unexpected err: %v", err)
		}
		if got != "Core Team" {
			t.Fatalf("ValidateTeamName = %q, want %q", got, "Core Team")
		}
	})

	t.Run("accepts 120 chars", func(t *testing.T) {
		name := strings.Repeat("a", 120)
		got, err := ValidateTeamName(name)
		if err != nil {
			t.Fatalf("ValidateTeamName(120-char) unexpected err: %v", err)
		}
		if got != name {
			t.Fatalf("ValidateTeamName(120-char) = %q, want input back", got)
		}
	})

	invalid := []struct {
		label string
		name  string
	}{
		{"empty", ""},
		{"whitespace only", "   \t\n"},
		{"121 chars", strings.Repeat("a", 121)},
	}
	for _, tt := range invalid {
		tt := tt
		t.Run("invalid/"+tt.label, func(t *testing.T) {
			got, err := ValidateTeamName(tt.name)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("ValidateTeamName(%q) err = %v, want ErrValidation", tt.name, err)
			}
			if got != "" {
				t.Fatalf("ValidateTeamName(%q) = %q, want empty on error", tt.name, got)
			}
		})
	}
}

func TestTeamInvitationUsable(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	tests := []struct {
		name   string
		status InvitationStatus
		exp    time.Time
		want   bool
	}{
		{"pending future expiry is usable", InvitePending, future, true},
		{"pending past expiry is not usable", InvitePending, past, false},
		{"pending exactly at expiry is not usable (Before is strict)", InvitePending, now, false},
		{"accepted future expiry is not usable", InviteAccepted, future, false},
		{"revoked future expiry is not usable", InviteRevoked, future, false},
		{"expired status is not usable", InviteExpired, future, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			inv := TeamInvitation{Status: tt.status, ExpiresAt: tt.exp}
			if got := inv.Usable(now); got != tt.want {
				t.Fatalf("Usable(%v) with status %q expiry %v = %v, want %v", now, tt.status, tt.exp, got, tt.want)
			}
		})
	}
}

func TestTeamInvitationTokenHashNotSerialized(t *testing.T) {
	inv := TeamInvitation{
		ID:        "invite_1",
		TeamID:    "team_1",
		Email:     "ada@example.com",
		Role:      TeamRoleMember,
		InvitedBy: "user_1",
		Status:    InvitePending,
		TokenHash: "deadbeef",
		ExpiresAt: time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m := jsonKeys(t, b)
	if _, ok := m["tokenHash"]; ok {
		t.Fatalf("TeamInvitation JSON = %s, want no tokenHash key (TokenHash is json:\"-\")", b)
	}
	if strings.Contains(string(b), "deadbeef") {
		t.Fatalf("TeamInvitation JSON leaked token hash: %s", b)
	}
	for _, key := range []string{"id", "teamId", "email", "role", "invitedBy", "status", "expiresAt", "createdAt"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("TeamInvitation JSON = %s, want key %q", b, key)
		}
	}
}

func TestTeamMemberJSONShape(t *testing.T) {
	mem := TeamMember{
		TeamID:            "team_1",
		UserID:            "user_1",
		Role:              TeamRoleAdmin,
		ShareReadStatuses: false,
		JoinedAt:          time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(mem)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m := jsonKeys(t, b)
	for _, key := range []string{"teamId", "userId", "role", "shareReadStatuses", "joinedAt", "name", "email"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("TeamMember JSON = %s, want key %q", b, key)
		}
	}
	// Unenriched identity serializes as empty strings — present, honest,
	// never fabricated.
	if m["name"] != "" || m["email"] != "" {
		t.Fatalf("TeamMember JSON name/email = %v/%v, want empty when unenriched", m["name"], m["email"])
	}
	if m["shareReadStatuses"] != false {
		t.Fatalf("TeamMember JSON shareReadStatuses = %v, want privacy default false serialized", m["shareReadStatuses"])
	}
}
