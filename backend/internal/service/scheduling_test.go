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
