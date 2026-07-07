package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestCalendarRepoUpsertPreservesVisibilityAndColor(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	const pid = "cal-provider-1"
	first, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID:          acct.ID,
		ProviderCalendarID: pid,
		Name:               "Work",
		Color:              "#111111",
		CanWrite:           true,
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if !first.IsVisible {
		t.Fatal("new calendar must default to visible")
	}

	// User hides it and repaints it locally.
	first.IsVisible = false
	first.Color = "#abcdef"
	if err := st.Calendars().Update(ctx, first); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Provider re-sync upserts the same calendar with a new name and its own color.
	resynced, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID:          acct.ID,
		ProviderCalendarID: pid,
		Name:               "Work (renamed)",
		Color:              "#000000", // provider color must NOT overwrite local pref
		CanWrite:           true,
	})
	if err != nil {
		t.Fatalf("Upsert resync: %v", err)
	}
	if resynced.Name != "Work (renamed)" {
		t.Fatalf("name not updated on resync: %q", resynced.Name)
	}
	if resynced.IsVisible {
		t.Fatal("resync clobbered the local is_visible preference")
	}
	if resynced.Color != "#abcdef" {
		t.Fatalf("resync clobbered local color: got %q, want #abcdef", resynced.Color)
	}
}

func TestCalendarRepoUpsertDefaults(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	c, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID:          acct.ID,
		ProviderCalendarID: newID(),
		Name:               "New Cal",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if c.Color != "#6366f1" {
		t.Fatalf("Color = %q, want #6366f1", c.Color)
	}
	if c.TimeZone != "UTC" {
		t.Fatalf("TimeZone = %q, want UTC", c.TimeZone)
	}
	if !c.IsVisible {
		t.Fatal("IsVisible must default to true")
	}
}

func TestCalendarRepoGetByIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Calendars().GetByID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCalendarRepoListByUserAndAccount(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	secondary, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID: acct.ID, ProviderCalendarID: newID(), Name: "Secondary", IsPrimary: false,
	})
	if err != nil {
		t.Fatalf("Upsert secondary: %v", err)
	}
	primary, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID: acct.ID, ProviderCalendarID: newID(), Name: "Primary", IsPrimary: true,
	})
	if err != nil {
		t.Fatalf("Upsert primary: %v", err)
	}

	byUser, err := st.Calendars().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(byUser) != 2 || byUser[0].ID != primary.ID || byUser[1].ID != secondary.ID {
		t.Fatalf("ListByUser = %+v, want primary first", byUser)
	}

	byAccount, err := st.Calendars().ListByAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("ListByAccount: %v", err)
	}
	if len(byAccount) != 2 || byAccount[0].ID != primary.ID || byAccount[1].ID != secondary.ID {
		t.Fatalf("ListByAccount = %+v, want primary first", byAccount)
	}
}

func TestCalendarRepoUpdateMissing(t *testing.T) {
	st, _ := newTestStore(t)
	err := st.Calendars().Update(context.Background(), domain.Calendar{ID: "nope", Name: "x"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// --- EventRepo ---------------------------------------------------------------

func TestEventRepoUpsertGetDefaults(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	pid := newID()
	name := "Alice"
	created, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID:      cal.ID,
		ProviderEventID: pid,
		Title:           "Standup",
		Start:           start,
		End:             end,
		Attendees:       []domain.Attendee{{Email: "alice@example.com", Name: &name, Response: domain.RsvpAccepted}},
		Conferencing:    &domain.Conferencing{Provider: domain.ConferencingMeet, URL: "https://meet.example/abc"},
		ReminderMinutes: []int{10, 30},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if created.Status != domain.EventConfirmed {
		t.Fatalf("Status = %q, want confirmed default", created.Status)
	}
	if created.Visibility != domain.VisibilityDefault {
		t.Fatalf("Visibility = %q, want default", created.Visibility)
	}

	byID, err := st.Events().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	byProvider, err := st.Events().GetByProviderID(ctx, cal.ID, pid)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	for _, e := range []domain.Event{byID, byProvider} {
		if len(e.Attendees) != 1 || e.Attendees[0].Email != "alice@example.com" {
			t.Fatalf("Attendees = %+v", e.Attendees)
		}
		if e.Conferencing == nil || e.Conferencing.URL != "https://meet.example/abc" {
			t.Fatalf("Conferencing = %+v, want round-tripped pointer", e.Conferencing)
		}
		if len(e.ReminderMinutes) != 2 {
			t.Fatalf("ReminderMinutes = %+v", e.ReminderMinutes)
		}
	}

	// omitted attendees/reminders/conferencing normalize to non-nil-empty/nil.
	bare, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: newID(), Title: "Bare", Start: start, End: end,
	})
	if err != nil {
		t.Fatalf("Upsert bare: %v", err)
	}
	if bare.Attendees == nil || len(bare.Attendees) != 0 {
		t.Fatalf("bare Attendees = %+v, want empty non-nil", bare.Attendees)
	}
	if bare.ReminderMinutes == nil || len(bare.ReminderMinutes) != 0 {
		t.Fatalf("bare ReminderMinutes = %+v, want empty non-nil", bare.ReminderMinutes)
	}
	if bare.Conferencing != nil {
		t.Fatalf("bare Conferencing = %+v, want nil", bare.Conferencing)
	}
}

func TestEventRepoListInRangeOverlap(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	mk := func(title string, start, end time.Time) domain.Event {
		e, err := st.Events().Upsert(ctx, domain.Event{
			CalendarID: cal.ID, ProviderEventID: newID(), Title: title, Start: start, End: end,
		})
		if err != nil {
			t.Fatalf("Upsert %s: %v", title, err)
		}
		return e
	}

	a := mk("A-inside", from.Add(1*time.Hour), from.Add(2*time.Hour))
	b := mk("B-straddle", from.Add(-1*time.Hour), from.Add(1*time.Hour))
	mk("C-before", from.Add(-3*time.Hour), from.Add(-2*time.Hour))
	mk("D-after", to.Add(1*time.Hour), to.Add(2*time.Hour))

	got, err := st.Events().ListInRange(ctx, "u1", from, to, nil)
	if err != nil {
		t.Fatalf("ListInRange: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range got {
		ids[e.ID] = true
	}
	if len(got) != 2 || !ids[a.ID] || !ids[b.ID] {
		t.Fatalf("ListInRange = %+v, want only A and B", got)
	}
}

func TestEventRepoListInRangeVisibilityGating(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	hiddenCal, err := st.Calendars().Upsert(ctx, domain.Calendar{
		AccountID: acct.ID, ProviderCalendarID: newID(), Name: "Hidden",
	})
	if err != nil {
		t.Fatalf("Upsert hiddenCal: %v", err)
	}
	hiddenCal.IsVisible = false
	if err := st.Calendars().Update(ctx, hiddenCal); err != nil {
		t.Fatalf("Update hiddenCal: %v", err)
	}

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	hiddenEvent, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: hiddenCal.ID, ProviderEventID: newID(), Title: "Hidden event",
		Start: from.Add(1 * time.Hour), End: from.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Upsert hiddenEvent: %v", err)
	}

	withoutSelection, err := st.Events().ListInRange(ctx, "u1", from, to, nil)
	if err != nil {
		t.Fatalf("ListInRange nil calendarIDs: %v", err)
	}
	for _, e := range withoutSelection {
		if e.ID == hiddenEvent.ID {
			t.Fatalf("hidden calendar event leaked into default listing: %+v", withoutSelection)
		}
	}

	withSelection, err := st.Events().ListInRange(ctx, "u1", from, to, []string{hiddenCal.ID})
	if err != nil {
		t.Fatalf("ListInRange explicit calendarIDs: %v", err)
	}
	found := false
	for _, e := range withSelection {
		if e.ID == hiddenEvent.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("explicit calendarIDs selection must include hidden calendar event: %+v", withSelection)
	}
}

func TestEventRepoSearch(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	ev, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: newID(), Title: "Team standup",
		Start: start, End: start.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	found, err := st.Events().Search(ctx, "u1", "standup", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(found) != 1 || found[0].ID != ev.ID {
		t.Fatalf("Search = %+v", found)
	}

	none, err := st.Events().Search(ctx, "u1", "nonexistentzzz", 0)
	if err != nil {
		t.Fatalf("Search no-match: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("Search no-match = %+v, want empty non-nil slice", none)
	}
}

func TestEventRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	start := time.Now().UTC().Truncate(time.Microsecond)
	ev, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: newID(), Title: "To delete",
		Start: start, End: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := st.Events().Delete(ctx, ev.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Events().GetByID(ctx, ev.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Events().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestEventRepoDeleteByProviderID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	pid := newID()
	start := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: cal.ID, ProviderEventID: pid, Title: "To delete",
		Start: start, End: start.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := st.Events().DeleteByProviderID(ctx, cal.ID, pid); err != nil {
		t.Fatalf("DeleteByProviderID: %v", err)
	}
	if _, err := st.Events().GetByProviderID(ctx, cal.ID, pid); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByProviderID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Events().DeleteByProviderID(ctx, cal.ID, "unknown-pid"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteByProviderID unknown pair: err = %v, want ErrNotFound", err)
	}
}
