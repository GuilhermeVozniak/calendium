package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/ics"
	"calendium/backend/internal/port"
)

// --- fakes ------------------------------------------------------------------

type fakeCalSubRepo struct {
	subs         map[string]domain.CalendarSubscription
	events       map[string][]domain.Event // subscriptionID -> occurrences
	nextID       int
	replaceCalls int
	rangeCalls   int
}

func newFakeCalSubRepo() *fakeCalSubRepo {
	return &fakeCalSubRepo{subs: map[string]domain.CalendarSubscription{}, events: map[string][]domain.Event{}}
}

var _ port.CalendarSubscriptionRepo = (*fakeCalSubRepo)(nil)

func (f *fakeCalSubRepo) Create(_ context.Context, s domain.CalendarSubscription) (domain.CalendarSubscription, error) {
	for _, existing := range f.subs {
		if existing.UserID == s.UserID && existing.URL == s.URL {
			return domain.CalendarSubscription{}, fmt.Errorf("%w: already subscribed", domain.ErrConflict)
		}
	}
	if s.ID == "" {
		f.nextID++
		s.ID = fmt.Sprintf("sub%d", f.nextID)
	}
	f.subs[s.ID] = s
	return s, nil
}

func (f *fakeCalSubRepo) GetByID(_ context.Context, id string) (domain.CalendarSubscription, error) {
	s, ok := f.subs[id]
	if !ok {
		return domain.CalendarSubscription{}, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeCalSubRepo) ListByUser(_ context.Context, userID string) ([]domain.CalendarSubscription, error) {
	var out []domain.CalendarSubscription
	for _, s := range f.subs {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeCalSubRepo) ListDue(_ context.Context, since time.Time) ([]domain.CalendarSubscription, error) {
	var out []domain.CalendarSubscription
	for _, s := range f.subs {
		if s.LastFetchedAt == nil || !s.LastFetchedAt.After(since) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeCalSubRepo) Update(_ context.Context, s domain.CalendarSubscription) error {
	if _, ok := f.subs[s.ID]; !ok {
		return domain.ErrNotFound
	}
	f.subs[s.ID] = s
	return nil
}

func (f *fakeCalSubRepo) Delete(_ context.Context, id string) error {
	if _, ok := f.subs[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.subs, id)
	delete(f.events, id)
	return nil
}

func (f *fakeCalSubRepo) ReplaceEvents(_ context.Context, subscriptionID string, events []domain.Event) error {
	f.replaceCalls++
	f.events[subscriptionID] = events
	return nil
}

// ListEventsInRange mirrors the SQL contract: visible subscriptions only,
// overlap with [from, to), SubscriptionID set, Status confirmed.
func (f *fakeCalSubRepo) ListEventsInRange(_ context.Context, userID string, from, to time.Time) ([]domain.Event, error) {
	f.rangeCalls++
	out := []domain.Event{}
	for subID, evs := range f.events {
		sub, ok := f.subs[subID]
		if !ok || sub.UserID != userID || !sub.IsVisible {
			continue
		}
		for _, ev := range evs {
			if ev.Start.Before(to) && ev.End.After(from) {
				id := subID
				ev.SubscriptionID = &id
				ev.Status = domain.EventConfirmed
				out = append(out, ev)
			}
		}
	}
	return out, nil
}

type fakeIcsFetcher struct {
	cal         ics.Calendar
	etag        string
	notModified bool
	err         error

	calls   int
	gotURL  string
	gotEtag string
}

var _ port.IcsFetcher = (*fakeIcsFetcher)(nil)

func (f *fakeIcsFetcher) Fetch(_ context.Context, url, etag string) (ics.Calendar, string, bool, error) {
	f.calls++
	f.gotURL, f.gotEtag = url, etag
	if f.err != nil {
		return ics.Calendar{}, "", false, f.err
	}
	return f.cal, f.etag, f.notModified, nil
}

// --- fixture ----------------------------------------------------------------

var subTestNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func newSubFixture(fetcher *fakeIcsFetcher) (*CalendarService, *fakeCalSubRepo, *fakeEventRepo) {
	subs := newFakeCalSubRepo()
	events := newEventRepo()
	svc := NewCalendarService(CalendarServiceDeps{
		Accounts:     newAccountRepo(),
		Calendars:    newCalendarRepo(),
		Events:       events,
		Clock:        newClock(subTestNow),
		SelfHosted:   true,
		CalendarSubs: subs,
		IcsFetcher:   fetcher,
	})
	return svc, subs, events
}

func holidayFeed() ics.Calendar {
	return ics.Calendar{
		Name: "US Holidays",
		Events: []ics.Event{
			{
				UID:     "july4@x",
				Summary: "Independence Day",
				Start:   time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
				End:     time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC),
				AllDay:  true,
			},
			{
				UID:     "standup@x",
				Summary: "Weekly sync",
				Start:   time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
				End:     time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC),
				RRule:   "FREQ=WEEKLY;COUNT=3",
			},
			{
				UID:     "gone@x",
				Summary: "Cancelled thing",
				Start:   time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC),
				End:     time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC),
				Status:  "CANCELLED",
			},
		},
	}
}

// --- tests ------------------------------------------------------------------

func TestCreateCalendarSubscriptionFetchesImmediately(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakeIcsFetcher{cal: holidayFeed(), etag: `"v1"`}
	svc, subs, _ := newSubFixture(fetcher)

	sub, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{
		URL: "https://example.com/holidays.ics",
	})
	if err != nil {
		t.Fatalf("CreateCalendarSubscription: %v", err)
	}
	if fetcher.calls != 1 || fetcher.gotURL != "https://example.com/holidays.ics" || fetcher.gotEtag != "" {
		t.Fatalf("fetcher calls=%d url=%q etag=%q, want one uncached fetch", fetcher.calls, fetcher.gotURL, fetcher.gotEtag)
	}
	if sub.Name != "US Holidays" {
		t.Fatalf("Name = %q, want feed X-WR-CALNAME", sub.Name)
	}
	if sub.Etag != `"v1"` {
		t.Fatalf("Etag = %q, want cached validator", sub.Etag)
	}
	if sub.LastFetchedAt == nil || !sub.LastFetchedAt.Equal(subTestNow) {
		t.Fatalf("LastFetchedAt = %v, want %v", sub.LastFetchedAt, subTestNow)
	}
	if sub.Color != domain.DefaultSubscriptionColor || !sub.IsVisible {
		t.Fatalf("defaults not applied: %+v", sub)
	}

	evs := subs.events[sub.ID]
	// 1 all-day holiday + 3 weekly occurrences (COUNT=3); CANCELLED dropped.
	if len(evs) != 4 {
		t.Fatalf("expanded events = %d, want 4 (got %+v)", len(evs), evs)
	}
	for _, ev := range evs {
		if ev.Title == "Cancelled thing" {
			t.Fatal("CANCELLED event must be dropped")
		}
		if ev.Status != domain.EventConfirmed {
			t.Fatalf("Status = %q, want confirmed", ev.Status)
		}
	}
}

func TestCreateCalendarSubscriptionRejectsBadInput(t *testing.T) {
	ctx := context.Background()

	t.Run("non-https URL is 400 and never fetched", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{cal: holidayFeed()}
		svc, subs, _ := newSubFixture(fetcher)
		_, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "http://example.com/a.ics"})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		if fetcher.calls != 0 || len(subs.subs) != 0 {
			t.Fatalf("invalid URL must not fetch or persist (calls=%d subs=%d)", fetcher.calls, len(subs.subs))
		}
	})

	t.Run("fetch failure is 422 and nothing persists", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{err: errors.New("feed answered status 500")}
		svc, subs, _ := newSubFixture(fetcher)
		_, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/a.ics"})
		if !errors.Is(err, domain.ErrUnprocessable) {
			t.Fatalf("err = %v, want ErrUnprocessable", err)
		}
		if len(subs.subs) != 0 || subs.replaceCalls != 0 {
			t.Fatal("failed create must not persist anything")
		}
	})

	t.Run("duplicate url is 409", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{cal: holidayFeed()}
		svc, _, _ := newSubFixture(fetcher)
		if _, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/a.ics"}); err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/a.ics"})
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("user name beats feed name", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{cal: holidayFeed()}
		svc, _, _ := newSubFixture(fetcher)
		sub, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{
			URL: "https://example.com/a.ics", Name: "My feed", Color: "#123456",
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if sub.Name != "My feed" || sub.Color != "#123456" {
			t.Fatalf("sub = %+v, want explicit name/color kept", sub)
		}
	})
}

func TestListEventsMergesSubscriptionEvents(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakeIcsFetcher{cal: holidayFeed(), etag: `"v1"`}
	svc, subs, events := newSubFixture(fetcher)

	sub, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/holidays.ics"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One provider event of our own inside the window.
	if _, err := events.Upsert(ctx, domain.Event{
		ID: "mine", CalendarID: "cal1", Title: "1:1",
		Start:  time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		Status: domain.EventConfirmed,
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	from := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	got, err := svc.ListEvents(ctx, "u1", from, to, nil)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	// Window holds: my 1:1 + the July 20 weekly occurrence. (The holiday and
	// later weekly occurrences fall outside.)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (got %+v)", len(got), got)
	}
	var sawMine, sawSub bool
	for _, ev := range got {
		if ev.ID == "mine" {
			sawMine = true
			continue
		}
		sawSub = true
		if ev.SubscriptionID == nil || *ev.SubscriptionID != sub.ID {
			t.Fatalf("subscription event without SubscriptionID: %+v", ev)
		}
	}
	if !sawMine || !sawSub {
		t.Fatalf("merge incomplete: mine=%v sub=%v", sawMine, sawSub)
	}
	// Sorted by start: weekly sync (09:00) before my 1:1 (11:00).
	if !got[0].Start.Before(got[1].Start) {
		t.Fatalf("not sorted by start: %v then %v", got[0].Start, got[1].Start)
	}

	// Hiding the feed removes its events from the merge.
	hidden := false
	if _, err := svc.UpdateCalendarSubscription(ctx, "u1", sub.ID, port.CalendarSubscriptionPatch{IsVisible: &hidden}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	got, err = svc.ListEvents(ctx, "u1", from, to, nil)
	if err != nil {
		t.Fatalf("ListEvents hidden: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("hidden feed leaked: %+v", got)
	}

	// Narrowing to explicit calendarIds excludes feed events entirely.
	visible := true
	if _, err := svc.UpdateCalendarSubscription(ctx, "u1", sub.ID, port.CalendarSubscriptionPatch{IsVisible: &visible}); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	subs.rangeCalls = 0
	got, err = svc.ListEvents(ctx, "u1", from, to, []string{"cal1"})
	if err != nil {
		t.Fatalf("ListEvents calendarIds: %v", err)
	}
	if subs.rangeCalls != 0 {
		t.Fatal("calendarIds filter must not consult subscription events")
	}
	for _, ev := range got {
		if ev.SubscriptionID != nil {
			t.Fatalf("calendarIds-filtered list contains feed event: %+v", ev)
		}
	}
}

// TestAvailabilityIgnoresSubscriptionEvents extends the availability
// contract: informational feeds must never block booking.
func TestAvailabilityIgnoresSubscriptionEvents(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakeIcsFetcher{cal: holidayFeed()}
	svc, subs, _ := newSubFixture(fetcher)

	sub, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/holidays.ics"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// A feed event blanketing the whole probe window.
	subs.events[sub.ID] = []domain.Event{{
		ProviderEventID: "block@x", Title: "All-consuming feed event",
		Start: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
	}}

	from := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 20, 17, 0, 0, 0, time.UTC)
	subs.rangeCalls = 0
	slots, err := svc.Availability(ctx, "u1", from, to, 30*time.Minute)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if subs.rangeCalls != 0 {
		t.Fatal("Availability must never consult subscription events")
	}
	if len(slots) != 1 || !slots[0].Start.Equal(from) || !slots[0].End.Equal(to) {
		t.Fatalf("slots = %+v, want the whole window free", slots)
	}
}

func TestSubscriptionCrudOwnership(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakeIcsFetcher{cal: holidayFeed()}
	svc, _, _ := newSubFixture(fetcher)

	sub, err := svc.CreateCalendarSubscription(ctx, "u1", port.CalendarSubscriptionInput{URL: "https://example.com/holidays.ics"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Cross-tenant negatives: u2 can neither patch nor delete u1's feed.
	name := "stolen"
	if _, err := svc.UpdateCalendarSubscription(ctx, "u2", sub.ID, port.CalendarSubscriptionPatch{Name: &name}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant update err = %v, want ErrNotFound", err)
	}
	if err := svc.DeleteCalendarSubscription(ctx, "u2", sub.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant delete err = %v, want ErrNotFound", err)
	}

	// Owner patch works field by field.
	color := "#00ff00"
	updated, err := svc.UpdateCalendarSubscription(ctx, "u1", sub.ID, port.CalendarSubscriptionPatch{Color: &color})
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if updated.Color != "#00ff00" || updated.Name != sub.Name {
		t.Fatalf("updated = %+v, want only color changed", updated)
	}

	list, err := svc.ListCalendarSubscriptions(ctx, "u1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := svc.DeleteCalendarSubscription(ctx, "u1", sub.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	list, err = svc.ListCalendarSubscriptions(ctx, "u1")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list len = %d, want 0", len(list))
	}
}

func TestSubscriptionRefresherPass(t *testing.T) {
	ctx := context.Background()

	seed := func(fetcher *fakeIcsFetcher) (*SubscriptionRefresher, *fakeCalSubRepo, domain.CalendarSubscription) {
		subs := newFakeCalSubRepo()
		stale := subTestNow.Add(-2 * time.Hour)
		sub, _ := subs.Create(ctx, domain.CalendarSubscription{
			UserID: "u1", URL: "https://example.com/holidays.ics", Name: "Holidays",
			Color: domain.DefaultSubscriptionColor, IsVisible: true,
			Etag: `"v1"`, LastFetchedAt: &stale,
		})
		subs.events[sub.ID] = []domain.Event{{ProviderEventID: "old@x", Title: "Old occurrence",
			Start: subTestNow, End: subTestNow.Add(time.Hour)}}
		r := NewSubscriptionRefresher(SubscriptionRefresherDeps{
			Subs: subs, Fetcher: fetcher, Clock: newClock(subTestNow),
		})
		return r, subs, sub
	}

	t.Run("due feed is refetched and its events replaced", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{cal: holidayFeed(), etag: `"v2"`}
		r, subs, sub := seed(fetcher)

		if err := r.RefreshDue(ctx); err != nil {
			t.Fatalf("RefreshDue: %v", err)
		}
		if fetcher.calls != 1 || fetcher.gotEtag != `"v1"` {
			t.Fatalf("fetch calls=%d etag=%q, want 1 revalidating fetch", fetcher.calls, fetcher.gotEtag)
		}
		after := subs.subs[sub.ID]
		if after.Etag != `"v2"` || after.LastError != nil {
			t.Fatalf("after = %+v, want new etag and no error", after)
		}
		if after.LastFetchedAt == nil || !after.LastFetchedAt.Equal(subTestNow) {
			t.Fatalf("LastFetchedAt = %v, want %v", after.LastFetchedAt, subTestNow)
		}
		if len(subs.events[sub.ID]) != 4 {
			t.Fatalf("events = %d, want 4 expanded occurrences", len(subs.events[sub.ID]))
		}
	})

	t.Run("fresh feed is not due", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{cal: holidayFeed()}
		r, subs, sub := seed(fetcher)
		fresh := subTestNow.Add(-5 * time.Minute)
		s := subs.subs[sub.ID]
		s.LastFetchedAt = &fresh
		subs.subs[sub.ID] = s

		if err := r.RefreshDue(ctx); err != nil {
			t.Fatalf("RefreshDue: %v", err)
		}
		if fetcher.calls != 0 {
			t.Fatalf("fetch calls = %d, want 0 for a fresh feed", fetcher.calls)
		}
	})

	t.Run("304 keeps events and clears any previous error", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{notModified: true, etag: `"v1"`}
		r, subs, sub := seed(fetcher)
		prev := "boom"
		s := subs.subs[sub.ID]
		s.LastError = &prev
		subs.subs[sub.ID] = s

		if err := r.RefreshDue(ctx); err != nil {
			t.Fatalf("RefreshDue: %v", err)
		}
		after := subs.subs[sub.ID]
		if after.LastError != nil {
			t.Fatalf("LastError = %v, want cleared on 304", *after.LastError)
		}
		if subs.replaceCalls != 0 {
			t.Fatal("304 must not touch the event set")
		}
		if len(subs.events[sub.ID]) != 1 {
			t.Fatalf("events = %d, want the previous set kept", len(subs.events[sub.ID]))
		}
	})

	t.Run("fetch failure records LastError and keeps the previous good set", func(t *testing.T) {
		fetcher := &fakeIcsFetcher{err: errors.New("feed answered status 500")}
		r, subs, sub := seed(fetcher)

		if err := r.RefreshDue(ctx); err != nil {
			t.Fatalf("RefreshDue: %v (per-feed failures must not fail the pass)", err)
		}
		after := subs.subs[sub.ID]
		if after.LastError == nil || *after.LastError == "" {
			t.Fatal("LastError not recorded")
		}
		if after.LastFetchedAt == nil || !after.LastFetchedAt.Equal(subTestNow) {
			t.Fatalf("LastFetchedAt = %v, want attempt recorded to avoid hot-looping", after.LastFetchedAt)
		}
		if len(subs.events[sub.ID]) != 1 || subs.events[sub.ID][0].Title != "Old occurrence" {
			t.Fatal("previous good event set must be kept on failure")
		}
		if after.Etag != `"v1"` {
			t.Fatalf("Etag = %q, want previous validator kept", after.Etag)
		}
	})
}
