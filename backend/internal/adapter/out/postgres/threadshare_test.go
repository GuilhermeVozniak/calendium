package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestThreadShareRepoCreateAndTokenLookup(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th := seedThread(t, st, a.ID, time.Now())

	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	sh, err := repo.Create(ctx, domain.ThreadShare{
		ThreadID:  th.ID,
		CreatedBy: "u1",
		Audience:  domain.ShareAudienceExternal,
		TokenHash: "hash-1",
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sh.ID == "" || sh.CreatedAt.IsZero() {
		t.Fatalf("Create returned incomplete share: %+v", sh)
	}

	got, err := repo.GetByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.ID != sh.ID || got.ThreadID != th.ID || got.CreatedBy != "u1" ||
		got.Audience != domain.ShareAudienceExternal || got.TeamID != nil ||
		got.RevokedAt != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("GetByTokenHash = %+v", got)
	}

	if _, err := repo.GetByTokenHash(ctx, "no-such-hash"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown hash err = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByID(ctx, "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id err = %v, want ErrNotFound", err)
	}
}

func TestThreadShareRepoDuplicateTokenHashConflicts(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th := seedThread(t, st, a.ID, time.Now())

	mk := domain.ThreadShare{ThreadID: th.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "dupe"}
	if _, err := repo.Create(ctx, mk); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := repo.Create(ctx, mk); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate token_hash err = %v, want ErrConflict", err)
	}
}

func TestThreadShareRepoListByThreadScoped(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th1 := seedThread(t, st, a.ID, time.Now())
	th2 := seedThread(t, st, a.ID, time.Now())

	sh1, err := repo.Create(ctx, domain.ThreadShare{ThreadID: th1.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	sh2, err := repo.Create(ctx, domain.ThreadShare{ThreadID: th1.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "h2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, domain.ThreadShare{ThreadID: th2.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "h3"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListByThread(ctx, th1.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListByThread = %d shares, %v; want 2", len(got), err)
	}
	if got[0].ID != sh1.ID || got[1].ID != sh2.ID {
		t.Fatalf("ListByThread order = [%s %s], want [%s %s]", got[0].ID, got[1].ID, sh1.ID, sh2.ID)
	}

	// A thread with no shares yields an empty (non-nil) slice.
	th3 := seedThread(t, st, a.ID, time.Now())
	got, err = repo.ListByThread(ctx, th3.ID)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ListByThread(empty) = %+v, %v; want empty slice", got, err)
	}
}

func TestThreadShareRepoRevoke(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th := seedThread(t, st, a.ID, time.Now())

	sh, err := repo.Create(ctx, domain.ThreadShare{ThreadID: th.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := repo.Revoke(ctx, sh.ID, at); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err := repo.GetByID(ctx, sh.ID)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(at) {
		t.Fatalf("after revoke = %+v, %v; want RevokedAt %v", got, err, at)
	}

	// Re-revoking keeps the original timestamp (idempotent).
	if err := repo.Revoke(ctx, sh.ID, at.Add(time.Hour)); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	got, _ = repo.GetByID(ctx, sh.ID)
	if !got.RevokedAt.Equal(at) {
		t.Fatalf("RevokedAt moved on re-revoke: %v", got.RevokedAt)
	}

	if err := repo.Revoke(ctx, "ghost", at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Revoke(ghost) = %v, want ErrNotFound", err)
	}
}

func TestThreadShareRepoCascadeOnThreadDelete(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th := seedThread(t, st, a.ID, time.Now())

	sh, err := repo.Create(ctx, domain.ThreadShare{ThreadID: th.ID, CreatedBy: "u1", Audience: domain.ShareAudienceExternal, TokenHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM threads WHERE id = $1`, th.ID); err != nil {
		t.Fatalf("delete thread: %v", err)
	}
	if _, err := repo.GetByID(ctx, sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("share survived thread delete: %v, want ErrNotFound", err)
	}
}

func TestThreadShareRepoTeamShareCascadesOnTeamDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	repo := NewThreadShareRepo(st)
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, "u1", "Acme")

	sh, err := repo.Create(ctx, domain.ThreadShare{
		ThreadID: th.ID, CreatedBy: "u1",
		Audience: domain.ShareAudienceTeam, TeamID: &team.ID, TokenHash: "h1",
	})
	if err != nil {
		t.Fatalf("Create(team share): %v", err)
	}
	got, err := repo.GetByID(ctx, sh.ID)
	if err != nil || got.TeamID == nil || *got.TeamID != team.ID {
		t.Fatalf("team share = %+v, %v", got, err)
	}

	if err := NewTeamRepo(st).Delete(ctx, team.ID); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	if _, err := repo.GetByID(ctx, sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("team share survived team delete: %v, want ErrNotFound", err)
	}
}
