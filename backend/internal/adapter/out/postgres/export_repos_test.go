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

// Migration 0029: the export keyset pagers (threads per account, events per
// calendar, both ordered by id) have a matching (scope, id) btree, so a page
// is an index range scan instead of a re-sort of the account's full set.
func TestExportPagerIndexes(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()
	want := map[string]string{
		"threads_account_id_id_idx": "(account_id, id)",
		"events_calendar_id_id_idx": "(calendar_id, id)",
	}
	for name, cols := range want {
		var def string
		if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&def); err != nil {
			t.Fatalf("index %s: %v", name, err)
		}
		if !strings.Contains(def, cols) {
			t.Fatalf("%s = %q, want columns %s", name, def, cols)
		}
	}
	// The thread pager's plan uses the new index (sequential scans off so
	// the tiny test table cannot hide a missing index).
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, `EXPLAIN SELECT id FROM threads WHERE account_id = 'a' AND id > '' ORDER BY id LIMIT 200`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	_ = rows.Close()
	if !strings.Contains(plan.String(), "threads_account_id_id_idx") || strings.Contains(plan.String(), "Sort") {
		t.Fatalf("thread pager plan does not use (account_id, id) without a sort:\n%s", plan.String())
	}
}

// UserSettingsRepo.Save writes the document and the switch in one statement;
// a nil switch inserts the default (true) and keeps an existing value.
func TestUserSettingsSaveWritesSwitchWithDocument(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	off, on := false, true
	repo := st.UserSettings()
	if err := repo.Save(ctx, domain.UserSettings{UserID: "u1", TimeZone: "Europe/Lisbon", WorkingHours: []domain.AvailabilityWindow{}}, &off); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "u1")
	if err != nil || got.TimeZone != "Europe/Lisbon" || got.AIBackground {
		t.Fatalf("after Save(off) = %+v err=%v, want Lisbon + off", got, err)
	}
	if err := repo.Save(ctx, domain.UserSettings{UserID: "u1", TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, "u1"); got.TimeZone != "UTC" || got.AIBackground {
		t.Fatalf("Save(nil) = %+v, want UTC and the switch kept off", got)
	}
	if err := repo.Save(ctx, domain.UserSettings{UserID: "u1", TimeZone: "Asia/Tokyo", WorkingHours: []domain.AvailabilityWindow{}}, &on); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, "u1"); got.TimeZone != "Asia/Tokyo" || !got.AIBackground {
		t.Fatalf("Save(on) = %+v, want Tokyo + on", got)
	}
	seedUser(t, st, "u2")
	if err := repo.Save(ctx, domain.UserSettings{UserID: "u2", TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, "u2"); !got.AIBackground {
		t.Fatal("a new row saved with a nil switch must default to on")
	}
}
