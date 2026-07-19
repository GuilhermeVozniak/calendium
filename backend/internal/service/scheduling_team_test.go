package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- Team booking links (M2.7 Task 14) fixture --------------------------------

// teamLinkFixture wires a SchedulingService with a team t1 (owner u1
// "organizer@x.com" on account a1/cal1, member u2 "member@x.com" on account
// a2/cal2) where u2 has shared cal2 with t1 at free_busy, plus an outsider
// u3 who is on no team. The event repo is user-scoped (calendars+accounts
// wired) so u1's and u2's mirrors stay distinct.
type teamLinkFixture struct {
	svc       *SchedulingService
	links     *fakeBookingLinkRepo
	teams     *fakeTeamRepo
	shares    *fakeShareRepo
	shareID   string
	events    *fakeEventRepo
	calendars *fakeCalendarRepo
	calProv   *fakeCalendarProvider
	mailProv  *fakeMailProvider
	clock     *fakeClock
}

func newTeamLinkFixture(t *testing.T) *teamLinkFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC) // a Monday
	clock := newClock(base)

	accounts := newAccountRepo()
	for _, a := range []domain.ConnectedAccount{
		{ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "organizer@x.com"},
		{ID: "a2", UserID: "u2", Provider: domain.ProviderGoogle, Email: "member@x.com"},
	} {
		if _, err := accounts.Create(ctx, a); err != nil {
			t.Fatalf("seed account: %v", err)
		}
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "valid-access", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	calendars := newCalendarRepo()
	calendars.accounts = accounts
	for _, c := range []domain.Calendar{
		{ID: "cal1", AccountID: "a1", ProviderCalendarID: "prov-cal-1", Name: "Work", CanWrite: true, IsVisible: true},
		{ID: "cal2", AccountID: "a2", ProviderCalendarID: "prov-cal-2", Name: "Member", CanWrite: true, IsVisible: true},
	} {
		calendars.byID[c.ID] = c
		calendars.order = append(calendars.order, c.ID)
	}

	users := newUserRepo()
	for _, u := range []domain.User{
		{ID: "u1", Email: "organizer@x.com", Name: ptr("Organizer")},
		{ID: "u2", Email: "member@x.com", Name: ptr("Member")},
		{ID: "u3", Email: "outsider@x.com"},
	} {
		if _, err := users.Upsert(ctx, u); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	teams := newTeamRepo()
	if _, err := teams.Create(ctx, domain.Team{ID: "t1", Name: "Sales", CreatedBy: "u1"},
		domain.TeamMember{TeamID: "t1", UserID: "u1", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: "t1", UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	shares := newFakeShareRepo()
	share, err := shares.Create(ctx, domain.CalendarShare{
		CalendarID: "cal2", GranteeTeamID: ptr("t1"),
		Permission: domain.PermissionFreeBusy, CreatedBy: "u2",
	})
	if err != nil {
		t.Fatalf("seed share: %v", err)
	}

	events := newEventRepo()
	events.calendars = calendars
	events.accounts = accounts

	links := newBookingLinkRepo()
	bookings := newBookingRepo(links)
	calProv := newCalendarProvider()
	calProv.createdEvent = domain.Event{ID: "provider-assigned", ProviderEventID: "prov-evt-1"}
	mailProv := newMailProvider()

	svc := NewSchedulingService(SchedulingServiceDeps{
		Users:             users,
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Links:             links,
		Bookings:          bookings,
		Settings:          newUserSettingsRepo(),
		Teams:             teams,
		Shares:            shares,
		Tx:                newTxRunner(),
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: calProv},
		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mailProv},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
	})
	return &teamLinkFixture{
		svc: svc, links: links, teams: teams, shares: shares, shareID: share.ID,
		events: events, calendars: calendars, calProv: calProv, mailProv: mailProv, clock: clock,
	}
}

// teamLinkInput is a valid team-scoped booking link on t1 with member u2:
// 30-minute slots, every day 09:00-17:00 UTC.
func teamLinkInput() port.BookingLinkInput {
	windows := make([]domain.AvailabilityWindow, 7)
	for wd := range windows {
		windows[wd] = domain.AvailabilityWindow{Weekday: wd, Start: "09:00", End: "17:00"}
	}
	return port.BookingLinkInput{
		Slug: "team-intro", Title: "Sales Intro", CalendarID: "cal1",
		DurationMinutes: 30, TimeZone: "UTC", Windows: windows, Active: true,
		TeamID: ptr("t1"), MemberUserIDs: []string{"u2"},
	}
}

// seedMemberEvent puts a busy event on u2's mirror (cal2).
func (f *teamLinkFixture) seedMemberEvent(id string, start, end time.Time) {
	f.events.byID[id] = domain.Event{ID: id, CalendarID: "cal2", Title: "1:1", Start: start, End: end}
	f.events.order = append(f.events.order, id)
}

// --- create/update validation --------------------------------------------------

func TestTeamLinkCreateStoresTeamAndNormalizedMembers(t *testing.T) {
	ctx := context.Background()
	f := newTeamLinkFixture(t)
	in := teamLinkInput()
	in.MemberUserIDs = []string{"u1", "u2", "u2", " "} // creator implicit, dupes/blanks dropped

	link, err := f.svc.CreateLink(ctx, "u1", in)
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if link.TeamID == nil || *link.TeamID != "t1" {
		t.Fatalf("TeamID = %v, want t1", link.TeamID)
	}
	if len(link.MemberUserIDs) != 1 || link.MemberUserIDs[0] != "u2" {
		t.Fatalf("MemberUserIDs = %v, want [u2]", link.MemberUserIDs)
	}
}

func TestTeamLinkCreateAuthzAndValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		creator  string
		prep     func(f *teamLinkFixture)
		mutate   func(in *port.BookingLinkInput)
		want     error
		wantText string
	}{
		{
			// Cross-tenant negative: an outsider must not learn the team
			// exists — 404, never 403 or a validation hint.
			name:    "creator outside the team gets 404",
			creator: "u3",
			mutate: func(in *port.BookingLinkInput) {
				in.CalendarID = "cal1" // still u1's calendar → 404 there first is fine too
			},
			want: domain.ErrNotFound,
		},
		{
			name:   "unknown team gets 404",
			mutate: func(in *port.BookingLinkInput) { in.TeamID = ptr("ghost") },
			want:   domain.ErrNotFound,
		},
		{
			name: "memberUserIds without teamId is invalid",
			mutate: func(in *port.BookingLinkInput) {
				in.TeamID = nil
			},
			want: domain.ErrValidation,
		},
		{
			name:     "listed member outside the team is named",
			mutate:   func(in *port.BookingLinkInput) { in.MemberUserIDs = []string{"u3"} },
			want:     domain.ErrValidation,
			wantText: "u3",
		},
		{
			name:     "member without a free_busy grant to the team is named",
			prep:     func(f *teamLinkFixture) { _ = f.shares.Delete(context.Background(), f.shareID) },
			want:     domain.ErrValidation,
			wantText: `"u2" has not shared free/busy`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTeamLinkFixture(t)
			if tt.prep != nil {
				tt.prep(f)
			}
			in := teamLinkInput()
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			creator := tt.creator
			if creator == "" {
				creator = "u1"
			}
			_, err := f.svc.CreateLink(ctx, creator, in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateLink err = %v, want %v", err, tt.want)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("error %q does not name the offending member (%q)", err, tt.wantText)
			}
		})
	}
}

func TestTeamLinkUpdateRevalidatesMembers(t *testing.T) {
	ctx := context.Background()
	f := newTeamLinkFixture(t)
	link, err := f.svc.CreateLink(ctx, "u1", teamLinkInput())
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	// A team member who does not own the link cannot update it (404, not 403).
	if _, err := f.svc.UpdateLink(ctx, "u2", link.ID, teamLinkInput()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("UpdateLink by non-owner err = %v, want ErrNotFound", err)
	}

	// Adding a non-member on update fails validation like on create.
	in := teamLinkInput()
	in.MemberUserIDs = []string{"u2", "u3"}
	if _, err := f.svc.UpdateLink(ctx, "u1", link.ID, in); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateLink err = %v, want ErrValidation", err)
	}

	// Dropping the team on update reverts to a personal link.
	in = teamLinkInput()
	in.TeamID, in.MemberUserIDs = nil, nil
	updated, err := f.svc.UpdateLink(ctx, "u1", link.ID, in)
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if updated.TeamID != nil || len(updated.MemberUserIDs) != 0 {
		t.Fatalf("updated link still team-scoped: %+v", updated)
	}
}

// --- collective availability ---------------------------------------------------

func TestTeamLinkCollectiveSlotsIntersectMembers(t *testing.T) {
	ctx := context.Background()
	f := newTeamLinkFixture(t)
	link, err := f.svc.CreateLink(ctx, "u1", teamLinkInput())
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	// u2 is busy 10:00-10:30 on their own mirror (cal2).
	busyStart := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	f.seedMemberEvent("ev-member", busyStart, busyStart.Add(30*time.Minute))

	from := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	slots, err := f.svc.PublicSlots(ctx, link.Slug, from, to)
	if err != nil {
		t.Fatalf("PublicSlots: %v", err)
	}
	if containsStart(slots, busyStart) {
		t.Fatalf("slot at member-busy %v still offered — collective intersection broken", busyStart)
	}
	if !containsStart(slots, busyStart.Add(time.Hour)) {
		t.Fatalf("free 11:00 slot missing; slots = %v", slots)
	}

	// Removing u2 from the team drops them from the intersection on the very
	// next request — slots are recomputed per request, so the link simply
	// intersects fewer members (fewer/none semantics).
	if err := f.teams.RemoveMember(ctx, "t1", "u2"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	slots, err = f.svc.PublicSlots(ctx, link.Slug, from, to)
	if err != nil {
		t.Fatalf("PublicSlots after removal: %v", err)
	}
	if !containsStart(slots, busyStart) {
		t.Fatalf("removed member's busy still blocks %v", busyStart)
	}
}

// --- booking confirmation ------------------------------------------------------

func TestTeamLinkBookInvitesMembersOnCreatorCalendar(t *testing.T) {
	ctx := context.Background()
	f := newTeamLinkFixture(t)
	if _, err := f.svc.CreateLink(ctx, "u1", teamLinkInput()); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	req := port.BookingRequest{
		Start:        time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC),
		InviteeName:  "Ivy Invitee",
		InviteeEmail: "ivy@example.com",
		InviteeTZ:    "UTC",
	}
	got, err := f.svc.Book(ctx, "team-intro", req)
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if got.Status != domain.BookingConfirmed {
		t.Fatalf("Status = %q, want confirmed", got.Status)
	}

	// Single provider write on the CREATOR's calendar — no cross-account fan-out.
	if f.calProv.lastCreateCalendarID != "prov-cal-1" {
		t.Fatalf("event created on %q, want creator's prov-cal-1", f.calProv.lastCreateCalendarID)
	}
	want := map[string]bool{"organizer@x.com": true, "ivy@example.com": true, "member@x.com": true}
	if len(f.calProv.lastCreateInput.AttendeeEmails) != len(want) {
		t.Fatalf("attendees = %v, want owner+invitee+member", f.calProv.lastCreateInput.AttendeeEmails)
	}
	for _, e := range f.calProv.lastCreateInput.AttendeeEmails {
		if !want[e] {
			t.Fatalf("unexpected attendee %q", e)
		}
	}

	// A member removed from the team is no longer invited on later bookings.
	if err := f.teams.RemoveMember(ctx, "t1", "u2"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	req.Start = req.Start.Add(time.Hour)
	if _, err := f.svc.Book(ctx, "team-intro", req); err != nil {
		t.Fatalf("second Book: %v", err)
	}
	for _, e := range f.calProv.lastCreateInput.AttendeeEmails {
		if e == "member@x.com" {
			t.Fatalf("removed member still invited: %v", f.calProv.lastCreateInput.AttendeeEmails)
		}
	}
}

// --- public surface fail-closed ------------------------------------------------

func TestTeamLinkPublicPayloadLeaksNoMembership(t *testing.T) {
	ctx := context.Background()
	f := newTeamLinkFixture(t)
	if _, err := f.svc.CreateLink(ctx, "u1", teamLinkInput()); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}

	page, err := f.svc.PublicPage(ctx, "team-intro")
	if err != nil {
		t.Fatalf("PublicPage: %v", err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	for _, leak := range []string{"t1", "u2", "member@x.com", "organizer@x.com"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("public booking page leaks %q: %s", leak, raw)
		}
	}
}
