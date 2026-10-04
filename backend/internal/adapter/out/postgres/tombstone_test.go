package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
)

// A JWT for a purged user cannot resurrect the users row: the purge writes
// the tombstone in its transaction and the requireAuth upsert (UserService
// .EnsureUser → Users.Upsert) is refused with ErrUserDeleted (→ 401).
func TestPurgedUserCannotBeResurrected(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	lifecycle := service.NewUserLifecycleService(service.UserLifecycleDeps{
		Users: st.Users(), Teams: NewTeamRepo(st), Delegations: NewDelegationRepo(st),
		Subscriptions: st.Subscriptions(), Tx: st, Clock: service.SystemClock{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if _, err := lifecycle.Purge(ctx, "u1"); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	var tomb int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM deleted_users WHERE id = 'u1'`).Scan(&tomb); err != nil || tomb != 1 {
		t.Fatalf("deleted_users rows for u1 = %d err=%v, want 1", tomb, err)
	}

	users := service.NewUserService(st.Users(), st.UserPreferences(), service.SystemClock{})
	_, err := users.EnsureUser(ctx, port.Identity{Subject: "u1", Email: "u1@example.com", Name: "Back"})
	if !errors.Is(err, domain.ErrUserDeleted) || !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("EnsureUser(purged) = %v, want ErrUserDeleted", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("users rows for u1 = %d err=%v, want 0 (not resurrected)", n, err)
	}
	// Existing users keep upserting.
	if _, err := users.EnsureUser(ctx, port.Identity{Subject: "u2", Email: "u2-new@example.com"}); err != nil {
		t.Fatalf("EnsureUser(u2) = %v", err)
	}
	if u, err := st.Users().GetByID(ctx, "u2"); err != nil || u.Email != "u2-new@example.com" {
		t.Fatalf("u2 = %+v err=%v, want the refreshed email", u, err)
	}
	// Re-running the purge (idempotent retry) is still a no-op success.
	if _, err := lifecycle.Purge(ctx, "u1"); err != nil {
		t.Fatalf("second Purge: %v", err)
	}
}

// The tombstone shares the purge transaction: a rolled-back purge leaves
// neither the tombstone nor a missing users row.
func TestTombstoneRollsBackWithPurgeTx(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	boom := errors.New("boom")
	err := st.RunInTx(ctx, func(ctx context.Context) error {
		if err := st.Users().Tombstone(ctx, "u1"); err != nil {
			return err
		}
		if err := st.Users().Delete(ctx, "u1"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("RunInTx = %v, want boom", err)
	}
	var tomb, users int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM deleted_users), (SELECT count(*) FROM users WHERE id = 'u1')`).Scan(&tomb, &users); err != nil {
		t.Fatal(err)
	}
	if tomb != 0 || users != 1 {
		t.Fatalf("after rollback: tombstones=%d users=%d, want 0 and 1", tomb, users)
	}
	// Tombstone is idempotent and not FK'd to users.
	if err := st.Users().Tombstone(ctx, "never-existed"); err != nil {
		t.Fatalf("Tombstone(unknown) = %v", err)
	}
	if err := st.Users().Tombstone(ctx, "never-existed"); err != nil {
		t.Fatalf("Tombstone twice = %v", err)
	}
}

// TeamRepo.LockMembershipsForUpdate holds the user's team rows and their
// member rows: a concurrent invitation acceptance (member insert, FK key
// share on teams) and a co-owner leaving (member delete) both block until
// the purge transaction ends.
func TestLockMembershipsForUpdateBlocksConcurrentMembershipChanges(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	seedUser(t, st, "u3")
	teams := NewTeamRepo(st)
	team, err := teams.Create(ctx, domain.Team{Name: "T", CreatedBy: "u1"}, domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- st.RunInTx(ctx, func(ctx context.Context) error {
			if err := teams.LockMembershipsForUpdate(ctx, "u1"); err != nil {
				close(locked)
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	try := func(stmt string, args ...any) error {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(ctx, `SET lock_timeout = '300ms'`); err != nil {
			t.Fatal(err)
		}
		_, err = conn.ExecContext(ctx, stmt, args...)
		_, _ = conn.ExecContext(ctx, `RESET lock_timeout`)
		return err
	}
	if err := try(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, 'u3', 'member')`, team.ID); err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Errorf("member insert during the lock = %v, want a lock timeout", err)
	}
	if err := try(`DELETE FROM team_members WHERE team_id = $1 AND user_id = 'u2'`, team.ID); err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Errorf("co-owner leave during the lock = %v, want a lock timeout", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("locking tx: %v", err)
	}
	if err := try(`DELETE FROM team_members WHERE team_id = $1 AND user_id = 'u2'`, team.ID); err != nil {
		t.Fatalf("after the lock is released the leave succeeds: %v", err)
	}
}

// C-1: a requireAuth upsert that arrives while the purge transaction is
// open (tombstone written, users row deleted, not yet committed) must not
// re-create the row after the purge commits. The users BEFORE INSERT
// trigger serialises on the same per-id lock the tombstone insert takes,
// then sees the committed tombstone and raises; Upsert maps that to
// ErrUserDeleted (→ 401).
func TestConcurrentUpsertDuringPurgeIsRefused(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	purged := make(chan struct{})
	release := make(chan struct{})
	purgeDone := make(chan error, 1)
	go func() {
		purgeDone <- st.RunInTx(ctx, func(ctx context.Context) error {
			if err := st.Users().Tombstone(ctx, "u1"); err != nil {
				close(purged)
				return err
			}
			if err := st.Users().Delete(ctx, "u1"); err != nil {
				close(purged)
				return err
			}
			close(purged)
			<-release
			return nil
		})
	}()
	<-purged

	users := service.NewUserService(st.Users(), st.UserPreferences(), service.SystemClock{})
	upsertDone := make(chan error, 1)
	go func() {
		_, err := users.EnsureUser(ctx, port.Identity{Subject: "u1", Email: "u1@example.com", Name: "Back"})
		upsertDone <- err
	}()

	// Wait until the upsert is parked on a lock held by the purge tx.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%INSERT INTO users%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-upsertDone:
			close(release)
			<-purgeDone
			t.Fatalf("upsert finished before the purge committed (err=%v); it must block", err)
		default:
		}
		if time.Now().After(deadline) {
			close(release)
			<-purgeDone
			t.Fatal("upsert never blocked on the purge transaction")
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(release)
	if err := <-purgeDone; err != nil {
		t.Fatalf("purge tx: %v", err)
	}
	err := <-upsertDone
	if !errors.Is(err, domain.ErrUserDeleted) || !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("concurrent EnsureUser = %v, want ErrUserDeleted (401)", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("users rows for u1 = %d err=%v, want 0 (not re-created)", n, err)
	}
}

// A purge for an id that never got (or no longer has) a users row still
// writes the tombstone, so a JWT minted before the Better Auth delete can
// not provision a fresh row afterwards.
func TestPurgeWithoutUsersRowStillTombstones(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	lifecycle := service.NewUserLifecycleService(service.UserLifecycleDeps{
		Users: st.Users(), Teams: NewTeamRepo(st), Delegations: NewDelegationRepo(st),
		Subscriptions: st.Subscriptions(), Tx: st, Clock: service.SystemClock{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if _, err := lifecycle.Purge(ctx, "ghost"); err != nil {
		t.Fatalf("Purge(ghost): %v", err)
	}
	var tomb int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM deleted_users WHERE id = 'ghost'`).Scan(&tomb); err != nil || tomb != 1 {
		t.Fatalf("deleted_users rows for ghost = %d err=%v, want 1", tomb, err)
	}
	users := service.NewUserService(st.Users(), st.UserPreferences(), service.SystemClock{})
	if _, err := users.EnsureUser(ctx, port.Identity{Subject: "ghost", Email: "g@example.com"}); !errors.Is(err, domain.ErrUserDeleted) {
		t.Fatalf("EnsureUser(ghost) = %v, want ErrUserDeleted", err)
	}
	// A direct insert (any path, not just Upsert) is refused by the trigger.
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email) VALUES ('ghost', 'g@example.com')`); err == nil {
		t.Fatal("raw INSERT of a tombstoned id succeeded; the trigger must refuse it")
	}
}
