package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedComment(t *testing.T, st *Store, threadID, teamID, authorID, body string, mentions []string, at time.Time) domain.Comment {
	t.Helper()
	c, err := NewCommentRepo(st).Create(context.Background(), domain.Comment{
		ThreadID: threadID, TeamID: teamID, AuthorID: authorID,
		Body: body, Mentions: mentions,
		CreatedAt: at.UTC().Truncate(time.Microsecond),
		UpdatedAt: at.UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	return c
}

func TestCommentRepoCreateGetRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")

	now := time.Now().UTC().Truncate(time.Microsecond)
	c := seedComment(t, st, th.ID, team.ID, u.ID, "hello @b", []string{"u2", "u3"}, now)
	if c.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, err := NewCommentRepo(st).GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ThreadID != th.ID || got.TeamID != team.ID || got.AuthorID != u.ID || got.Body != "hello @b" {
		t.Fatalf("round trip = %+v", got)
	}
	if len(got.Mentions) != 2 || got.Mentions[0] != "u2" || got.Mentions[1] != "u3" {
		t.Fatalf("mentions = %v, want [u2 u3]", got.Mentions)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = %v/%v, want %v", got.CreatedAt, got.UpdatedAt, now)
	}
	if got.DeletedAt != nil {
		t.Fatalf("DeletedAt = %v, want nil", got.DeletedAt)
	}

	if _, err := NewCommentRepo(st).GetByID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v, want ErrNotFound", err)
	}
}

func TestCommentRepoEmptyMentionsRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")

	c := seedComment(t, st, th.ID, team.ID, u.ID, "plain", nil, time.Now())
	got, err := NewCommentRepo(st).GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Mentions == nil || len(got.Mentions) != 0 {
		t.Fatalf("mentions = %#v, want empty non-nil slice", got.Mentions)
	}
}

func TestCommentRepoBodyCheckConstraint(t *testing.T) {
	st, _ := newTestStore(t)
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")

	_, err := NewCommentRepo(st).Create(context.Background(), domain.Comment{
		ThreadID: th.ID, TeamID: team.ID, AuthorID: u.ID,
		Body: strings.Repeat("x", 10001),
	})
	if err == nil {
		t.Fatal("body over 10000 chars accepted; CHECK constraint missing")
	}
}

func TestCommentRepoListByThreadTeamScopesAndOrders(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	u2 := seedUser(t, st, "u2")
	a := seedAccount(t, st, u.ID)
	th1 := seedThread(t, st, a.ID, time.Now())
	th2 := seedThread(t, st, a.ID, time.Now())
	teamA := seedTeam(t, st, u.ID, "A")
	teamB := seedTeam(t, st, u2.ID, "B")

	base := time.Now().UTC().Truncate(time.Microsecond)
	older := seedComment(t, st, th1.ID, teamA.ID, u.ID, "first", nil, base.Add(-time.Hour))
	newer := seedComment(t, st, th1.ID, teamA.ID, u.ID, "second", nil, base)
	seedComment(t, st, th2.ID, teamA.ID, u.ID, "other thread", nil, base)
	seedComment(t, st, th1.ID, teamB.ID, u2.ID, "other team", nil, base)

	got, err := NewCommentRepo(st).ListByThreadTeam(ctx, th1.ID, teamA.ID)
	if err != nil {
		t.Fatalf("ListByThreadTeam: %v", err)
	}
	if len(got) != 2 || got[0].ID != older.ID || got[1].ID != newer.ID {
		t.Fatalf("list = %+v, want [first second] oldest-first, scoped to (thread, team)", got)
	}

	empty, err := NewCommentRepo(st).ListByThreadTeam(ctx, th2.ID, teamB.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v, %v; want empty non-nil slice", empty, err)
	}
}

func TestCommentRepoUpdate(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")
	c := seedComment(t, st, th.ID, team.ID, u.ID, "v1", nil, time.Now())

	c.Body = "v2 @ada"
	c.Mentions = []string{"u-ada"}
	c.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
	if err := NewCommentRepo(st).Update(ctx, c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := NewCommentRepo(st).GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Body != "v2 @ada" || len(got.Mentions) != 1 || got.Mentions[0] != "u-ada" || !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Fatalf("after update = %+v", got)
	}

	ghost := c
	ghost.ID = "missing"
	if err := NewCommentRepo(st).Update(ctx, ghost); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing err = %v, want ErrNotFound", err)
	}
}

func TestCommentRepoSoftDeleteHidesEverywhere(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")
	c := seedComment(t, st, th.ID, team.ID, u.ID, "bye", nil, time.Now())

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := NewCommentRepo(st).SoftDelete(ctx, c.ID, at); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := NewCommentRepo(st).GetByID(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after soft delete err = %v, want ErrNotFound", err)
	}
	list, err := NewCommentRepo(st).ListByThreadTeam(ctx, th.ID, team.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("list after soft delete = %+v, %v; want empty", list, err)
	}
	// Soft-deleted rows are invisible to writes too.
	if err := NewCommentRepo(st).SoftDelete(ctx, c.ID, at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double soft delete err = %v, want ErrNotFound", err)
	}
	if err := NewCommentRepo(st).Update(ctx, c); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update soft-deleted err = %v, want ErrNotFound", err)
	}
	// The row itself is retained (soft, not hard, delete).
	var n int
	if err := sharedDB.QueryRow(`SELECT count(*) FROM thread_comments WHERE id = $1 AND deleted_at IS NOT NULL`, c.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("raw row count = %d, %v; want 1 soft-deleted row", n, err)
	}
}

func TestCommentRepoThreadCascade(t *testing.T) {
	st, db := newTestStore(t)
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "Acme")
	c := seedComment(t, st, th.ID, team.ID, u.ID, "gone with thread", nil, time.Now())

	if _, err := db.Exec(`DELETE FROM threads WHERE id = $1`, th.ID); err != nil {
		t.Fatalf("delete thread: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM thread_comments WHERE id = $1`, c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("comments after thread delete = %d, %v; want 0 (ON DELETE CASCADE)", n, err)
	}
}
