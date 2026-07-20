package postgres

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// --- M2.8 Task 17: all-kinds managed-event listing, user-scoped --------------

func seedManagedEvent(t *testing.T, st *Store, calendarID, userID, providerEventID string, kind domain.ManagedKind) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ev, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: calendarID, ProviderEventID: providerEventID, Title: string(kind),
		Start: now, End: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if err := st.ManagedEvents().Create(ctx, domain.ManagedEvent{
		EventID: ev.ID, UserID: userID, Kind: kind,
	}); err != nil {
		t.Fatalf("seed managed event: %v", err)
	}
	return ev.ID
}

func TestInsightsManagedEventsListAllByUserSpansKinds(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	cal := seedCalendar(t, st, acct.ID)

	focusID := seedManagedEvent(t, st, cal.ID, "u1", "pe-focus", domain.ManagedFocus)
	bufferID := seedManagedEvent(t, st, cal.ID, "u1", "pe-buffer", domain.ManagedBuffer)
	travelID := seedManagedEvent(t, st, cal.ID, "u1", "pe-travel", domain.ManagedTravel)

	got, err := st.InsightsManagedEvents().ListAllByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListAllByUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (every kind in one query)", len(got))
	}
	kinds := map[string]domain.ManagedKind{}
	for _, m := range got {
		kinds[m.EventID] = m.Kind
	}
	if kinds[focusID] != domain.ManagedFocus || kinds[bufferID] != domain.ManagedBuffer || kinds[travelID] != domain.ManagedTravel {
		t.Fatalf("kinds = %v, want focus/buffer/travel rows", kinds)
	}
}

func TestInsightsManagedEventsListAllByUserIsUserScoped(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	acct1 := seedAccount(t, st, "u1")
	acct2 := seedAccount(t, st, "u2")
	cal1 := seedCalendar(t, st, acct1.ID)
	cal2 := seedCalendar(t, st, acct2.ID)

	mine := seedManagedEvent(t, st, cal1.ID, "u1", "pe-mine", domain.ManagedFocus)
	seedManagedEvent(t, st, cal2.ID, "u2", "pe-theirs", domain.ManagedFocus)

	got, err := st.InsightsManagedEvents().ListAllByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListAllByUser: %v", err)
	}
	if len(got) != 1 || got[0].EventID != mine {
		t.Fatalf("got = %+v, want exactly u1's row — cross-tenant leak", got)
	}

	// A user with no rows gets an empty, non-nil slice — never another
	// tenant's data.
	none, err := st.InsightsManagedEvents().ListAllByUser(ctx, "stranger")
	if err != nil {
		t.Fatalf("ListAllByUser(stranger): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("stranger rows = %#v, want non-nil empty", none)
	}
}
