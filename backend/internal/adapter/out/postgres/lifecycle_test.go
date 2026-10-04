package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestUserExportClaim(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	ok, _, err := st.UserExports().Claim(ctx, "u1", now, time.Hour)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v, want ok", ok, err)
	}
	ok, retryAt, err := st.UserExports().Claim(ctx, "u1", now.Add(10*time.Minute), time.Hour)
	if err != nil || ok {
		t.Fatalf("second claim inside the window: ok=%v err=%v, want refused", ok, err)
	}
	if !retryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("retryAt = %v, want %v", retryAt, now.Add(time.Hour))
	}
	var started time.Time
	if err := db.QueryRowContext(ctx, `SELECT started_at FROM user_exports WHERE user_id = 'u1'`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if !started.Equal(now) {
		t.Fatalf("a refused claim must not move started_at: got %v, want %v", started, now)
	}
	ok, _, err = st.UserExports().Claim(ctx, "u1", now.Add(time.Hour), time.Hour)
	if err != nil || !ok {
		t.Fatalf("claim exactly one window later: ok=%v err=%v, want ok", ok, err)
	}
}

func TestUserExportsCascadeFromUsers(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if ok, _, err := st.UserExports().Claim(ctx, "u1", time.Now(), time.Hour); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_exports WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_exports rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestUserPreferencesCascade(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.UserPreferences().Put(ctx, "u1", port.UserPreferences{Theme: "ocean"}); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conname = 'user_preferences_user_id_fkey' AND confdeltype = 'c'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("user_preferences_user_id_fkey ON DELETE CASCADE present = %d err=%v, want 1", fk, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_preferences WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_preferences rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestTeamsCreatedBySetNull(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teams := NewTeamRepo(st)
	team, err := teams.Create(ctx, domain.Team{Name: "Design", CreatedBy: "u1"},
		domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("deleting a team creator must no longer be RESTRICTed: %v", err)
	}
	var createdBy sql.NullString
	var members int
	if err := db.QueryRowContext(ctx, `SELECT created_by FROM teams WHERE id = $1`, team.ID).Scan(&createdBy); err != nil {
		t.Fatalf("team must survive its creator: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("created_by = %q, want NULL", createdBy.String)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE team_id = $1`, team.ID).Scan(&members); err != nil || members != 1 {
		t.Fatalf("surviving members = %d err=%v, want 1 (u2)", members, err)
	}
}
