package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// seedTeam creates a team owned by ownerID (user must already exist).
func seedTeam(t *testing.T, st *Store, ownerID, name string) domain.Team {
	t.Helper()
	team, err := NewTeamRepo(st).Create(context.Background(),
		domain.Team{Name: name, CreatedBy: ownerID},
		domain.TeamMember{UserID: ownerID, Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return team
}

func TestTeamRepoCreate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")

	team, err := repo.Create(ctx,
		domain.Team{Name: "Acme", CreatedBy: "u1"},
		domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if team.ID == "" || team.CreatedAt.IsZero() {
		t.Fatalf("Create returned incomplete team: %+v", team)
	}

	got, err := repo.GetByID(ctx, team.ID)
	if err != nil || got.Name != "Acme" || got.CreatedBy != "u1" {
		t.Fatalf("GetByID = %+v, %v", got, err)
	}

	// The owner membership was inserted in the same transaction.
	m, err := repo.GetMember(ctx, team.ID, "u1")
	if err != nil {
		t.Fatalf("GetMember(owner): %v", err)
	}
	if m.Role != domain.TeamRoleOwner || m.ShareReadStatuses || m.JoinedAt.IsZero() {
		t.Fatalf("owner membership = %+v, want role owner, share false", m)
	}
}

func TestTeamRepoCreateAtomicRollback(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")

	// Owner membership insert fails (user "ghost" violates the FK); the
	// team insert from the same transaction must roll back with it.
	_, err := repo.Create(ctx,
		domain.Team{ID: "team-atomic", Name: "Doomed", CreatedBy: "u1"},
		domain.TeamMember{UserID: "ghost", Role: domain.TeamRoleOwner})
	if err == nil {
		t.Fatal("Create with unknown owner succeeded, want FK error")
	}
	if _, err := repo.GetByID(ctx, "team-atomic"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("team row survived rolled-back create: err=%v, want ErrNotFound", err)
	}
}

func TestTeamRepoListByUserScoped(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	seedUser(t, st, "u3")
	teamA := seedTeam(t, st, "u1", "Team A")
	teamB := seedTeam(t, st, "u2", "Team B")

	got, err := repo.ListByUser(ctx, "u1")
	if err != nil || len(got) != 1 || got[0].ID != teamA.ID {
		t.Fatalf("ListByUser(u1) = %+v, %v; want only %s", got, err, teamA.ID)
	}

	// Cross-tenant: a member of another team sees only their own team.
	got, err = repo.ListByUser(ctx, "u2")
	if err != nil || len(got) != 1 || got[0].ID != teamB.ID {
		t.Fatalf("ListByUser(u2) = %+v, %v; want only %s", got, err, teamB.ID)
	}

	// A user in no team gets an empty (non-nil) slice.
	got, err = repo.ListByUser(ctx, "u3")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ListByUser(u3) = %+v, %v; want empty slice", got, err)
	}
}

func TestTeamRepoGetMemberCrossTenant(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	seedTeam(t, st, "u2", "Team B")

	// u2 belongs to Team B, not Team A: the authz primitive must miss.
	if _, err := repo.GetMember(ctx, teamA.ID, "u2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMember(cross-tenant) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetMember(ctx, teamA.ID, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMember(unknown user) = %v, want ErrNotFound", err)
	}
}

func TestTeamRepoUpsertMember(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	team := seedTeam(t, st, "u1", "Team A")

	join := domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleMember}
	if err := repo.UpsertMember(ctx, join); err != nil {
		t.Fatalf("UpsertMember(insert): %v", err)
	}
	m, err := repo.GetMember(ctx, team.ID, "u2")
	if err != nil || m.Role != domain.TeamRoleMember || m.ShareReadStatuses {
		t.Fatalf("after insert: %+v, %v; want member/share=false", m, err)
	}
	joined := m.JoinedAt

	// Update role + opt-in via the same call; joined_at is preserved.
	promo := domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleAdmin, ShareReadStatuses: true}
	if err := repo.UpsertMember(ctx, promo); err != nil {
		t.Fatalf("UpsertMember(update): %v", err)
	}
	// Idempotent: repeating the exact same upsert is a no-op, not an error.
	if err := repo.UpsertMember(ctx, promo); err != nil {
		t.Fatalf("UpsertMember(repeat): %v", err)
	}
	m, err = repo.GetMember(ctx, team.ID, "u2")
	if err != nil || m.Role != domain.TeamRoleAdmin || !m.ShareReadStatuses {
		t.Fatalf("after update: %+v, %v; want admin/share=true", m, err)
	}
	if !m.JoinedAt.Equal(joined) {
		t.Fatalf("JoinedAt changed on upsert: %v -> %v", joined, m.JoinedAt)
	}

	members, err := repo.ListMembers(ctx, team.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListMembers = %d members, %v; want 2", len(members), err)
	}
}

func TestTeamRepoListMembersScoped(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	seedTeam(t, st, "u2", "Team B")

	// Cross-tenant: Team A's roster never includes Team B's members.
	members, err := repo.ListMembers(ctx, teamA.ID)
	if err != nil || len(members) != 1 || members[0].UserID != "u1" {
		t.Fatalf("ListMembers(teamA) = %+v, %v; want only u1", members, err)
	}
}

func TestTeamRepoListMembersIdentityEnrichment(t *testing.T) {
	// F2: ListMembers joins users for display identity. GetMember (the
	// authz primitive) stays unenriched — enrichment exists only on the
	// roster read that services gate behind the caller's own membership.
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1") // email only, no display name
	name := "Uma Member"
	if _, err := st.Users().Upsert(ctx, domain.User{ID: "u2", Email: "u2@example.com", Name: &name}); err != nil {
		t.Fatalf("seed named user: %v", err)
	}
	team := seedTeam(t, st, "u1", "Team A")
	if err := repo.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	members, err := repo.ListMembers(ctx, team.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListMembers = %d, %v; want 2", len(members), err)
	}
	byUser := map[string]domain.TeamMember{}
	for _, m := range members {
		byUser[m.UserID] = m
	}
	if got := byUser["u1"]; got.Name != "" || got.Email != "u1@example.com" {
		t.Fatalf("u1 identity = %q/%q, want \"\"/u1@example.com (NULL name stays empty)", got.Name, got.Email)
	}
	if got := byUser["u2"]; got.Name != "Uma Member" || got.Email != "u2@example.com" {
		t.Fatalf("u2 identity = %q/%q, want Uma Member/u2@example.com", got.Name, got.Email)
	}

	m, err := repo.GetMember(ctx, team.ID, "u2")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if m.Name != "" || m.Email != "" {
		t.Fatalf("GetMember enriched (%q/%q), want the authz primitive to stay identity-free", m.Name, m.Email)
	}
}

func TestTeamRepoRemoveMember(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	teamB := seedTeam(t, st, "u2", "Team B")

	if err := repo.UpsertMember(ctx, domain.TeamMember{TeamID: teamA.ID, UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if err := repo.RemoveMember(ctx, teamA.ID, "u2"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := repo.GetMember(ctx, teamA.ID, "u2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMember after remove = %v, want ErrNotFound", err)
	}
	// Removing a non-member is ErrNotFound, and a cross-tenant remove never
	// touches the other team's rows.
	if err := repo.RemoveMember(ctx, teamB.ID, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RemoveMember(cross-tenant) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetMember(ctx, teamB.ID, "u2"); err != nil {
		t.Fatalf("teamB owner membership disturbed: %v", err)
	}
}

func TestTeamRepoCountByRole(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	teamB := seedTeam(t, st, "u2", "Team B")

	count := func(teamID string, role domain.TeamRole) int {
		t.Helper()
		n, err := repo.CountByRole(ctx, teamID, role)
		if err != nil {
			t.Fatalf("CountByRole(%s, %s): %v", teamID, role, err)
		}
		return n
	}

	if n := count(teamA.ID, domain.TeamRoleOwner); n != 1 {
		t.Fatalf("owners = %d, want 1", n)
	}

	// Promotion: u2 joins Team A as a second owner.
	if err := repo.UpsertMember(ctx, domain.TeamMember{TeamID: teamA.ID, UserID: "u2", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatalf("UpsertMember(promote): %v", err)
	}
	if n := count(teamA.ID, domain.TeamRoleOwner); n != 2 {
		t.Fatalf("owners after promotion = %d, want 2", n)
	}

	// Demotion back to member.
	if err := repo.UpsertMember(ctx, domain.TeamMember{TeamID: teamA.ID, UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("UpsertMember(demote): %v", err)
	}
	if n := count(teamA.ID, domain.TeamRoleOwner); n != 1 {
		t.Fatalf("owners after demotion = %d, want 1", n)
	}
	if n := count(teamA.ID, domain.TeamRoleMember); n != 1 {
		t.Fatalf("members after demotion = %d, want 1", n)
	}

	// Cross-tenant: Team B's owner count is untouched by Team A churn, and
	// u2's Team-A role never bleeds into Team B counts.
	if n := count(teamB.ID, domain.TeamRoleOwner); n != 1 {
		t.Fatalf("teamB owners = %d, want 1", n)
	}
	if n := count(teamB.ID, domain.TeamRoleMember); n != 0 {
		t.Fatalf("teamB members = %d, want 0", n)
	}
}

func TestTeamRepoUpdateAndDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	seedUser(t, st, "u1")
	team := seedTeam(t, st, "u1", "Team A")

	team.Name = "Renamed"
	if err := repo.Update(ctx, team); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByID(ctx, team.ID)
	if err != nil || got.Name != "Renamed" {
		t.Fatalf("after update: %+v, %v", got, err)
	}

	if err := repo.Update(ctx, domain.Team{ID: "missing", Name: "X"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, team.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, team.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, team.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete(again) = %v, want ErrNotFound", err)
	}
}

func TestTeamRepoDeleteCascades(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamRepo(st)
	invites := NewTeamInvitationRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	team := seedTeam(t, st, "u1", "Team A")

	if err := repo.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if _, err := invites.Create(ctx, domain.TeamInvitation{
		TeamID: team.ID, Email: "new@example.com", Role: domain.TeamRoleMember,
		InvitedBy: "u1", TokenHash: "cascade-hash", ExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("Create invitation: %v", err)
	}

	if err := repo.Delete(ctx, team.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM team_members WHERE team_id = $1)
		     + (SELECT count(*) FROM team_invitations WHERE team_id = $1)`, team.ID,
	).Scan(&left); err != nil || left != 0 {
		t.Fatalf("cascade left %d rows, err=%v; want 0", left, err)
	}
}

func TestTeamInvitationRepoRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamInvitationRepo(st)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	teamB := seedTeam(t, st, "u2", "Team B")

	expires := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Microsecond)
	inv, err := repo.Create(ctx, domain.TeamInvitation{
		TeamID: teamA.ID, Email: "New@Example.com", Role: domain.TeamRoleAdmin,
		InvitedBy: "u1", TokenHash: "hash-1", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if inv.ID == "" || inv.Status != domain.InvitePending || inv.CreatedAt.IsZero() {
		t.Fatalf("Create returned incomplete invitation: %+v", inv)
	}

	got, err := repo.GetByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.ID != inv.ID || got.TeamID != teamA.ID || got.Email != "New@Example.com" ||
		got.Role != domain.TeamRoleAdmin || got.InvitedBy != "u1" ||
		got.Status != domain.InvitePending || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("GetByTokenHash round-trip mismatch: %+v", got)
	}

	if _, err := repo.GetByTokenHash(ctx, "unknown-hash"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByTokenHash(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, inv.ID); err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if _, err := repo.GetByID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID(missing) = %v, want ErrNotFound", err)
	}

	// Cross-tenant: Team B's invitation list never shows Team A's invites.
	if listB, err := repo.ListByTeam(ctx, teamB.ID); err != nil || len(listB) != 0 {
		t.Fatalf("ListByTeam(teamB) = %+v, %v; want empty", listB, err)
	}
	listA, err := repo.ListByTeam(ctx, teamA.ID)
	if err != nil || len(listA) != 1 || listA[0].ID != inv.ID {
		t.Fatalf("ListByTeam(teamA) = %+v, %v; want the one invite", listA, err)
	}

	// Update drives the lifecycle (revoke) and round-trips.
	inv.Status = domain.InviteRevoked
	if err := repo.Update(ctx, inv); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.GetByID(ctx, inv.ID)
	if err != nil || got.Status != domain.InviteRevoked {
		t.Fatalf("after revoke: %+v, %v", got, err)
	}
	if err := repo.Update(ctx, domain.TeamInvitation{ID: "missing", Status: domain.InviteRevoked}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestTeamInvitationRepoPendingUnique(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewTeamInvitationRepo(st)
	seedUser(t, st, "u1")
	team := seedTeam(t, st, "u1", "Team A")
	expires := time.Now().Add(48 * time.Hour)

	first, err := repo.Create(ctx, domain.TeamInvitation{
		TeamID: team.ID, Email: "New@Example.com", InvitedBy: "u1",
		TokenHash: "hash-1", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// team_invitations_pending_idx: one pending invite per lower(email) per
	// team — the repo maps the unique violation to domain.ErrConflict.
	if _, err := repo.Create(ctx, domain.TeamInvitation{
		TeamID: team.ID, Email: "new@example.COM", InvitedBy: "u1",
		TokenHash: "hash-2", ExpiresAt: expires,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create(duplicate pending) = %v, want ErrConflict", err)
	}

	// A duplicate token_hash is also a conflict (global unique).
	if _, err := repo.Create(ctx, domain.TeamInvitation{
		TeamID: team.ID, Email: "other@example.com", InvitedBy: "u1",
		TokenHash: "hash-1", ExpiresAt: expires,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create(duplicate token hash) = %v, want ErrConflict", err)
	}

	// Once the first invite leaves pending, the address can be re-invited
	// (the unique index is partial).
	first.Status = domain.InviteRevoked
	if err := repo.Update(ctx, first); err != nil {
		t.Fatalf("Update(revoke): %v", err)
	}
	if _, err := repo.Create(ctx, domain.TeamInvitation{
		TeamID: team.ID, Email: "new@example.com", InvitedBy: "u1",
		TokenHash: "hash-3", ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("Create after revoke: %v", err)
	}
}
