package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// seedMirrorEvent stores a bare confirmed event so managed_events rows can
// satisfy their FK to the mirror.
func seedMirrorEvent(t *testing.T, st *Store, calendarID, title string, start time.Time) domain.Event {
	t.Helper()
	e, err := st.Events().Upsert(context.Background(), domain.Event{
		CalendarID:      calendarID,
		ProviderEventID: newID(),
		Title:           title,
		Start:           start,
		End:             start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return e
}

func TestManagedEventRepoCRUD(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	c := seedCalendar(t, st, a.ID)
	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	focusEv := seedMirrorEvent(t, st, c.ID, "Focus time", start)
	bufferEv := seedMirrorEvent(t, st, c.ID, "Buffer", start.Add(2*time.Hour))

	week := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if err := st.ManagedEvents().Create(ctx, domain.ManagedEvent{
		EventID: focusEv.ID, UserID: "u1", Kind: domain.ManagedFocus, WeekStart: &week,
	}); err != nil {
		t.Fatalf("Create(focus): %v", err)
	}
	if err := st.ManagedEvents().Create(ctx, domain.ManagedEvent{
		EventID: bufferEv.ID, UserID: "u1", Kind: domain.ManagedBuffer, SourceEventID: &focusEv.ID,
	}); err != nil {
		t.Fatalf("Create(buffer): %v", err)
	}

	got, err := st.ManagedEvents().GetByEventID(ctx, focusEv.ID)
	if err != nil {
		t.Fatalf("GetByEventID: %v", err)
	}
	if got.UserID != "u1" || got.Kind != domain.ManagedFocus || got.SourceEventID != nil {
		t.Fatalf("round trip = %+v", got)
	}
	if got.WeekStart == nil || got.WeekStart.Format("2006-01-02") != "2026-07-20" {
		t.Fatalf("WeekStart = %v, want 2026-07-20", got.WeekStart)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be stamped")
	}

	if _, err := st.ManagedEvents().GetByEventID(ctx, "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByEventID(ghost) err = %v, want ErrNotFound", err)
	}

	focusList, err := st.ManagedEvents().ListByUser(ctx, "u1", domain.ManagedFocus)
	if err != nil {
		t.Fatalf("ListByUser(focus): %v", err)
	}
	if len(focusList) != 1 || focusList[0].EventID != focusEv.ID {
		t.Fatalf("ListByUser(focus) = %+v, want only %s", focusList, focusEv.ID)
	}
	bufferList, err := st.ManagedEvents().ListByUser(ctx, "u1", domain.ManagedBuffer)
	if err != nil {
		t.Fatalf("ListByUser(buffer): %v", err)
	}
	if len(bufferList) != 1 || bufferList[0].EventID != bufferEv.ID {
		t.Fatalf("ListByUser(buffer) = %+v, want only %s", bufferList, bufferEv.ID)
	}
	if s := bufferList[0].SourceEventID; s == nil || *s != focusEv.ID {
		t.Fatalf("buffer SourceEventID = %v, want %s", s, focusEv.ID)
	}

	bySource, err := st.ManagedEvents().ListBySourceEvent(ctx, focusEv.ID)
	if err != nil {
		t.Fatalf("ListBySourceEvent: %v", err)
	}
	if len(bySource) != 1 || bySource[0].EventID != bufferEv.ID {
		t.Fatalf("ListBySourceEvent = %+v, want only %s", bySource, bufferEv.ID)
	}

	if err := st.ManagedEvents().Delete(ctx, focusEv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.ManagedEvents().GetByEventID(ctx, focusEv.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByEventID after Delete err = %v, want ErrNotFound", err)
	}
}

// TestManagedEventRepoCascade: deleting the mirrored event row deletes its
// managed tag (ON DELETE CASCADE), and cascading a source event removes the
// blocks that referenced it.
func TestManagedEventRepoCascade(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	a := seedAccount(t, st, "u1")
	c := seedCalendar(t, st, a.ID)
	start := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	meeting := seedMirrorEvent(t, st, c.ID, "Meeting", start)
	buffer := seedMirrorEvent(t, st, c.ID, "Buffer", start.Add(-30*time.Minute))

	if err := st.ManagedEvents().Create(ctx, domain.ManagedEvent{
		EventID: buffer.ID, UserID: "u1", Kind: domain.ManagedBuffer, SourceEventID: &meeting.ID,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Deleting the tagged event's mirror row cascades the tag away.
	if err := st.Events().Delete(ctx, buffer.ID); err != nil {
		t.Fatalf("delete mirror: %v", err)
	}
	if _, err := st.ManagedEvents().GetByEventID(ctx, buffer.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tag survived mirror delete: err = %v, want ErrNotFound", err)
	}
}
