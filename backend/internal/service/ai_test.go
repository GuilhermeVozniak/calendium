package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// aiProposeFixture wires an AIService (paywall bypassed via SelfHosted) plus
// its collaborator fakes for ProposeEvent tests.
type aiProposeFixture struct {
	svc      *AIService
	threads  *fakeThreadRepo
	accounts *fakeAccountRepo
	messages *fakeMessageRepo
	ai       *fakeAI
	cal      *fakeCalendarService
	clock    *fakeClock
}

func newAIProposeFixture(t *testing.T) *aiProposeFixture {
	t.Helper()
	base := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)
	accounts := newAccountRepo()
	threads := newThreadRepo()
	messages := newMessageRepo()
	ai := newAI()
	cal := newCalendarService()

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Calendar:      cal,
		AI:            ai,
		Clock:         clock,
		SelfHosted:    true, // bypass the paywall; entitlement tested elsewhere
	})
	return &aiProposeFixture{svc: svc, threads: threads, accounts: accounts, messages: messages, ai: ai, cal: cal, clock: clock}
}

// seedThread registers an owned thread with one message from a guest.
func (f *aiProposeFixture) seedThread(t *testing.T, participants ...string) {
	t.Helper()
	f.accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "owner@example.com"}
	var parts []domain.EmailAddress
	for _, p := range participants {
		parts = append(parts, domain.EmailAddress{Email: p})
	}
	f.threads.byID["t1"] = domain.Thread{
		ID: "t1", AccountID: "a1", Subject: "Sync next week", Participants: parts,
	}
	if _, err := f.messages.Upsert(context.Background(), domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "guest@example.com"},
		BodyText: "Can we meet?", SentAt: f.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAIProposeEventSnapsToOfferedSlot pins the happy path: the model's
// preferredSlot matches an offered AVAILABILITY slot verbatim, so the
// proposal uses that slot's Start/End, the clamped duration, and the
// title/location/notes straight from the model.
func TestAIProposeEventSnapsToOfferedSlot(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	slot2 := f.clock.Now().Add(48 * time.Hour)
	f.cal.availRet = []domain.AvailabilitySlot{
		{Start: slot1, End: slot1.Add(30 * time.Minute)},
		{Start: slot2, End: slot2.Add(30 * time.Minute)},
	}

	f.ai.jsonOut = `{"title":"Sync call","attendeeEmails":["guest@example.com"],"durationMinutes":45,"preferredSlot":"` +
		slot2.Format(time.RFC3339) + `","location":"Zoom","notes":"agenda"}`
	f.ai.jsonModel = "gpt-test"

	got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("ProposeEvent() error = %v, want nil", err)
	}
	if !got.Start.Equal(slot2) {
		t.Fatalf("Start = %v, want offered slot2 %v", got.Start, slot2)
	}
	if !got.End.Equal(slot2.Add(45 * time.Minute)) {
		t.Fatalf("End = %v, want Start+45m", got.End)
	}
	if got.Title != "Sync call" {
		t.Fatalf("Title = %q, want %q", got.Title, "Sync call")
	}
	if got.Location != "Zoom" || got.Notes != "agenda" {
		t.Fatalf("Location/Notes = %q/%q, want Zoom/agenda", got.Location, got.Notes)
	}
	wantAttendees := []string{"owner@example.com", "guest@example.com"}
	if !reflect.DeepEqual(got.Attendees, wantAttendees) {
		t.Fatalf("Attendees = %v, want %v", got.Attendees, wantAttendees)
	}
	if f.cal.availCalls != 1 {
		t.Fatalf("Availability calls = %d, want 1", f.cal.availCalls)
	}
	wantTo := f.clock.Now().Add(7 * 24 * time.Hour)
	if !f.cal.gotTo.Equal(wantTo) {
		t.Fatalf("Availability `to` = %v, want now+7d %v", f.cal.gotTo, wantTo)
	}
}

// TestAIProposeEventInvalidSlotFallsBackToFirst covers both flavors of a bad
// preferredSlot -- unparseable text and a syntactically valid RFC3339
// timestamp that just isn't one of the offered slots -- falling back to the
// first offered slot rather than erroring (proposal honesty: we never invent
// a time the model didn't actually validate against real availability).
func TestAIProposeEventInvalidSlotFallsBackToFirst(t *testing.T) {
	tests := []struct {
		name          string
		preferredSlot string
	}{
		{"unparseable text", "next Tuesday afternoon"},
		{"valid RFC3339 but not an offered slot", "2099-01-01T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAIProposeFixture(t)
			f.seedThread(t, "owner@example.com", "guest@example.com")

			slot1 := f.clock.Now().Add(24 * time.Hour)
			f.cal.availRet = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
			f.ai.jsonOut = `{"title":"Sync","attendeeEmails":[],"durationMinutes":30,"preferredSlot":"` + tt.preferredSlot + `"}`

			got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
			if err != nil {
				t.Fatalf("ProposeEvent() error = %v, want nil", err)
			}
			if !got.Start.Equal(slot1) {
				t.Fatalf("Start = %v, want fallback to first offered slot %v", got.Start, slot1)
			}
		})
	}
}

// TestAIProposeEventDropsAttendeeNotInThread covers the honesty rule for
// attendees: an email the model proposes that never appeared as a thread
// participant is dropped, while the owning account is always included.
func TestAIProposeEventDropsAttendeeNotInThread(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	f.cal.availRet = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
	f.ai.jsonOut = `{"title":"Sync","attendeeEmails":["guest@example.com","stranger@example.com"],"durationMinutes":30,"preferredSlot":"` +
		slot1.Format(time.RFC3339) + `"}`

	got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("ProposeEvent() error = %v, want nil", err)
	}
	wantAttendees := []string{"owner@example.com", "guest@example.com"}
	if !reflect.DeepEqual(got.Attendees, wantAttendees) {
		t.Fatalf("Attendees = %v, want %v (stranger@example.com dropped)", got.Attendees, wantAttendees)
	}
}

// TestAIProposeEventNoAvailabilityIsConflict covers the "no free slots in the
// next 7 days" case: a validation-style domain.ErrConflict, not a 500 and not
// a silently invented time.
func TestAIProposeEventNoAvailabilityIsConflict(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")
	f.cal.availRet = nil // no free slots

	_, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want wrapping domain.ErrConflict", err)
	}
	if err.Error() != "conflict: no free slots" {
		t.Fatalf("err message = %q, want %q", err.Error(), "conflict: no free slots")
	}
	if f.ai.lastSystem != "" {
		t.Fatalf("AI was invoked (lastSystem=%q), want it skipped when there's nothing to propose against", f.ai.lastSystem)
	}
}

// TestAIProposeEventMalformedModelOutputIsRejected is the proposal-honesty
// regression test: when the model's JSON doesn't decode into
// eventProposalOut, ProposeEvent must reject it with domain.ErrAIOutput
// (returned raw, per the LLM error-mapping contract) rather than passing any
// partial/garbage output through as a proposal.
func TestAIProposeEventMalformedModelOutputIsRejected(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	f.cal.availRet = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
	f.ai.jsonOut = `not json` // fakeAI.CompleteJSON wraps the decode failure in domain.ErrAIOutput

	_, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if !errors.Is(err, domain.ErrAIOutput) {
		t.Fatalf("err = %v, want wrapping domain.ErrAIOutput", err)
	}
}

// TestAIProposeEventClampsDuration covers the [15, 480]-minute clamp
// (default 30 when the model omits it) so a bogus/absent durationMinutes
// never produces a degenerate or unbounded event.
func TestAIProposeEventClampsDuration(t *testing.T) {
	tests := []struct {
		name        string
		modelMins   int
		wantMinutes time.Duration
	}{
		{"unset defaults to 30", 0, 30 * time.Minute},
		{"below floor clamps to 15", 5, 15 * time.Minute},
		{"above ceiling clamps to 480", 1000, 480 * time.Minute},
		{"within range passes through", 60, 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAIProposeFixture(t)
			f.seedThread(t, "owner@example.com", "guest@example.com")

			slot1 := f.clock.Now().Add(24 * time.Hour)
			f.cal.availRet = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
			f.ai.jsonOut = fmt.Sprintf(`{"title":"Sync","attendeeEmails":[],"durationMinutes":%d,"preferredSlot":%q}`,
				tt.modelMins, slot1.Format(time.RFC3339))

			got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
			if err != nil {
				t.Fatalf("ProposeEvent() error = %v, want nil", err)
			}
			if got.End.Sub(got.Start) != tt.wantMinutes {
				t.Fatalf("duration = %v, want %v", got.End.Sub(got.Start), tt.wantMinutes)
			}
		})
	}
}
