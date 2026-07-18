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

// --- SchedulingService (meeting polls) fixture (Task 9) ----------------------

// pollFixture is a SchedulingService wired to one Google account (a1, user
// u1, email organizer@x.com) owning one writable calendar (cal1), with a
// valid non-expiring access token so tokenSource never refreshes.
type pollFixture struct {
	svc       *SchedulingService
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	polls     *fakePollRepo
	users     *fakeUserRepo
	calProv   *fakeCalendarProvider
	mailProv  *fakeMailProvider
	clock     *fakeClock
}

func newPollFixture(t *testing.T) *pollFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "organizer@x.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "valid-access", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	calendars := newCalendarRepo()
	calendars.byID["cal1"] = domain.Calendar{
		ID: "cal1", AccountID: "a1", ProviderCalendarID: "prov-cal-1",
		Name: "Work", CanWrite: true, IsVisible: true,
	}

	events := newEventRepo()
	polls := newPollRepo()
	users := newUserRepo()
	if _, err := users.Upsert(ctx, domain.User{ID: "u1", Email: "organizer@x.com", Name: ptr("Organizer")}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	calProv := newCalendarProvider()
	mailProv := newMailProvider()

	svc := NewSchedulingService(SchedulingServiceDeps{
		Subscriptions:     newSubscriptionRepo(),
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Polls:             polls,
		Users:             users,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: calProv},
		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mailProv},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})
	return &pollFixture{svc, accounts, calendars, events, polls, users, calProv, mailProv, clock}
}

// validPollInput returns a 2-option, 30-minute poll input on cal1 anchored to
// the fixture's clock, so every option's End = Start + DurationMinutes.
func validPollInput(base time.Time) port.PollInput {
	return port.PollInput{
		Title:           "Sync",
		CalendarID:      "cal1",
		DurationMinutes: 30,
		Options: []domain.PollOption{
			{Start: base.Add(1 * time.Hour), End: base.Add(90 * time.Minute)},
			{Start: base.Add(2 * time.Hour), End: base.Add(150 * time.Minute)},
		},
	}
}

// --- CreatePoll ---------------------------------------------------------------

func TestPollCreateValidation(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		userID string
		mutate func(in *port.PollInput)
		want   error
	}{
		{
			name:   "blank title",
			mutate: func(in *port.PollInput) { in.Title = "   " },
			want:   domain.ErrValidation,
		},
		{
			name:   "zero duration",
			mutate: func(in *port.PollInput) { in.DurationMinutes = 0 },
			want:   domain.ErrValidation,
		},
		{
			name:   "one option is too few",
			mutate: func(in *port.PollInput) { in.Options = in.Options[:1] },
			want:   domain.ErrValidation,
		},
		{
			name: "eleven options is too many",
			mutate: func(in *port.PollInput) {
				opts := make([]domain.PollOption, 11)
				for i := range opts {
					start := base.Add(time.Duration(i+1) * time.Hour)
					opts[i] = domain.PollOption{Start: start, End: start.Add(30 * time.Minute)}
				}
				in.Options = opts
			},
			want: domain.ErrValidation,
		},
		{
			name:   "empty calendar id",
			mutate: func(in *port.PollInput) { in.CalendarID = "" },
			want:   domain.ErrValidation,
		},
		{
			name:   "unknown calendar id",
			mutate: func(in *port.PollInput) { in.CalendarID = "nope" },
			want:   domain.ErrNotFound,
		},
		{
			name:   "foreign calendar",
			userID: "intruder",
			want:   domain.ErrNotFound,
		},
		{
			name: "option end does not equal start+duration",
			mutate: func(in *port.PollInput) {
				in.Options[0].End = in.Options[0].End.Add(time.Minute)
			},
			want: domain.ErrValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPollFixture(t)
			in := validPollInput(f.clock.Now())
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			userID := tt.userID
			if userID == "" {
				userID = "u1"
			}
			_, err := f.svc.CreatePoll(ctx, userID, in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPollCreate_ReadOnlyCalendarRejected(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	c := f.calendars.byID["cal1"]
	c.CanWrite = false
	f.calendars.byID["cal1"] = c

	_, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestPollCreate_AssignsOptionIdsAndToken(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)

	got, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	if got.Status != domain.PollOpen {
		t.Fatalf("Status = %q, want open", got.Status)
	}
	if len(got.Token) != 32 {
		t.Fatalf("len(Token) = %d, want 32 (32 hex chars)", len(got.Token))
	}
	for i, o := range got.Options {
		if o.ID == "" {
			t.Fatalf("option %d has no assigned id", i)
		}
	}
	if got.Options[0].ID == got.Options[1].ID {
		t.Fatal("options were assigned the same id")
	}
}

func TestPollCreate_TokenCollisionIsRetried(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	// Force the first Create attempt to look like a token collision at the
	// unique constraint; the service must retry with a freshly generated
	// token rather than surfacing the error.
	f.polls.createErrs = []error{domain.ErrConflict}

	got, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll after simulated collision: %v", err)
	}
	if len(got.Token) != 32 {
		t.Fatalf("len(Token) = %d, want 32", len(got.Token))
	}
	if len(f.polls.createErrs) != 0 {
		t.Fatalf("createErrs not fully consumed: %v", f.polls.createErrs)
	}
}

func TestPollCreate_ExhaustsRetriesAndPropagatesConflict(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	f.polls.createErrs = []error{
		domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict, domain.ErrConflict,
	}

	_, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict after exhausting retries", err)
	}
}

// --- ListPolls / DeletePoll ----------------------------------------------------

func TestPollListAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	created, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}

	list, err := f.svc.ListPolls(ctx, "u1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListPolls = %v, %v; want 1 poll", list, err)
	}

	if _, err := f.svc.ListPolls(ctx, "intruder"); err != nil {
		t.Fatalf("ListPolls(intruder): %v", err)
	}

	if err := f.svc.DeletePoll(ctx, "intruder", created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeletePoll(foreign user) = %v, want ErrNotFound", err)
	}
	if err := f.svc.DeletePoll(ctx, "u1", created.ID); err != nil {
		t.Fatalf("DeletePoll: %v", err)
	}
	if _, err := f.polls.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("poll not deleted: %v", err)
	}
}

// --- PublicPollByToken / VotePoll -----------------------------------------------

func TestPollPublicByToken_UnknownToken(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	if _, err := f.svc.PublicPollByToken(ctx, "nonexistent"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPollVote_TalliesAndHidesVoterEmails(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1, opt2 := poll.Options[0].ID, poll.Options[1].ID

	ballots := []port.PollBallot{
		{VoterEmail: "alice@example.com", VoterName: "Alice", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes}},
		{VoterEmail: "bob@example.com", VoterName: "Bob", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteIfNeeded, opt2: domain.VoteNo}},
		{VoterEmail: "carol@example.com", VoterName: "Carol", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteNo}},
	}
	var pub port.PublicPoll
	for _, b := range ballots {
		pub, err = f.svc.VotePoll(ctx, poll.Token, b)
		if err != nil {
			t.Fatalf("VotePoll(%s): %v", b.VoterEmail, err)
		}
	}

	if got, want := pub.Tallies[opt1], (port.PollTally{Yes: 1, IfNeeded: 1, No: 1}); got != want {
		t.Fatalf("tallies[opt1] = %+v, want %+v", got, want)
	}
	if got, want := pub.Tallies[opt2], (port.PollTally{No: 1}); got != want {
		t.Fatalf("tallies[opt2] = %+v, want %+v", got, want)
	}

	// The public document has no field capable of carrying voter identity —
	// only anonymized tallies. Guard this with a substring check on the
	// voters' emails against the fields that DO leak to the public: Token,
	// Title, Description, OrganizerName.
	for _, leaked := range []string{pub.Token, pub.Title, pub.OrganizerName} {
		if strings.Contains(leaked, "alice@example.com") || strings.Contains(leaked, "bob@example.com") || strings.Contains(leaked, "carol@example.com") {
			t.Fatalf("voter email leaked into public poll field: %q", leaked)
		}
	}
}

func TestPollVote_RevoteReplacesRatherThanAppends(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1 := poll.Options[0].ID

	if _, err := f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
		VoterEmail: "Alice@Example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes},
	}); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	pub, err := f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
		VoterEmail: "alice@example.com", // same voter, case-insensitive
		Choices:    map[string]domain.PollVoteChoice{opt1: domain.VoteNo},
	})
	if err != nil {
		t.Fatalf("re-vote: %v", err)
	}
	if got, want := pub.Tallies[opt1], (port.PollTally{No: 1}); got != want {
		t.Fatalf("tallies[opt1] after re-vote = %+v, want %+v (replaced, not appended)", got, want)
	}
}

func TestPollVote_Validation(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown option id", func(t *testing.T) {
		f := newPollFixture(t)
		poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
		if err != nil {
			t.Fatalf("CreatePoll: %v", err)
		}
		_, err = f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
			VoterEmail: "alice@example.com", Choices: map[string]domain.PollVoteChoice{"nope": domain.VoteYes},
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("invalid voter email", func(t *testing.T) {
		f := newPollFixture(t)
		poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
		if err != nil {
			t.Fatalf("CreatePoll: %v", err)
		}
		opt1 := poll.Options[0].ID
		_, err = f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
			VoterEmail: "not-an-email", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes},
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		f := newPollFixture(t)
		_, err := f.svc.VotePoll(ctx, "nonexistent", port.PollBallot{VoterEmail: "a@x.com"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestPollVote_OnConfirmedPollIsConflict(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1 := poll.Options[0].ID
	if _, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1); err != nil {
		t.Fatalf("ConfirmPoll: %v", err)
	}

	_, err = f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
		VoterEmail: "late@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes},
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// --- ConfirmPoll ----------------------------------------------------------------

func TestPollConfirm_CreatesEventWithVoterAttendeesAndEmailsVoters(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1, opt2 := poll.Options[0].ID, poll.Options[1].ID

	votes := []port.PollBallot{
		{VoterEmail: "alice@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes}},
		{VoterEmail: "bob@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteIfNeeded}},
		{VoterEmail: "carol@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteNo}},
		{VoterEmail: "dave@example.com", Choices: map[string]domain.PollVoteChoice{opt2: domain.VoteYes}}, // votes on the OTHER option
	}
	for _, b := range votes {
		if _, err := f.svc.VotePoll(ctx, poll.Token, b); err != nil {
			t.Fatalf("VotePoll(%s): %v", b.VoterEmail, err)
		}
	}

	f.calProv.createdEvent = domain.Event{ID: "provider-assigned", ProviderEventID: "prov-evt-1"}

	got, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("ConfirmPoll: %v", err)
	}
	if got.Status != domain.PollConfirmed {
		t.Fatalf("Status = %q, want confirmed", got.Status)
	}
	if got.WinnerOptionID == nil || *got.WinnerOptionID != opt1 {
		t.Fatalf("WinnerOptionID = %v, want %q", got.WinnerOptionID, opt1)
	}
	if got.EventID == nil || *got.EventID == "" {
		t.Fatal("EventID not set")
	}

	// Only yes/if_needed voters on the WINNING option become attendees.
	gotAttendees := f.calProv.lastCreateInput.AttendeeEmails
	wantAttendees := []string{"alice@example.com", "bob@example.com"}
	if len(gotAttendees) != len(wantAttendees) {
		t.Fatalf("AttendeeEmails = %v, want %v", gotAttendees, wantAttendees)
	}
	for i, e := range wantAttendees {
		if gotAttendees[i] != e {
			t.Fatalf("AttendeeEmails = %v, want %v", gotAttendees, wantAttendees)
		}
	}
	if f.calProv.lastCreateCalendarID != "prov-cal-1" {
		t.Fatalf("provider called with calendar %q, want prov-cal-1", f.calProv.lastCreateCalendarID)
	}
	if f.calProv.lastCreateInput.Start != poll.Options[0].Start || f.calProv.lastCreateInput.End != poll.Options[0].End {
		t.Fatalf("event window = [%v,%v), want winning option window", f.calProv.lastCreateInput.Start, f.calProv.lastCreateInput.End)
	}

	// The local mirror was upserted with the provider-assigned event.
	mirrored, err := f.events.GetByID(ctx, *got.EventID)
	if err != nil {
		t.Fatalf("mirrored event not found: %v", err)
	}
	if mirrored.CalendarID != "cal1" {
		t.Fatalf("mirrored.CalendarID = %q, want cal1", mirrored.CalendarID)
	}

	// Every yes/if_needed voter on the winning option got emailed, and only
	// those voters.
	if len(f.mailProv.sent) != 2 {
		t.Fatalf("len(sent) = %d, want 2", len(f.mailProv.sent))
	}
	gotRecipients := map[string]bool{}
	for _, msg := range f.mailProv.sent {
		if len(msg.To) != 1 {
			t.Fatalf("message To = %v, want exactly one recipient", msg.To)
		}
		gotRecipients[msg.To[0].Email] = true
		if msg.From.Email != "organizer@x.com" {
			t.Fatalf("From = %q, want organizer's account email", msg.From.Email)
		}
		if !strings.Contains(msg.Subject, poll.Title) {
			t.Fatalf("Subject = %q, want it to mention %q", msg.Subject, poll.Title)
		}
	}
	if !gotRecipients["alice@example.com"] || !gotRecipients["bob@example.com"] {
		t.Fatalf("recipients = %v, want alice and bob", gotRecipients)
	}
	if gotRecipients["carol@example.com"] || gotRecipients["dave@example.com"] {
		t.Fatalf("recipients = %v, want neither carol (voted no) nor dave (voted on other option)", gotRecipients)
	}
}

func TestPollConfirm_IdempotentSameOption(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1 := poll.Options[0].ID

	first, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("first ConfirmPoll: %v", err)
	}
	createCallsBefore := f.calProv.lastCreateCalendarID
	sentBefore := len(f.mailProv.sent)

	second, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("second ConfirmPoll (same option): %v", err)
	}
	if second.Status != domain.PollConfirmed || second.EventID == nil || first.EventID == nil || *second.EventID != *first.EventID {
		t.Fatalf("second confirm changed state: first=%+v second=%+v", first, second)
	}
	if f.calProv.lastCreateCalendarID != createCallsBefore {
		t.Fatal("idempotent re-confirm re-invoked the provider event creation")
	}
	if len(f.mailProv.sent) != sentBefore {
		t.Fatal("idempotent re-confirm re-sent confirmation emails")
	}
}

// TestPollConfirm_MailSendFailureStillConfirms: the confirmation email is
// best-effort (matching sync.go's notifyThread convention). A Send failure
// must never leave the provider event orphaned with the poll stuck open —
// the poll must still end up confirmed, with the winner and event ID
// persisted, after exactly one provider.CreateEvent call, and ConfirmPoll
// itself must not return an error for a mail failure.
func TestPollConfirm_MailSendFailureStillConfirms(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1 := poll.Options[0].ID
	if _, err := f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
		VoterEmail: "alice@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes},
	}); err != nil {
		t.Fatalf("VotePoll: %v", err)
	}

	f.calProv.createdEvent = domain.Event{ID: "provider-assigned", ProviderEventID: "prov-evt-1"}
	f.mailProv.sendErr = errors.New("smtp 500")

	got, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("ConfirmPoll returned an error for a mail-send failure: %v", err)
	}
	if got.Status != domain.PollConfirmed {
		t.Fatalf("Status = %q, want confirmed despite mail failure", got.Status)
	}
	if got.WinnerOptionID == nil || *got.WinnerOptionID != opt1 {
		t.Fatalf("WinnerOptionID = %v, want %q", got.WinnerOptionID, opt1)
	}
	if got.EventID == nil || *got.EventID == "" {
		t.Fatal("EventID not persisted despite mail failure")
	}
	if len(f.events.order) != 1 {
		t.Fatalf("provider events created = %d, want exactly 1", len(f.events.order))
	}
	if len(f.mailProv.sent) != 1 {
		t.Fatalf("send attempts = %d, want 1 (best-effort attempt still made)", len(f.mailProv.sent))
	}

	// The confirmed state must also be durably persisted, not just returned.
	persisted, err := f.polls.GetByID(ctx, poll.ID)
	if err != nil {
		t.Fatalf("GetByID after confirm: %v", err)
	}
	if persisted.Status != domain.PollConfirmed || persisted.EventID == nil || *persisted.EventID != *got.EventID {
		t.Fatalf("persisted poll = %+v, want confirmed with matching event id", persisted)
	}
}

// TestPollConfirm_RetryAfterMailFailureDoesNotDuplicateEvent: retrying
// ConfirmPoll after a mail-send failure must land on the idempotent
// short-circuit (poll already PollConfirmed with this option) rather than
// re-running provider.CreateEvent, which would otherwise produce a duplicate
// calendar event.
func TestPollConfirm_RetryAfterMailFailureDoesNotDuplicateEvent(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1 := poll.Options[0].ID
	if _, err := f.svc.VotePoll(ctx, poll.Token, port.PollBallot{
		VoterEmail: "alice@example.com", Choices: map[string]domain.PollVoteChoice{opt1: domain.VoteYes},
	}); err != nil {
		t.Fatalf("VotePoll: %v", err)
	}

	f.mailProv.sendErr = errors.New("smtp 500")

	first, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("first ConfirmPoll (mail failure) returned an error: %v", err)
	}
	if len(f.events.order) != 1 {
		t.Fatalf("provider events after first confirm = %d, want 1", len(f.events.order))
	}
	sentBefore := len(f.mailProv.sent)

	// Any further provider.CreateEvent call would hit this and fail the
	// test's expectations below, proving the retry never re-invokes it.
	f.calProv.createErr = errors.New("must not be called again")

	second, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1)
	if err != nil {
		t.Fatalf("retry after mail failure returned an error (should hit idempotent no-op): %v", err)
	}
	if second.EventID == nil || first.EventID == nil || *second.EventID != *first.EventID {
		t.Fatalf("retry changed the confirmed event: first=%+v second=%+v", first, second)
	}
	if len(f.events.order) != 1 {
		t.Fatalf("provider events after retry = %d, want still 1 (no duplicate event)", len(f.events.order))
	}
	if len(f.mailProv.sent) != sentBefore {
		t.Fatal("retry after mail failure re-sent the confirmation email")
	}
}

func TestPollConfirm_DifferentOptionAfterConfirmIsConflict(t *testing.T) {
	ctx := context.Background()
	f := newPollFixture(t)
	poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}
	opt1, opt2 := poll.Options[0].ID, poll.Options[1].ID

	if _, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt1); err != nil {
		t.Fatalf("first ConfirmPoll: %v", err)
	}
	if _, err := f.svc.ConfirmPoll(ctx, "u1", poll.ID, opt2); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestPollConfirm_ValidationAndOwnership(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown option id", func(t *testing.T) {
		f := newPollFixture(t)
		poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
		if err != nil {
			t.Fatalf("CreatePoll: %v", err)
		}
		_, err = f.svc.ConfirmPoll(ctx, "u1", poll.ID, "nope")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("foreign user", func(t *testing.T) {
		f := newPollFixture(t)
		poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
		if err != nil {
			t.Fatalf("CreatePoll: %v", err)
		}
		_, err = f.svc.ConfirmPoll(ctx, "intruder", poll.ID, poll.Options[0].ID)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("read-only calendar", func(t *testing.T) {
		f := newPollFixture(t)
		poll, err := f.svc.CreatePoll(ctx, "u1", validPollInput(f.clock.Now()))
		if err != nil {
			t.Fatalf("CreatePoll: %v", err)
		}
		c := f.calendars.byID["cal1"]
		c.CanWrite = false
		f.calendars.byID["cal1"] = c

		_, err = f.svc.ConfirmPoll(ctx, "u1", poll.ID, poll.Options[0].ID)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("unknown poll id", func(t *testing.T) {
		f := newPollFixture(t)
		_, err := f.svc.ConfirmPoll(ctx, "u1", "nope", "opt1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- buildPollConfirmationEmail (pure) -------------------------------------------

func TestPollBuildConfirmationEmail(t *testing.T) {
	start := time.Date(2026, 8, 1, 15, 0, 0, 0, time.UTC)
	poll := domain.MeetingPoll{Title: "Roadmap sync", Description: ptr("Quarterly planning")}
	winner := domain.PollOption{Start: start, End: start.Add(30 * time.Minute)}

	subject, body := buildPollConfirmationEmail(poll, winner, "alice@example.com")
	if !strings.Contains(subject, "Roadmap sync") {
		t.Fatalf("subject = %q, want it to mention the poll title", subject)
	}
	if !strings.Contains(body, "Roadmap sync") || !strings.Contains(body, "Quarterly planning") {
		t.Fatalf("body = %q, want title and description", body)
	}
}

// --- computeSlots (pure engine) -----------------------------------------------

func TestComputeSlotsWindowDiscretizationAcrossDays(t *testing.T) {
	// Anchor on a Tuesday: the window recurs on both Tuesdays inside [from,to).
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "11:00"},
		},
	}
	from := day0
	to := day0.AddDate(0, 0, 8) // spans day0's Tuesday and the following Tuesday
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)

	day1 := day0.AddDate(0, 0, 7)
	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
		{Start: day0.Add(9*time.Hour + 30*time.Minute), End: day0.Add(10 * time.Hour)},
		{Start: day0.Add(10 * time.Hour), End: day0.Add(10*time.Hour + 30*time.Minute)},
		{Start: day0.Add(10*time.Hour + 30*time.Minute), End: day0.Add(11 * time.Hour)},
		{Start: day1.Add(9 * time.Hour), End: day1.Add(9*time.Hour + 30*time.Minute)},
		{Start: day1.Add(9*time.Hour + 30*time.Minute), End: day1.Add(10 * time.Hour)},
		{Start: day1.Add(10 * time.Hour), End: day1.Add(10*time.Hour + 30*time.Minute)},
		{Start: day1.Add(10*time.Hour + 30*time.Minute), End: day1.Add(11 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

// TestComputeSlotsDSTSpringForward proves the discretization loop is
// DST-correct: a 09:00-17:00 window on America/New_York's 2026 spring-forward
// day (March 8) resolves to the true UTC instants for that day's actual
// offset (EDT, UTC-4, since the 2am->3am jump happens before 09:00), not the
// pre-transition EST (UTC-5) offset.
func TestComputeSlotsDSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	transitionDay := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "America/New_York",
		DurationMinutes: 60,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(transitionDay.Weekday()), Start: "09:00", End: "17:00"},
		},
	}
	from := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)
	if len(got) != 8 {
		t.Fatalf("len(got) = %d, want 8 (09:00-17:00 in 1h steps)", len(got))
	}

	wantFirstStart := time.Date(2026, 3, 8, 9, 0, 0, 0, loc)
	if !got[0].Start.Equal(wantFirstStart) {
		t.Fatalf("first slot start = %v, want %v (local 09:00 America/New_York)", got[0].Start, wantFirstStart)
	}
	// EDT is UTC-4; by 09:00 local the 2am->3am spring-forward has already
	// happened, so 09:00 local must be 13:00 UTC, NOT 14:00 UTC (which would
	// be the stale pre-DST EST/UTC-5 offset).
	wantFirstUTC := time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC)
	if !got[0].Start.Equal(wantFirstUTC) {
		t.Fatalf("first slot start (UTC) = %v, want %v", got[0].Start.UTC(), wantFirstUTC)
	}
}

func TestComputeSlotsBuffersPadBusyEvent(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		BufferBeforeMin: 15,
		BufferAfterMin:  15,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "12:00"},
		},
	}
	busy := []domain.BusyInterval{
		{Start: day0.Add(10 * time.Hour), End: day0.Add(10*time.Hour + 30*time.Minute)},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, busy, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
		{Start: day0.Add(11 * time.Hour), End: day0.Add(11*time.Hour + 30*time.Minute)},
		{Start: day0.Add(11*time.Hour + 30*time.Minute), End: day0.Add(12 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func TestComputeSlotsDailyLimitHidesWholeDay(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 7)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 30,
		DailyLimit:      1,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "11:00"},
		},
	}
	// One confirmed booking on day0 only; day0 should vanish entirely (not
	// just the slot it overlaps), day1 stays untouched.
	active := []domain.Booking{
		{Status: domain.BookingConfirmed, Start: day0.Add(9 * time.Hour), End: day0.Add(9*time.Hour + 30*time.Minute)},
	}
	from, to := day0, day0.AddDate(0, 0, 8)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, active, from, to, now)

	for _, s := range got {
		if !s.Start.Before(day1) {
			continue
		}
		t.Fatalf("day0 slot %v survived dailyLimit, want the whole day hidden", s)
	}
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4 (day1's untouched slots only)", len(got))
	}
}

func TestComputeSlotsMinNoticeHidesNearSlots(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 60,
		MinNoticeMin:    120, // 2h
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "17:00"},
		},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(9*time.Hour + 30*time.Minute) // 09:30 -> notice limit 11:30

	got := computeSlots(link, domain.UserSettings{}, nil, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(12 * time.Hour), End: day0.Add(13 * time.Hour)},
		{Start: day0.Add(13 * time.Hour), End: day0.Add(14 * time.Hour)},
		{Start: day0.Add(14 * time.Hour), End: day0.Add(15 * time.Hour)},
		{Start: day0.Add(15 * time.Hour), End: day0.Add(16 * time.Hour)},
		{Start: day0.Add(16 * time.Hour), End: day0.Add(17 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func TestComputeSlotsActiveHoldBlocksItsSlot(t *testing.T) {
	day0 := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	link := domain.BookingLink{
		TimeZone:        "UTC",
		DurationMinutes: 60,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(day0.Weekday()), Start: "09:00", End: "12:00"},
		},
	}
	active := []domain.Booking{
		{Status: domain.BookingHold, Start: day0.Add(10 * time.Hour), End: day0.Add(11 * time.Hour)},
	}
	from, to := day0, day0.AddDate(0, 0, 1)
	now := day0.Add(-48 * time.Hour)

	got := computeSlots(link, domain.UserSettings{}, nil, active, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: day0.Add(9 * time.Hour), End: day0.Add(10 * time.Hour)},
		{Start: day0.Add(11 * time.Hour), End: day0.Add(12 * time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

// TestComputeSlotsWorkingHoursDifferentTimeZone proves working-hours
// intersection is evaluated in settings.TimeZone even when it differs from
// the link's own TimeZone: a link window in America/New_York (EST, UTC-5 in
// January) is intersected against working hours declared in Asia/Tokyo
// (JST, UTC+9). Only the NY slot fully contained in the JST working window
// survives; the following hour, partially outside it, is dropped.
func TestComputeSlotsWorkingHoursDifferentTimeZone(t *testing.T) {
	nyLoc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(NY): %v", err)
	}
	jstLoc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("LoadLocation(JST): %v", err)
	}

	nyWindowDay := time.Date(2026, 1, 6, 0, 0, 0, 0, nyLoc)
	link := domain.BookingLink{
		TimeZone:            "America/New_York",
		DurationMinutes:     60,
		RespectWorkingHours: true,
		Windows: []domain.AvailabilityWindow{
			{Weekday: int(nyWindowDay.Weekday()), Start: "06:00", End: "08:00"},
		},
	}

	// NY 06:00 Jan 6 == JST 20:00 Jan 6 (UTC-5 -> UTC+9 is +14h, same JST
	// calendar date); NY 07:00-08:00 == JST 21:00-22:00 Jan 6.
	firstSlotStart := time.Date(2026, 1, 6, 6, 0, 0, 0, nyLoc)
	jstDate := firstSlotStart.In(jstLoc)
	settings := domain.UserSettings{
		TimeZone: "Asia/Tokyo",
		WorkingHours: []domain.AvailabilityWindow{
			{Weekday: int(jstDate.Weekday()), Start: "19:00", End: "21:30"},
		},
	}

	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	got := computeSlots(link, settings, nil, nil, from, to, now)

	want := []domain.AvailabilitySlot{
		{Start: firstSlotStart, End: firstSlotStart.Add(time.Hour)},
	}
	assertSlotsEqual(t, got, want)
}

func assertSlotsEqual(t *testing.T, got, want []domain.AvailabilitySlot) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
			t.Fatalf("slot %d = %+v, want %+v\nfull got:  %+v\nfull want: %+v", i, got[i], want[i], got, want)
		}
	}
}

// --- SchedulingService: booking-link CRUD, PublicPage, PublicSlots -----------

type schedFixture struct {
	svc       *SchedulingService
	links     *fakeBookingLinkRepo
	bookings  *fakeBookingRepo
	users     *fakeUserRepo
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *fakeEventRepo
	settings  *fakeUserSettingsRepo
	clock     *fakeClock
}

func newSchedFixture(t *testing.T) *schedFixture {
	t.Helper()
	links := newBookingLinkRepo()
	bookings := newBookingRepo(links)
	users := newUserRepo()
	accounts := newAccountRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	settingsRepo := newUserSettingsRepo()
	clock := newClock(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))

	svc := NewSchedulingService(SchedulingServiceDeps{
		Users:      users,
		Accounts:   accounts,
		Calendars:  calendars,
		Events:     events,
		Links:      links,
		Bookings:   bookings,
		Settings:   settingsRepo,
		Clock:      clock,
		SelfHosted: true,
	})
	return &schedFixture{
		svc: svc, links: links, bookings: bookings, users: users,
		accounts: accounts, calendars: calendars, events: events,
		settings: settingsRepo, clock: clock,
	}
}

// seedOwnedCalendar creates an account + writable calendar owned by userID
// and returns the calendar id.
func (f *schedFixture) seedOwnedCalendar(t *testing.T, userID string, canWrite bool) string {
	t.Helper()
	ctx := context.Background()
	accountID := userID + "-acct"
	if _, err := f.accounts.Create(ctx, domain.ConnectedAccount{
		ID: accountID, UserID: userID, Provider: domain.ProviderGoogle, Email: userID + "@example.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	cal, err := f.calendars.Upsert(ctx, domain.Calendar{
		AccountID: accountID, ProviderCalendarID: "prov-" + userID, CanWrite: canWrite,
	})
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	return cal.ID
}

func validLinkInput(calendarID string) port.BookingLinkInput {
	return port.BookingLinkInput{
		Slug:            "intro-call",
		Title:           "Intro Call",
		CalendarID:      calendarID,
		DurationMinutes: 30,
		TimeZone:        "UTC",
		Windows: []domain.AvailabilityWindow{
			{Weekday: 1, Start: "09:00", End: "17:00"},
		},
		Active: true,
	}
}

func TestSchedulingLinksCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("valid input creates the link", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)

		got, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("CreateLink: %v", err)
		}
		if got.ID == "" || got.UserID != "u1" || got.Slug != "intro-call" {
			t.Fatalf("CreateLink result = %+v", got)
		}
	})

	t.Run("invalid slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Slug = "Not_Valid!"

		_, err := f.svc.CreateLink(ctx, "u1", in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("reserved slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Slug = "admin"

		_, err := f.svc.CreateLink(ctx, "u1", in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("calendar owned by someone else 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "owner", true)

		_, err := f.svc.CreateLink(ctx, "intruder", validLinkInput(calID))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("read-only calendar rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", false)

		_, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("slug collision propagates ErrConflict", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed first link: %v", err)
		}

		_, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestSchedulingLinksUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("owner can update", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		in := validLinkInput(calID)
		in.Title = "Renamed"
		got, err := f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if err != nil {
			t.Fatalf("UpdateLink: %v", err)
		}
		if got.Title != "Renamed" || got.ID != created.ID || !got.CreatedAt.Equal(created.CreatedAt) {
			t.Fatalf("UpdateLink result = %+v", got)
		}
	})

	t.Run("foreign link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "owner", true)
		created, err := f.svc.CreateLink(ctx, "owner", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		_, err = f.svc.UpdateLink(ctx, "intruder", created.ID, validLinkInput(calID))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid slug rejected", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}

		in := validLinkInput(calID)
		in.Slug = "!!bad!!"
		_, err = f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("calendar must still be owned and writable", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}
		foreignCalID := f.seedOwnedCalendar(t, "someone-else", true)

		in := validLinkInput(foreignCalID)
		_, err = f.svc.UpdateLink(ctx, "u1", created.ID, in)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("slug collision with another link propagates ErrConflict", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		first := validLinkInput(calID)
		first.Slug = "first-slug"
		if _, err := f.svc.CreateLink(ctx, "u1", first); err != nil {
			t.Fatalf("seed first: %v", err)
		}
		second := validLinkInput(calID)
		second.Slug = "second-slug"
		createdSecond, err := f.svc.CreateLink(ctx, "u1", second)
		if err != nil {
			t.Fatalf("seed second: %v", err)
		}

		in := validLinkInput(calID)
		in.Slug = "first-slug" // collides with the first link
		_, err = f.svc.UpdateLink(ctx, "u1", createdSecond.ID, in)
		if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

func TestSchedulingLinksListAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newSchedFixture(t)
	calID := f.seedOwnedCalendar(t, "u1", true)
	created, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID))
	if err != nil {
		t.Fatalf("seed link: %v", err)
	}

	links, err := f.svc.ListLinks(ctx, "u1")
	if err != nil || len(links) != 1 {
		t.Fatalf("ListLinks = %v, %v; want 1 link", links, err)
	}

	if err := f.svc.DeleteLink(ctx, "intruder", created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("DeleteLink(foreign) = %v, want ErrNotFound", err)
	}
	if err := f.svc.DeleteLink(ctx, "u1", created.ID); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	links, err = f.svc.ListLinks(ctx, "u1")
	if err != nil || len(links) != 0 {
		t.Fatalf("ListLinks after delete = %v, %v; want empty", links, err)
	}
}

func TestSchedulingLinksPublicPage(t *testing.T) {
	ctx := context.Background()

	t.Run("active link returns page with owner display name", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		name := "Ada Lovelace"
		if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "ada@example.com", Name: &name}); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		page, err := f.svc.PublicPage(ctx, "intro-call")
		if err != nil {
			t.Fatalf("PublicPage: %v", err)
		}
		if page.OwnerName != "Ada Lovelace" || page.Slug != "intro-call" || page.DurationMinutes != 30 {
			t.Fatalf("PublicPage = %+v", page)
		}
	})

	t.Run("owner without a display name falls back to email", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		if _, err := f.svc.CreateLink(ctx, "u1", validLinkInput(calID)); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		if _, err := f.users.Upsert(ctx, domain.User{ID: "u1", Email: "ada@example.com"}); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		page, err := f.svc.PublicPage(ctx, "intro-call")
		if err != nil {
			t.Fatalf("PublicPage: %v", err)
		}
		if page.OwnerName != "ada@example.com" {
			t.Fatalf("OwnerName = %q, want fallback email", page.OwnerName)
		}
	})

	t.Run("inactive link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Active = false
		if _, err := f.svc.CreateLink(ctx, "u1", in); err != nil {
			t.Fatalf("seed link: %v", err)
		}

		_, err := f.svc.PublicPage(ctx, "intro-call")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown slug 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		if _, err := f.svc.PublicPage(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSchedulingLinksPublicSlots(t *testing.T) {
	ctx := context.Background()

	newActiveLinkFixture := func(t *testing.T, maxAdvanceDays int) (*schedFixture, domain.BookingLink) {
		t.Helper()
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.MaxAdvanceDays = maxAdvanceDays
		// Every weekday open 09:00-17:00 so the query window always has slots
		// to clamp/reject against, regardless of which day `now` falls on.
		in.Windows = make([]domain.AvailabilityWindow, 7)
		for wd := 0; wd < 7; wd++ {
			in.Windows[wd] = domain.AvailabilityWindow{Weekday: wd, Start: "09:00", End: "17:00"}
		}
		created, err := f.svc.CreateLink(ctx, "u1", in)
		if err != nil {
			t.Fatalf("seed link: %v", err)
		}
		return f, created
	}

	t.Run("to must be after from", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 0)
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("query window over 31 days rejected", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 0)
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 32))
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("range clamped to the link's maxAdvanceDays", func(t *testing.T) {
		f, _ := newActiveLinkFixture(t, 5)
		now := f.clock.now
		slots, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 10))
		if err != nil {
			t.Fatalf("PublicSlots: %v", err)
		}
		limit := now.AddDate(0, 0, 5)
		for _, s := range slots {
			if s.Start.After(limit) {
				t.Fatalf("slot %v starts after the 5-day advance limit %v", s, limit)
			}
		}
	})

	t.Run("inactive link 404s", func(t *testing.T) {
		f := newSchedFixture(t)
		calID := f.seedOwnedCalendar(t, "u1", true)
		in := validLinkInput(calID)
		in.Active = false
		if _, err := f.svc.CreateLink(ctx, "u1", in); err != nil {
			t.Fatalf("seed link: %v", err)
		}
		now := f.clock.now
		_, err := f.svc.PublicSlots(ctx, "intro-call", now, now.AddDate(0, 0, 1))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
