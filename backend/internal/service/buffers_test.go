package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// Times build on autoMonday (Monday 2026-07-20 00:00 UTC, automation_test.go).
func bufAt(day, hour, minute int) time.Time {
	return autoMonday.AddDate(0, 0, day).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func bufPrefs(minutes int) domain.CalendarPrefs {
	p := domain.DefaultCalendarPrefs("u1")
	p.AutoBufferMinutes = minutes
	return p
}

// bufMeeting builds a confirmed timed event with n attendees.
func bufMeeting(id string, start, end time.Time, attendees int) domain.Event {
	ev := domain.Event{ID: id, Start: start, End: end, Status: domain.EventConfirmed}
	for i := 0; i < attendees; i++ {
		ev.Attendees = append(ev.Attendees, domain.Attendee{Email: fmt.Sprintf("%s-p%d@example.com", id, i)})
	}
	return ev
}

// bufOwned tags eventID as an engine-owned buffer trailing sourceEventID.
func bufOwned(eventID, sourceEventID string) (string, domain.ManagedEvent) {
	src := sourceEventID
	return eventID, domain.ManagedEvent{EventID: eventID, UserID: "u1", Kind: domain.ManagedBuffer, SourceEventID: &src}
}

func TestPlanBuffers(t *testing.T) {
	at := func(h, m int) time.Time { return bufAt(0, h, m) }

	type wantCreate struct {
		start, end time.Time
		source     string
	}
	cases := []struct {
		name    string
		minutes int
		events  []domain.Event
		owned   map[string]domain.ManagedEvent
		create  []wantCreate
		remove  []string
	}{
		{
			name:    "back-to-back pair gets one buffer",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
			create: []wantCreate{{at(10, 0), at(10, 5), "m1"}},
		},
		{
			name:    "triple chain gets two buffers",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 5), at(11, 0), 3),
				bufMeeting("m3", at(11, 10), at(12, 0), 2),
			},
			create: []wantCreate{
				{at(10, 0), at(10, 5), "m1"},
				{at(11, 0), at(11, 10), "m2"},
			},
		},
		{
			name:    "existing sufficient gap means none",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 30), at(11, 0), 2),
			},
		},
		{
			name:    "gap exactly the buffer size means none",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 15), at(11, 0), 2),
			},
		},
		{
			// Truly touching meetings leave no room to insert anything: a
			// buffer never exceeds the true gap, and the true gap is zero.
			name:    "touching meetings leave no room",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 0), at(11, 0), 2),
			},
		},
		{
			name:    "overlapping meetings get no buffer",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(9, 30), at(10, 30), 2),
			},
		},
		{
			// A solo block (no attendees) is not a meeting: neither a source
			// nor a follower.
			name:    "solo blocks are skipped",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("solo", at(9, 0), at(10, 0), 0),
				bufMeeting("m1", at(10, 5), at(11, 0), 2),
				bufMeeting("solo2", at(11, 5), at(12, 0), 0),
			},
		},
		{
			// One listed attendee is a block, not a meeting (>= 2 humans).
			name:    "single-attendee events are not meetings",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 1),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
		},
		{
			name:    "pref zero means empty plan",
			minutes: 0,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
		},
		{
			name:    "cancelled and all-day events are ignored",
			minutes: 15,
			events: func() []domain.Event {
				cancelled := bufMeeting("c1", at(10, 5), at(11, 0), 2)
				cancelled.Status = domain.EventCancelled
				allDay := bufMeeting("a1", bufAt(0, 0, 0), bufAt(1, 0, 0), 2)
				allDay.AllDay = true
				return []domain.Event{bufMeeting("m1", at(9, 0), at(10, 0), 2), cancelled, allDay}
			}(),
		},
		{
			// The tight gap is already held by a user event: never overlap it.
			name:    "gap held by a user event gets no buffer",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("hold", at(10, 0), at(10, 5), 0),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
		},
		{
			// Two meetings ending together want the same slot exactly once.
			name:    "co-terminal meetings share one buffer",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1a", at(9, 0), at(10, 0), 2),
				bufMeeting("m1b", at(9, 30), at(10, 0), 2),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
			create: []wantCreate{{at(10, 0), at(10, 5), "m1a"}},
		},
		{
			name:    "existing buffer is idempotent",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("b1", at(10, 0), at(10, 5), 0),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
			},
			owned: func() map[string]domain.ManagedEvent {
				id, m := bufOwned("b1", "m1")
				return map[string]domain.ManagedEvent{id: m}
			}(),
		},
		{
			// The buffer is not a meeting: it never seeds a buffer of its own
			// even while it sits back-to-back with m2.
			name:    "buffer-after-buffer never happens",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("b1", at(10, 0), at(10, 5), 0),
				bufMeeting("m2", at(10, 5), at(11, 0), 2),
				bufMeeting("m3", at(11, 5), at(12, 0), 2),
			},
			owned: func() map[string]domain.ManagedEvent {
				id, m := bufOwned("b1", "m1")
				return map[string]domain.ManagedEvent{id: m}
			}(),
			create: []wantCreate{{at(11, 0), at(11, 5), "m2"}},
		},
		{
			name:    "meeting moved leaves stale buffer removed and a new one created",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 30), 2), // used to end at 10:00
				bufMeeting("b1", at(10, 0), at(10, 5), 0), // stale: trails the old end
				bufMeeting("m2", at(10, 35), at(11, 0), 2),
			},
			owned: func() map[string]domain.ManagedEvent {
				id, m := bufOwned("b1", "m1")
				return map[string]domain.ManagedEvent{id: m}
			}(),
			create: []wantCreate{{at(10, 30), at(10, 35), "m1"}},
			remove: []string{"b1"},
		},
		{
			name:    "following meeting vanished removes the buffer",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", at(9, 0), at(10, 0), 2),
				bufMeeting("b1", at(10, 0), at(10, 5), 0),
			},
			owned: func() map[string]domain.ManagedEvent {
				id, m := bufOwned("b1", "m1")
				return map[string]domain.ManagedEvent{id: m}
			}(),
			remove: []string{"b1"},
		},
		{
			// Day edge: the buffer crosses midnight untouched — pure instants.
			name:    "buffer crosses midnight",
			minutes: 15,
			events: []domain.Event{
				bufMeeting("m1", bufAt(0, 22, 0), bufAt(0, 23, 55), 2),
				bufMeeting("m2", bufAt(1, 0, 0), bufAt(1, 1, 0), 2),
			},
			create: []wantCreate{{bufAt(0, 23, 55), bufAt(1, 0, 0), "m1"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planBuffers(bufPrefs(tc.minutes), tc.events, tc.owned)

			if len(plan.create) != len(tc.create) {
				t.Fatalf("create = %+v, want %d buffers %+v", plan.create, len(tc.create), tc.create)
			}
			for i, want := range tc.create {
				got := plan.create[i]
				if got.input.Title != bufferEventTitle {
					t.Fatalf("create[%d].Title = %q, want %q", i, got.input.Title, bufferEventTitle)
				}
				if !got.input.Start.Equal(want.start) || !got.input.End.Equal(want.end) {
					t.Fatalf("create[%d] = %v-%v, want %v-%v", i, got.input.Start, got.input.End, want.start, want.end)
				}
				if got.sourceEventID != want.source {
					t.Fatalf("create[%d].source = %q, want %q", i, got.sourceEventID, want.source)
				}
				if len(got.input.AttendeeEmails) != 0 {
					t.Fatalf("buffer %d has attendees %v — buffers never invite anyone", i, got.input.AttendeeEmails)
				}
				if got.input.ReminderMinutes == nil || len(got.input.ReminderMinutes) != 0 {
					t.Fatalf("buffer %d reminders = %v, want explicit none", i, got.input.ReminderMinutes)
				}
			}

			if len(plan.remove) != len(tc.remove) {
				t.Fatalf("remove = %v, want %v", plan.remove, tc.remove)
			}
			removed := map[string]bool{}
			for _, id := range plan.remove {
				removed[id] = true
			}
			for _, id := range tc.remove {
				if !removed[id] {
					t.Fatalf("remove = %v, want it to include %q", plan.remove, id)
				}
			}

			// The planner may only ever remove events it owns.
			for _, id := range plan.remove {
				if _, ok := tc.owned[id]; !ok {
					t.Fatalf("planner removed %q, which it does not own — forbidden", id)
				}
			}
		})
	}
}

// TestPlanBuffersDSTGap: Europe/Amsterdam springs forward on 2026-03-29
// (02:00 CET -> 03:00 CEST). A meeting ending 01:55 CET and one starting
// 03:05 CEST look 70 wall-clock minutes apart but are 10 real minutes apart
// — the planner works on instants, so the tight gap still gets its buffer.
func TestPlanBuffersDSTGap(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("tz: %v", err)
	}
	prefs := bufPrefs(15)
	prefs.TimeZone = "Europe/Amsterdam"
	m1 := bufMeeting("m1", time.Date(2026, 3, 29, 1, 0, 0, 0, loc), time.Date(2026, 3, 29, 1, 55, 0, 0, loc), 2)
	m2 := bufMeeting("m2", time.Date(2026, 3, 29, 3, 5, 0, 0, loc), time.Date(2026, 3, 29, 4, 0, 0, 0, loc), 2)

	plan := planBuffers(prefs, []domain.Event{m1, m2}, nil)

	if len(plan.create) != 1 {
		t.Fatalf("create = %+v, want one buffer in the 10-real-minute gap", plan.create)
	}
	got := plan.create[0]
	if !got.input.Start.Equal(m1.End) || !got.input.End.Equal(m2.Start) {
		t.Fatalf("buffer = %v-%v, want %v-%v", got.input.Start, got.input.End, m1.End, m2.Start)
	}
	if d := got.input.End.Sub(got.input.Start); d != 10*time.Minute {
		t.Fatalf("buffer duration = %v, want 10m of real time across the DST jump", d)
	}
}

// --- orchestration -----------------------------------------------------------

func (f *automationFixture) bufferTagsFor(userID string) []domain.ManagedEvent {
	out, _ := f.managed.ListByUser(context.Background(), userID, domain.ManagedBuffer)
	return out
}

// seedMeeting mirrors a user meeting with two attendees on cal.
func (f *automationFixture) seedMeeting(t *testing.T, cal domain.Calendar, id string, start, end time.Time) domain.Event {
	t.Helper()
	ev := bufMeeting(id, start, end, 2)
	ev.CalendarID = cal.ID
	ev.ProviderEventID = "p-" + id
	got, err := f.events.Upsert(context.Background(), ev)
	if err != nil {
		t.Fatalf("seed meeting %s: %v", id, err)
	}
	return got
}

func TestRunAutomationBuffersCreatesTaggedProviderEvents(t *testing.T) {
	f := newAutomationFixture()
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.AutoBufferMinutes = 10 })
	m1 := f.seedMeeting(t, cal, "m1", bufAt(0, 9, 0), bufAt(0, 10, 0))
	f.seedMeeting(t, cal, "m2", bufAt(0, 10, 5), bufAt(0, 11, 0))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}

	tags := f.bufferTagsFor("u1")
	if len(tags) != 1 {
		t.Fatalf("buffer tags = %+v, want exactly one", tags)
	}
	tag := tags[0]
	if tag.Kind != domain.ManagedBuffer || tag.UserID != "u1" {
		t.Fatalf("tag = %+v", tag)
	}
	if tag.SourceEventID == nil || *tag.SourceEventID != m1.ID {
		t.Fatalf("tag.SourceEventID = %v, want %q (the meeting the buffer trails)", tag.SourceEventID, m1.ID)
	}
	ev, err := f.events.GetByID(ctx, tag.EventID)
	if err != nil {
		t.Fatalf("buffer tag has no mirror event: %v", err)
	}
	if ev.Title != bufferEventTitle || !ev.Start.Equal(bufAt(0, 10, 0)) || !ev.End.Equal(bufAt(0, 10, 5)) {
		t.Fatalf("buffer event = %+v, want %q 10:00-10:05", ev, bufferEventTitle)
	}
	if ev.CalendarID != cal.ID {
		t.Fatalf("buffer on calendar %q, want primary %q", ev.CalendarID, cal.ID)
	}
}

func TestRunAutomationBuffersIsIdempotent(t *testing.T) {
	f := newAutomationFixture()
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.AutoBufferMinutes = 10 })
	f.seedMeeting(t, cal, "m1", bufAt(0, 9, 0), bufAt(0, 10, 0))
	f.seedMeeting(t, cal, "m2", bufAt(0, 10, 5), bufAt(0, 11, 0))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	createdAfterFirst := f.calSvc.created

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if f.calSvc.created != createdAfterFirst {
		t.Fatalf("second run created %d more events — not idempotent", f.calSvc.created-createdAfterFirst)
	}
	if len(f.calSvc.deletedIDs) != 0 {
		t.Fatalf("second run deleted %v — not idempotent", f.calSvc.deletedIDs)
	}
	if got := len(f.bufferTagsFor("u1")); got != 1 {
		t.Fatalf("buffer tags = %d after second run, want 1", got)
	}
}

func TestRunAutomationBuffersMeetingMovedReplansAndNeverTouchesUserEvents(t *testing.T) {
	f := newAutomationFixture()
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.AutoBufferMinutes = 10 })
	f.seedMeeting(t, cal, "m1", bufAt(0, 9, 0), bufAt(0, 10, 0))
	m2 := f.seedMeeting(t, cal, "m2", bufAt(0, 10, 5), bufAt(0, 11, 0))
	ctx := context.Background()

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	stale := f.bufferTagsFor("u1")[0]

	// The second meeting moves out to a comfortable 40-minute gap.
	m2.Start = bufAt(0, 10, 40)
	if _, err := f.events.Upsert(ctx, m2); err != nil {
		t.Fatalf("move m2: %v", err)
	}

	if err := f.svc.RunAutomation(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// The stale buffer is gone — provider delete + tag delete.
	if _, err := f.events.GetByID(ctx, stale.EventID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stale buffer still mirrored: err = %v", err)
	}
	if _, err := f.managed.GetByEventID(ctx, stale.EventID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stale buffer tag survived")
	}
	if got := len(f.bufferTagsFor("u1")); got != 0 {
		t.Fatalf("buffer tags = %d after the gap widened, want 0", got)
	}

	// User meetings are never deleted or mutated by the buffer pass.
	for _, id := range f.calSvc.deletedIDs {
		if id == "m1" || id == "m2" {
			t.Fatalf("automation deleted user event %q — forbidden", id)
		}
	}
	got, err := f.events.GetByID(ctx, "m2")
	if err != nil {
		t.Fatalf("user meeting gone: %v", err)
	}
	if !got.Start.Equal(bufAt(0, 10, 40)) || !got.End.Equal(bufAt(0, 11, 0)) {
		t.Fatalf("user meeting mutated: %+v", got)
	}
}

func TestRunAutomationBuffersProviderFailureLeavesNothingLocal(t *testing.T) {
	f := newAutomationFixture()
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.AutoBufferMinutes = 10 })
	f.seedMeeting(t, cal, "m1", bufAt(0, 9, 0), bufAt(0, 10, 0))
	f.seedMeeting(t, cal, "m2", bufAt(0, 10, 5), bufAt(0, 11, 0))
	f.calSvc.failUsers["u1"] = true

	// The fleet run itself must not fail (per-user log + continue).
	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation must not fail the fleet: %v", err)
	}

	// Honesty policy: provider write failed, so nothing local exists.
	if got := len(f.bufferTagsFor("u1")); got != 0 {
		t.Fatalf("buffer tags = %d after provider failure, want 0", got)
	}
	evs, _ := f.events.ListInRange(context.Background(), "u1", autoMonday, autoMonday.AddDate(0, 0, 14), nil)
	for _, ev := range evs {
		if ev.Title == bufferEventTitle {
			t.Fatalf("mirrored buffer despite provider failure: %+v", ev)
		}
	}
}

func TestRunAutomationBuffersPrefZeroCreatesNothing(t *testing.T) {
	f := newAutomationFixture()
	// Automation on via weather, but buffers off.
	cal := f.addUser(t, "u1", func(p *domain.CalendarPrefs) { p.WeatherEnabled = true })
	f.seedMeeting(t, cal, "m1", bufAt(0, 9, 0), bufAt(0, 10, 0))
	f.seedMeeting(t, cal, "m2", bufAt(0, 10, 5), bufAt(0, 11, 0))

	if err := f.svc.RunAutomation(context.Background()); err != nil {
		t.Fatalf("RunAutomation: %v", err)
	}
	if f.calSvc.created != 0 || len(f.bufferTagsFor("u1")) != 0 {
		t.Fatalf("created %d events with autoBufferMinutes=0", f.calSvc.created)
	}
}
