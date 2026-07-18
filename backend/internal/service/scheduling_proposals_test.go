package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- SchedulingService (propose-new-time) fixture (Task 10) ------------------

// proposalFixture wires a SchedulingService with two Calendium users: u1 (the
// organizer, account a1, email organizer@x.com, owning writable calendar
// cal1) and u2 (an invitee, account a2, email bob@example.com). evt1 is
// already mirrored on cal1 with both addresses as attendees (organizer@x.com
// as organizer, bob@example.com as a plain attendee) and a live
// ProviderEventID so provider write-through fires on Accept.
type proposalFixture struct {
	svc       *SchedulingService
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	proposals *fakeProposalRepo
	settings  *fakeUserSettingsRepo
	users     *fakeUserRepo
	calProv   *fakeCalendarProvider
	mailProv  *fakeMailProvider
	clock     *fakeClock
	base      time.Time
}

func newProposalFixture(t *testing.T) *proposalFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "organizer@x.com",
	}); err != nil {
		t.Fatalf("seed organizer account: %v", err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "organizer-token", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed organizer tokens: %v", err)
	}
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a2", UserID: "u2", Provider: domain.ProviderGoogle, Email: "bob@example.com",
	}); err != nil {
		t.Fatalf("seed proposer account: %v", err)
	}
	if err := accounts.SaveTokens(ctx, "a2", port.TokenSet{
		AccessToken: "proposer-token", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed proposer tokens: %v", err)
	}

	calendars := newCalendarRepo()
	calendars.byID["cal1"] = domain.Calendar{
		ID: "cal1", AccountID: "a1", ProviderCalendarID: "prov-cal-1",
		Name: "Work", TimeZone: "America/New_York", CanWrite: true, IsVisible: true,
	}

	events := newEventRepo()
	if _, err := events.Upsert(ctx, domain.Event{
		ID: "evt1", CalendarID: "cal1", ProviderEventID: "prov-evt-1",
		Title: "Sync", Start: base.Add(24 * time.Hour), End: base.Add(24*time.Hour + 30*time.Minute),
		Status: domain.EventConfirmed,
		Attendees: []domain.Attendee{
			{Email: "organizer@x.com", Organizer: true},
			{Email: "bob@example.com"},
		},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	proposals := newProposalRepo()
	settings := newUserSettingsRepo()
	users := newUserRepo()
	if _, err := users.Upsert(ctx, domain.User{ID: "u1", Email: "organizer@x.com", Name: ptr("Organizer")}); err != nil {
		t.Fatalf("seed organizer user: %v", err)
	}
	if _, err := users.Upsert(ctx, domain.User{ID: "u2", Email: "bob@example.com", Name: ptr("Bob")}); err != nil {
		t.Fatalf("seed proposer user: %v", err)
	}

	calProv := newCalendarProvider()
	mailProv := newMailProvider()

	svc := NewSchedulingService(SchedulingServiceDeps{
		Subscriptions:     newSubscriptionRepo(),
		Users:             users,
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Proposals:         proposals,
		Settings:          settings,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: calProv},
		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mailProv},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})
	return &proposalFixture{svc, accounts, calendars, events, proposals, settings, users, calProv, mailProv, clock, base}
}

func validProposalInput(base time.Time) port.TimeProposalInput {
	return port.TimeProposalInput{
		Start: base.Add(48 * time.Hour),
		End:   base.Add(48*time.Hour + 30*time.Minute),
		Note:  "does this work better?",
	}
}

// --- ProposeTime ---------------------------------------------------------------

func TestProposalPropose_NonAttendeeIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a3", UserID: "u3", Provider: domain.ProviderGoogle, Email: "stranger@example.com",
	}); err != nil {
		t.Fatalf("seed stranger account: %v", err)
	}

	_, err := f.svc.ProposeTime(ctx, "u3", "evt1", validProposalInput(f.base))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestProposalPropose_OrganizerCannotProposeOwnEvent(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)

	_, err := f.svc.ProposeTime(ctx, "u1", "evt1", validProposalInput(f.base))
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestProposalPropose_EndBeforeStartIsValidation(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	in := validProposalInput(f.base)
	in.End = in.Start.Add(-time.Minute)

	_, err := f.svc.ProposeTime(ctx, "u2", "evt1", in)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestProposalPropose_UnknownEventIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)

	_, err := f.svc.ProposeTime(ctx, "u2", "missing-event", validProposalInput(f.base))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestProposalPropose_Success(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	in := validProposalInput(f.base)

	got, err := f.svc.ProposeTime(ctx, "u2", "evt1", in)
	if err != nil {
		t.Fatalf("ProposeTime: %v", err)
	}
	if got.ID == "" {
		t.Fatal("ProposeTime did not assign an id")
	}
	if got.EventID != "evt1" || got.ProposerEmail != "bob@example.com" || got.ProposerName != "Bob" {
		t.Fatalf("proposal = %+v, want EventID evt1, ProposerEmail bob@example.com, ProposerName Bob", got)
	}
	if got.Status != domain.ProposalPending {
		t.Fatalf("Status = %q, want pending", got.Status)
	}
	if !got.Start.Equal(in.Start) || !got.End.Equal(in.End) {
		t.Fatalf("Start/End = %v/%v, want %v/%v", got.Start, got.End, in.Start, in.End)
	}
	if got.Note == nil || *got.Note != in.Note {
		t.Fatalf("Note = %v, want %q", got.Note, in.Note)
	}

	stored, err := f.proposals.GetByID(ctx, got.ID)
	if err != nil || stored.Status != domain.ProposalPending {
		t.Fatalf("stored proposal = %+v, %v; want persisted pending", stored, err)
	}

	if len(f.mailProv.sent) != 1 {
		t.Fatalf("len(sent) = %d, want 1 notification email", len(f.mailProv.sent))
	}
	msg := f.mailProv.sent[0]
	if msg.From.Email != "bob@example.com" {
		t.Fatalf("From = %+v, want proposer's own account (best-effort through the proposer's account)", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0].Email != "organizer@x.com" {
		t.Fatalf("To = %+v, want organizer@x.com", msg.To)
	}
	if msg.Subject != "New time proposed: Sync" {
		t.Fatalf("Subject = %q, want %q", msg.Subject, "New time proposed: Sync")
	}
	// Body must show both the old and new time windows in both parties'
	// zones (organizer's calendar zone America/New_York, proposer's default
	// UTC settings zone).
	for _, want := range []string{"America/New_York", "UTC", in.Note} {
		if !strings.Contains(msg.BodyText, want) {
			t.Fatalf("body does not contain %q:\n%s", want, msg.BodyText)
		}
	}
}

func TestProposalPropose_MailSendFailureDoesNotFailRequest(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	f.mailProv.sendErr = errors.New("smtp down")

	got, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base))
	if err != nil {
		t.Fatalf("ProposeTime: %v, want success despite mail failure (best-effort)", err)
	}
	if got.Status != domain.ProposalPending {
		t.Fatalf("Status = %q, want pending even though the notification failed", got.Status)
	}
}

// --- ListProposals ---------------------------------------------------------------

func TestProposalList_NonOwnerIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	if _, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base)); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	if _, err := f.svc.ListProposals(ctx, "u2", "evt1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (u2 is an invitee, not the organizer)", err)
	}
}

func TestProposalList_OwnerSeesProposals(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	if _, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base)); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	got, err := f.svc.ListProposals(ctx, "u1", "evt1")
	if err != nil {
		t.Fatalf("ListProposals: %v", err)
	}
	if len(got) != 1 || got[0].ProposerEmail != "bob@example.com" {
		t.Fatalf("ListProposals = %+v, want 1 proposal from bob@example.com", got)
	}
}

// --- AcceptProposal ---------------------------------------------------------------

func TestProposalAccept_PatchesEventAndSupersedesSiblings(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	in := validProposalInput(f.base)

	winner, err := f.svc.ProposeTime(ctx, "u2", "evt1", in)
	if err != nil {
		t.Fatalf("seed winning proposal: %v", err)
	}
	sibling, err := f.svc.ProposeTime(ctx, "u2", "evt1", port.TimeProposalInput{
		Start: in.Start.Add(3 * time.Hour), End: in.End.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("seed sibling proposal: %v", err)
	}

	f.calProv.updatedEvent = domain.Event{
		ID: "evt1", CalendarID: "cal1", ProviderEventID: "prov-evt-1",
		Title: "Sync", Start: winner.Start, End: winner.End,
		Status: domain.EventConfirmed,
		Attendees: []domain.Attendee{
			{Email: "organizer@x.com", Organizer: true},
			{Email: "bob@example.com"},
		},
	}

	sentBeforeAccept := len(f.mailProv.sent)

	got, err := f.svc.AcceptProposal(ctx, "u1", "evt1", winner.ID)
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	if !got.Start.Equal(winner.Start) || !got.End.Equal(winner.End) {
		t.Fatalf("returned event Start/End = %v/%v, want %v/%v", got.Start, got.End, winner.Start, winner.End)
	}

	if f.calProv.lastUpdateEventID != "prov-evt-1" {
		t.Fatalf("lastUpdateEventID = %q, want prov-evt-1", f.calProv.lastUpdateEventID)
	}
	if f.calProv.lastUpdatePatch.Start == nil || !f.calProv.lastUpdatePatch.Start.Equal(winner.Start) ||
		f.calProv.lastUpdatePatch.End == nil || !f.calProv.lastUpdatePatch.End.Equal(winner.End) {
		t.Fatalf("lastUpdatePatch = %+v, want Start/End = winner's proposed window", f.calProv.lastUpdatePatch)
	}

	mirrored, err := f.events.GetByID(ctx, "evt1")
	if err != nil || !mirrored.Start.Equal(winner.Start) || !mirrored.End.Equal(winner.End) {
		t.Fatalf("mirrored event = %+v, %v; want patched to winner's window", mirrored, err)
	}

	acceptedProposal, err := f.proposals.GetByID(ctx, winner.ID)
	if err != nil || acceptedProposal.Status != domain.ProposalAccepted {
		t.Fatalf("accepted proposal = %+v, %v; want Status accepted", acceptedProposal, err)
	}
	supersededProposal, err := f.proposals.GetByID(ctx, sibling.ID)
	if err != nil || supersededProposal.Status != domain.ProposalSuperseded {
		t.Fatalf("sibling proposal = %+v, %v; want Status superseded", supersededProposal, err)
	}

	// No extra Calendium-sent email for Accept — the provider's own
	// attendee notifications carry the change to guests.
	if len(f.mailProv.sent) != sentBeforeAccept {
		t.Fatalf("len(sent) = %d, want %d unchanged (provider write-through notifies guests, not Calendium)", len(f.mailProv.sent), sentBeforeAccept)
	}
}

func TestProposalAccept_NonPendingIsConflict(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	p, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base))
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	if _, err := f.svc.AcceptProposal(ctx, "u1", "evt1", p.ID); err != nil {
		t.Fatalf("first AcceptProposal: %v", err)
	}

	if _, err := f.svc.AcceptProposal(ctx, "u1", "evt1", p.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict on an already-accepted proposal", err)
	}
}

func TestProposalAccept_NonOrganizerIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	p, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base))
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	if _, err := f.svc.AcceptProposal(ctx, "u2", "evt1", p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (u2 does not own evt1's calendar)", err)
	}
}

// --- DeclineProposal ---------------------------------------------------------------

func TestProposalDecline_LeavesEventUntouched(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	p, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base))
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	before, err := f.events.GetByID(ctx, "evt1")
	if err != nil {
		t.Fatalf("GetByID before decline: %v", err)
	}

	if err := f.svc.DeclineProposal(ctx, "u1", "evt1", p.ID); err != nil {
		t.Fatalf("DeclineProposal: %v", err)
	}

	after, err := f.events.GetByID(ctx, "evt1")
	if err != nil || !after.Start.Equal(before.Start) || !after.End.Equal(before.End) {
		t.Fatalf("event changed by DeclineProposal: before=%+v after=%+v (%v)", before, after, err)
	}
	if f.calProv.lastUpdateEventID != "" {
		t.Fatalf("lastUpdateEventID = %q, want empty (decline must not touch the provider)", f.calProv.lastUpdateEventID)
	}

	declined, err := f.proposals.GetByID(ctx, p.ID)
	if err != nil || declined.Status != domain.ProposalDeclined {
		t.Fatalf("declined proposal = %+v, %v; want Status declined", declined, err)
	}
}

func TestProposalDecline_NonPendingIsConflict(t *testing.T) {
	ctx := context.Background()
	f := newProposalFixture(t)
	p, err := f.svc.ProposeTime(ctx, "u2", "evt1", validProposalInput(f.base))
	if err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	if err := f.svc.DeclineProposal(ctx, "u1", "evt1", p.ID); err != nil {
		t.Fatalf("first DeclineProposal: %v", err)
	}

	if err := f.svc.DeclineProposal(ctx, "u1", "evt1", p.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict on an already-declined proposal", err)
	}
}
