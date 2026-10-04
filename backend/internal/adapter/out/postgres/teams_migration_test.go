package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These tests prove migration 0011_teams.sql applies (TestMain runs the full
// embedded migration set against a real postgres container) and pin its key
// constraints. Repo implementations arrive in a later task; raw SQL here.

func TestTeamsMigrationSchema(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()

	for _, tbl := range []string{"teams", "team_members", "team_invitations"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, tbl,
		).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: count=%d err=%v", tbl, n, err)
		}
	}
}

func TestTeamsMigrationConstraints(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}

	// Seed the FK chain: users -> teams -> members/invitations.
	if err := exec(`INSERT INTO users (id, email) VALUES ('u1', 'u1@example.com'), ('u2', 'u2@example.com')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := exec(`INSERT INTO teams (id, name, created_by) VALUES ('t1', 'Acme', 'u1')`); err != nil {
		t.Fatalf("insert team: %v", err)
	}

	// Defaults: role 'member', share_read_statuses false (privacy default).
	if err := exec(`INSERT INTO team_members (team_id, user_id) VALUES ('t1', 'u2')`); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	var role string
	var share bool
	if err := db.QueryRowContext(ctx,
		`SELECT role, share_read_statuses FROM team_members WHERE team_id = 't1' AND user_id = 'u2'`,
	).Scan(&role, &share); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if role != "member" || share {
		t.Fatalf("member defaults: role=%q share=%v, want member/false", role, share)
	}

	// (team_id, user_id) is the primary key: duplicate membership rejected.
	if err := exec(`INSERT INTO team_members (team_id, user_id) VALUES ('t1', 'u2')`); err == nil {
		t.Fatal("duplicate membership accepted, want PK violation")
	}

	// One live invitation per lower(email) per team, pending only.
	exp := time.Now().Add(48 * time.Hour)
	if err := exec(`INSERT INTO team_invitations (id, team_id, email, invited_by, token_hash, expires_at)
		VALUES ('i1', 't1', 'New@Example.com', 'u1', 'hash-1', $1)`, exp); err != nil {
		t.Fatalf("insert invitation: %v", err)
	}
	err := exec(`INSERT INTO team_invitations (id, team_id, email, invited_by, token_hash, expires_at)
		VALUES ('i2', 't1', 'new@example.COM', 'u1', 'hash-2', $1)`, exp)
	if err == nil {
		t.Fatal("second pending invitation for same email accepted, want unique violation")
	}
	if !strings.Contains(err.Error(), "team_invitations_pending_idx") {
		t.Fatalf("expected team_invitations_pending_idx violation, got: %v", err)
	}
	// A non-pending row for the same address is allowed (index is partial).
	if err := exec(`INSERT INTO team_invitations (id, team_id, email, invited_by, status, token_hash, expires_at)
		VALUES ('i3', 't1', 'new@example.com', 'u1', 'revoked', 'hash-3', $1)`, exp); err != nil {
		t.Fatalf("revoked duplicate rejected, want allowed: %v", err)
	}

	// token_hash is globally unique (one-time invite links never collide).
	if err := exec(`INSERT INTO team_invitations (id, team_id, email, invited_by, status, token_hash, expires_at)
		VALUES ('i4', 't1', 'other@example.com', 'u1', 'revoked', 'hash-3', $1)`, exp); err == nil {
		t.Fatal("duplicate token_hash accepted, want unique violation")
	}

	// Deleting the team cascades members + invitations.
	if err := exec(`DELETE FROM teams WHERE id = 't1'`); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM team_members) + (SELECT count(*) FROM team_invitations)`,
	).Scan(&left); err != nil || left != 0 {
		t.Fatalf("cascade: %d rows left, err=%v", left, err)
	}

	// created_by was ON DELETE RESTRICT in 0011; migration 0029 (account
	// deletion) relaxed it to SET NULL: the team outlives its creator.
	if err := exec(`INSERT INTO teams (id, name, created_by) VALUES ('t2', 'Survivor', 'u1')`); err != nil {
		t.Fatalf("insert team t2: %v", err)
	}
	if err := exec(`DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("deleting team creator: %v, want SET NULL (0029)", err)
	}
	var creatorNull bool
	if err := db.QueryRowContext(ctx, `SELECT created_by IS NULL FROM teams WHERE id = 't2'`).Scan(&creatorNull); err != nil || !creatorNull {
		t.Fatalf("t2 created_by IS NULL = %v err=%v, want true", creatorNull, err)
	}
}
