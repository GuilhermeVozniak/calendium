package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// seedNoteEvent stores a minimal event on calendarID for note tests.
func seedNoteEvent(t *testing.T, st *Store, calendarID string) domain.Event {
	t.Helper()
	start := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	ev, err := st.Events().Upsert(context.Background(), domain.Event{
		CalendarID:      calendarID,
		ProviderEventID: newID(),
		Title:           "Sync",
		Start:           start,
		End:             start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return ev
}

func TestEventNoteUpsertGetRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	c := seedCalendar(t, st, a.ID)
	ev := seedNoteEvent(t, st, c.ID)

	saved, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID,
		UserID:  u.ID,
		BodyMD:  "# Agenda\n- intros",
		Links:   []string{"https://notion.so/doc", "https://docs.google.com/d/1"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if saved.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not set on upsert")
	}

	got, err := st.EventNotes().GetByEventID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EventID != ev.ID || got.UserID != u.ID || got.BodyMD != "# Agenda\n- intros" {
		t.Fatalf("got = %+v, want round-tripped note", got)
	}
	if want := []string{"https://notion.so/doc", "https://docs.google.com/d/1"}; !reflect.DeepEqual(got.Links, want) {
		t.Fatalf("links = %v, want %v", got.Links, want)
	}

	// Upsert replaces in place (single row per event) and coerces nil links
	// to a non-nil empty slice.
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID, UserID: u.ID, BodyMD: "replaced", Links: nil,
	}); err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	got2, err := st.EventNotes().GetByEventID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if got2.BodyMD != "replaced" {
		t.Fatalf("BodyMD = %q, want replaced", got2.BodyMD)
	}
	if got2.Links == nil || len(got2.Links) != 0 {
		t.Fatalf("links = %#v, want non-nil empty slice", got2.Links)
	}
}

func TestEventNoteEmptyUpsertDeletes(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	c := seedCalendar(t, st, a.ID)
	ev := seedNoteEvent(t, st, c.ID)

	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID, UserID: u.ID, BodyMD: "scratch", Links: []string{"https://x.test/doc"},
	}); err != nil {
		t.Fatalf("seed note: %v", err)
	}

	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID, UserID: u.ID, BodyMD: "", Links: nil,
	}); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
	if _, err := st.EventNotes().GetByEventID(ctx, ev.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after empty upsert: err = %v, want ErrNotFound", err)
	}

	// Clearing an already-missing note stays idempotent.
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID, UserID: u.ID,
	}); err != nil {
		t.Fatalf("second empty upsert: %v", err)
	}
}

func TestEventNoteCascadeOnEventDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	u := seedUser(t, st, "u1")
	a := seedAccount(t, st, u.ID)
	c := seedCalendar(t, st, a.ID)
	ev := seedNoteEvent(t, st, c.ID)

	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{
		EventID: ev.ID, UserID: u.ID, BodyMD: "keep me?", Links: []string{},
	}); err != nil {
		t.Fatalf("seed note: %v", err)
	}

	if err := st.Events().Delete(ctx, ev.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	if _, err := st.EventNotes().GetByEventID(ctx, ev.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("note survived event delete: err = %v, want ErrNotFound", err)
	}
}
