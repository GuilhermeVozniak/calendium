package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// Export reads added for completeness: each is scoped strictly to the user.

func TestCommentRepoListByAuthor(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	u2 := seedUser(t, st, "u2")
	a := seedAccount(t, st, u.ID)
	th := seedThread(t, st, a.ID, time.Now())
	team := seedTeam(t, st, u.ID, "A")
	base := time.Now().UTC().Truncate(time.Microsecond)
	older := seedComment(t, st, th.ID, team.ID, u.ID, "mine-1", nil, base.Add(-time.Hour))
	newer := seedComment(t, st, th.ID, team.ID, u.ID, "mine-2", nil, base)
	seedComment(t, st, th.ID, team.ID, u2.ID, "theirs", nil, base)
	gone := seedComment(t, st, th.ID, team.ID, u.ID, "deleted", nil, base)
	if err := NewCommentRepo(st).SoftDelete(ctx, gone.ID, base); err != nil {
		t.Fatal(err)
	}

	got, err := NewCommentRepo(st).ListByAuthor(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != older.ID || got[1].ID != newer.ID {
		t.Fatalf("ListByAuthor = %+v, want [mine-1 mine-2] (live, own, oldest first)", got)
	}
	none, err := NewCommentRepo(st).ListByAuthor(ctx, "nobody")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("empty = %#v, %v; want empty non-nil", none, err)
	}
}

func TestSnippetRepoListTeamByAuthor(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	team := seedTeam(t, st, "u1", "A")
	for _, s := range []domain.Snippet{
		{UserID: "u1", Name: "personal", BodyHTML: "x"},
		{UserID: "u1", TeamID: &team.ID, Name: "team-mine", BodyHTML: "x"},
		{UserID: "u2", TeamID: &team.ID, Name: "team-theirs", BodyHTML: "x"},
	} {
		if _, err := st.Snippets().Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Snippets().ListTeamByAuthor(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "team-mine" {
		t.Fatalf("ListTeamByAuthor = %+v, want [team-mine]", got)
	}
}

func TestBookingRepoListByUserPage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedBookingLink(t, st, "u1", "mine")
	theirs := seedBookingLink(t, st, "u2", "theirs")
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		start := base.Add(time.Duration(i) * time.Hour)
		b, err := st.Bookings().CreateHold(ctx, domain.Booking{LinkID: mine.ID, Start: start, End: start.Add(30 * time.Minute), InviteeName: "G", InviteeEmail: "g@example.com", InviteeTZ: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		want[b.ID] = true
	}
	if _, err := st.Bookings().CreateHold(ctx, domain.Booking{LinkID: theirs.ID, Start: base, End: base.Add(30 * time.Minute), InviteeName: "X", InviteeEmail: "x@example.com", InviteeTZ: "UTC"}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for after := ""; ; {
		page, err := st.Bookings().ListByUserPage(ctx, "u1", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range page {
			seen = append(seen, b.ID)
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d bookings (%v), want 5", len(seen), seen)
	}
	for i, id := range seen {
		if !want[id] {
			t.Fatalf("foreign booking %s in u1's pages", id)
		}
		if i > 0 && strings.Compare(seen[i-1], id) >= 0 {
			t.Fatalf("pages not in strict id order: %v", seen)
		}
	}
}
