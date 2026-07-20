package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedCalSub(t *testing.T, st *Store, userID, url string, mut func(*domain.CalendarSubscription)) domain.CalendarSubscription {
	t.Helper()
	sub := domain.CalendarSubscription{
		UserID:    userID,
		URL:       url,
		Name:      "Holidays",
		Color:     domain.DefaultSubscriptionColor,
		IsVisible: true,
	}
	if mut != nil {
		mut(&sub)
	}
	created, err := st.CalendarSubscriptions().Create(context.Background(), sub)
	if err != nil {
		t.Fatalf("seed calendar subscription: %v", err)
	}
	return created
}

func subEvent(uid, title string, start, end time.Time) domain.Event {
	return domain.Event{ProviderEventID: uid, Title: title, Start: start, End: end}
}

func TestCalendarSubscriptionCRUD(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	sub := seedCalSub(t, st, "u1", "https://example.com/holidays.ics", nil)
	if sub.ID == "" {
		t.Fatal("id not generated")
	}
	if sub.CreatedAt.IsZero() {
		t.Fatal("created_at not set")
	}

	got, err := st.CalendarSubscriptions().GetByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.URL != sub.URL || got.Name != "Holidays" || !got.IsVisible || got.UserID != "u1" {
		t.Fatalf("got = %+v", got)
	}

	// Update every mutable field and read it back.
	now := time.Now().UTC().Truncate(time.Second)
	fetchErr := "feed answered status 500"
	got.Name = "Renamed"
	got.Color = "#ff0000"
	got.IsVisible = false
	got.Etag = `"v2"`
	got.LastFetchedAt = &now
	got.LastError = &fetchErr
	if err := st.CalendarSubscriptions().Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	back, err := st.CalendarSubscriptions().GetByID(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if back.Name != "Renamed" || back.Color != "#ff0000" || back.IsVisible || back.Etag != `"v2"` {
		t.Fatalf("after update = %+v", back)
	}
	if back.LastFetchedAt == nil || !back.LastFetchedAt.Equal(now) {
		t.Fatalf("LastFetchedAt = %v, want %v", back.LastFetchedAt, now)
	}
	if back.LastError == nil || *back.LastError != fetchErr {
		t.Fatalf("LastError = %v, want %q", back.LastError, fetchErr)
	}

	if err := st.CalendarSubscriptions().Delete(ctx, sub.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.CalendarSubscriptions().GetByID(ctx, sub.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: %v, want ErrNotFound", err)
	}
	if err := st.CalendarSubscriptions().Delete(ctx, sub.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Delete: %v, want ErrNotFound", err)
	}
}

func TestCalendarSubscriptionUniquePerUserURL(t *testing.T) {
	st, _ := newTestStore(t)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	seedCalSub(t, st, "u1", "https://example.com/holidays.ics", nil)
	_, err := st.CalendarSubscriptions().Create(context.Background(), domain.CalendarSubscription{
		UserID: "u1", URL: "https://example.com/holidays.ics", Name: "Dup", Color: "#000", IsVisible: true,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate create err = %v, want ErrConflict", err)
	}

	// A different user may subscribe to the same feed.
	seedCalSub(t, st, "u2", "https://example.com/holidays.ics", nil)
}

func TestCalendarSubscriptionListByUserAndListDue(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")

	now := time.Now().UTC()
	fresh := now.Add(-10 * time.Minute)
	stale := now.Add(-2 * time.Hour)

	never := seedCalSub(t, st, "u1", "https://example.com/never.ics", nil)
	staleSub := seedCalSub(t, st, "u1", "https://example.com/stale.ics", func(s *domain.CalendarSubscription) {
		s.LastFetchedAt = &stale
	})
	seedCalSub(t, st, "u2", "https://example.com/fresh.ics", func(s *domain.CalendarSubscription) {
		s.LastFetchedAt = &fresh
	})

	mine, err := st.CalendarSubscriptions().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("ListByUser len = %d, want 2 (user scoping)", len(mine))
	}

	due, err := st.CalendarSubscriptions().ListDue(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("ListDue len = %d, want 2 (never-fetched + stale, not fresh)", len(due))
	}
	if due[0].ID != never.ID || due[1].ID != staleSub.ID {
		t.Fatalf("ListDue order = [%s %s], want never-fetched first then stalest", due[0].ID, due[1].ID)
	}
}

func TestSubscriptionReplaceEventsAndRangeQuery(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	sub := seedCalSub(t, st, "u1", "https://example.com/holidays.ics", nil)

	day := func(d int, h int) time.Time { return time.Date(2026, 7, d, h, 0, 0, 0, time.UTC) }
	desc, loc := "desc", "loc"
	first := []domain.Event{
		{ProviderEventID: "a@x", Title: "A", Description: &desc, Location: &loc, Start: day(1, 9), End: day(1, 10)},
		{ProviderEventID: "b@x", Title: "B", Start: day(2, 0), End: day(3, 0), AllDay: true},
		{ProviderEventID: "z@x", Title: "Outside", Start: day(20, 9), End: day(20, 10)},
	}
	if err := st.CalendarSubscriptions().ReplaceEvents(ctx, sub.ID, first); err != nil {
		t.Fatalf("ReplaceEvents: %v", err)
	}

	got, err := st.CalendarSubscriptions().ListEventsInRange(ctx, "u1", day(1, 0), day(4, 0))
	if err != nil {
		t.Fatalf("ListEventsInRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (range excludes day 20)", len(got))
	}
	ev := got[0]
	if ev.Title != "A" || ev.ProviderEventID != "a@x" {
		t.Fatalf("first event = %+v, want A ordered first", ev)
	}
	if ev.SubscriptionID == nil || *ev.SubscriptionID != sub.ID {
		t.Fatalf("SubscriptionID = %v, want %s", ev.SubscriptionID, sub.ID)
	}
	if ev.Status != domain.EventConfirmed {
		t.Fatalf("Status = %q, want confirmed", ev.Status)
	}
	if ev.Description == nil || *ev.Description != "desc" || ev.Location == nil || *ev.Location != "loc" {
		t.Fatalf("description/location not round-tripped: %+v", ev)
	}
	if ev.Attendees == nil || ev.ReminderMinutes == nil {
		t.Fatal("Attendees/ReminderMinutes must be non-nil empty slices for JSON")
	}
	if !got[1].AllDay {
		t.Fatal("second event should be the all-day B")
	}

	// Cross-tenant negative: u2 must see nothing.
	other, err := st.CalendarSubscriptions().ListEventsInRange(ctx, "u2", day(1, 0), day(30, 0))
	if err != nil {
		t.Fatalf("ListEventsInRange u2: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("cross-tenant leak: u2 sees %d events", len(other))
	}

	// Replace swaps the whole set — feeds own truth.
	second := []domain.Event{{ProviderEventID: "c@x", Title: "C", Start: day(1, 12), End: day(1, 13)}}
	if err := st.CalendarSubscriptions().ReplaceEvents(ctx, sub.ID, second); err != nil {
		t.Fatalf("ReplaceEvents swap: %v", err)
	}
	got, err = st.CalendarSubscriptions().ListEventsInRange(ctx, "u1", day(1, 0), day(30, 0))
	if err != nil {
		t.Fatalf("ListEventsInRange after swap: %v", err)
	}
	if len(got) != 1 || got[0].Title != "C" {
		t.Fatalf("after swap = %+v, want only C", got)
	}

	// Hidden subscriptions contribute nothing.
	sub.IsVisible = false
	if err := st.CalendarSubscriptions().Update(ctx, sub); err != nil {
		t.Fatalf("hide subscription: %v", err)
	}
	got, err = st.CalendarSubscriptions().ListEventsInRange(ctx, "u1", day(1, 0), day(30, 0))
	if err != nil {
		t.Fatalf("ListEventsInRange hidden: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("hidden subscription leaked %d events", len(got))
	}
}

func TestSubscriptionCascades(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	sub := seedCalSub(t, st, "u1", "https://example.com/holidays.ics", nil)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := st.CalendarSubscriptions().ReplaceEvents(ctx, sub.ID, []domain.Event{
		subEvent("a@x", "A", start, start.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("ReplaceEvents: %v", err)
	}

	countEvents := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM subscription_events`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if countEvents() != 1 {
		t.Fatalf("events = %d, want 1", countEvents())
	}

	// Deleting the user cascades subscriptions and their events.
	if _, err := db.Exec(`DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if countEvents() != 0 {
		t.Fatalf("events after user delete = %d, want 0 (cascade)", countEvents())
	}
	if _, err := st.CalendarSubscriptions().GetByID(ctx, sub.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("subscription survived user delete: %v", err)
	}
}
