package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- Task 12 fakes -----------------------------------------------------------

// fakeShareRepo is a map-backed port.CalendarShareRepo honoring the
// duplicate-grant → ErrConflict contract of the SQL unique indexes.
type fakeShareRepo struct {
	byID  map[string]domain.CalendarShare
	order []string
}

func newFakeShareRepo() *fakeShareRepo {
	return &fakeShareRepo{byID: map[string]domain.CalendarShare{}}
}

func sameGrantee(a, b domain.CalendarShare) bool {
	if a.GranteeUserID != nil && b.GranteeUserID != nil {
		return *a.GranteeUserID == *b.GranteeUserID
	}
	if a.GranteeTeamID != nil && b.GranteeTeamID != nil {
		return *a.GranteeTeamID == *b.GranteeTeamID
	}
	return false
}

func (r *fakeShareRepo) Create(_ context.Context, s domain.CalendarShare) (domain.CalendarShare, error) {
	for _, id := range r.order {
		if ex := r.byID[id]; ex.CalendarID == s.CalendarID && sameGrantee(ex, s) {
			return domain.CalendarShare{}, fmt.Errorf("%w: already shared with this grantee", domain.ErrConflict)
		}
	}
	if s.ID == "" {
		s.ID = newID()
	}
	r.byID[s.ID] = s
	r.order = append(r.order, s.ID)
	return s, nil
}

func (r *fakeShareRepo) ListByCalendar(_ context.Context, calendarID string) ([]domain.CalendarShare, error) {
	out := []domain.CalendarShare{}
	for _, id := range r.order {
		if s := r.byID[id]; s.CalendarID == calendarID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *fakeShareRepo) ListForGrantee(_ context.Context, userID string, teamIDs []string) ([]domain.CalendarShare, error) {
	teams := map[string]struct{}{}
	for _, id := range teamIDs {
		teams[id] = struct{}{}
	}
	out := []domain.CalendarShare{}
	for _, id := range r.order {
		s := r.byID[id]
		if s.GranteeUserID != nil && *s.GranteeUserID == userID {
			out = append(out, s)
			continue
		}
		if s.GranteeTeamID != nil {
			if _, ok := teams[*s.GranteeTeamID]; ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (r *fakeShareRepo) Update(_ context.Context, s domain.CalendarShare) error {
	if _, ok := r.byID[s.ID]; !ok {
		return domain.ErrNotFound
	}
	r.byID[s.ID] = s
	return nil
}

func (r *fakeShareRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

var _ port.CalendarShareRepo = (*fakeShareRepo)(nil)

// fakeAuditRepo records audit entries in order.
type fakeAuditRepo struct {
	entries   []domain.AuditEntry
	recordErr error
}

func (r *fakeAuditRepo) Record(_ context.Context, e domain.AuditEntry) error {
	if r.recordErr != nil {
		return r.recordErr
	}
	r.entries = append(r.entries, e)
	return nil
}

func (r *fakeAuditRepo) ListByPrincipal(_ context.Context, principalID string, limit int) ([]domain.AuditEntry, error) {
	out := []domain.AuditEntry{}
	for i := len(r.entries) - 1; i >= 0; i-- {
		if r.entries[i].PrincipalID == principalID {
			out = append(out, r.entries[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

var _ port.AuditRepo = (*fakeAuditRepo)(nil)

// scopedEventRepo is a port.EventRepo whose ListInRange enforces the SQL
// adapter's user scoping (events → calendars → connected_accounts → user),
// unlike fakeEventRepo which assumes single-user seeding. Cross-tenant
// assertions in this file depend on that scoping being real.
type scopedEventRepo struct {
	byID  map[string]domain.Event
	order []string
	cals  *fakeCalendarRepo
	accts *fakeAccountRepo
}

func (r *scopedEventRepo) ownerOf(calendarID string) string {
	c, ok := r.cals.byID[calendarID]
	if !ok {
		return ""
	}
	a, ok := r.accts.byID[c.AccountID]
	if !ok {
		return ""
	}
	return a.UserID
}

func (r *scopedEventRepo) Upsert(_ context.Context, e domain.Event) (domain.Event, error) {
	if e.ID == "" {
		e.ID = newID()
	}
	if _, ok := r.byID[e.ID]; !ok {
		r.order = append(r.order, e.ID)
	}
	r.byID[e.ID] = e
	return e, nil
}

func (r *scopedEventRepo) GetByID(_ context.Context, id string) (domain.Event, error) {
	e, ok := r.byID[id]
	if !ok {
		return domain.Event{}, domain.ErrNotFound
	}
	return e, nil
}

func (r *scopedEventRepo) GetByProviderID(_ context.Context, calendarID, providerEventID string) (domain.Event, error) {
	for _, id := range r.order {
		if e := r.byID[id]; e.CalendarID == calendarID && e.ProviderEventID == providerEventID {
			return e, nil
		}
	}
	return domain.Event{}, domain.ErrNotFound
}

func (r *scopedEventRepo) ListInRange(_ context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	filter := map[string]struct{}{}
	for _, id := range calendarIDs {
		filter[id] = struct{}{}
	}
	out := []domain.Event{}
	for _, id := range r.order {
		e := r.byID[id]
		if r.ownerOf(e.CalendarID) != userID {
			continue
		}
		if len(filter) > 0 {
			if _, ok := filter[e.CalendarID]; !ok {
				continue
			}
		}
		if e.End.After(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *scopedEventRepo) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

func (r *scopedEventRepo) DeleteByProviderID(_ context.Context, calendarID, providerEventID string) error {
	return nil
}

func (r *scopedEventRepo) Search(_ context.Context, userID, query string, limit int) ([]domain.Event, error) {
	return nil, nil
}

func (r *scopedEventRepo) ClearGeo(_ context.Context, id string) error {
	e, ok := r.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	e.LocationLat, e.LocationLon = nil, nil
	r.byID[id] = e
	return nil
}

var _ port.EventRepo = (*scopedEventRepo)(nil)

// recordingCalendarProvider records the access token of every write so the
// tests can prove editor writes ride the OWNER's tokens, never the grantee's.
type recordingCalendarProvider struct {
	createdEvent domain.Event
	updatedEvent domain.Event

	lastCreateToken      string
	lastCreateCalendarID string
	lastUpdateToken      string
	lastDeleteToken      string
	deleteCalls          int
}

func (p *recordingCalendarProvider) SyncCalendars(_ context.Context, _ string) ([]domain.Calendar, error) {
	return nil, nil
}

func (p *recordingCalendarProvider) SyncEvents(_ context.Context, _, _, _ string) (port.CalendarSyncPage, error) {
	return port.CalendarSyncPage{}, nil
}

func (p *recordingCalendarProvider) CreateEvent(_ context.Context, accessToken, providerCalendarID string, _ domain.EventInput) (domain.Event, error) {
	p.lastCreateToken = accessToken
	p.lastCreateCalendarID = providerCalendarID
	return p.createdEvent, nil
}

func (p *recordingCalendarProvider) UpdateEvent(_ context.Context, accessToken, _, _ string, _ domain.EventPatch) (domain.Event, error) {
	p.lastUpdateToken = accessToken
	return p.updatedEvent, nil
}

func (p *recordingCalendarProvider) DeleteEvent(_ context.Context, accessToken, _, _ string) error {
	p.lastDeleteToken = accessToken
	p.deleteCalls++
	return nil
}

func (p *recordingCalendarProvider) RSVP(_ context.Context, _, _, _ string, _ domain.RsvpStatus, _ string) error {
	return nil
}

func (p *recordingCalendarProvider) FreeBusy(_ context.Context, _ string, _ []string, _, _ time.Time) (map[string][]domain.BusyInterval, error) {
	return nil, nil
}

var _ port.CalendarProvider = (*recordingCalendarProvider)(nil)

// --- fixture -----------------------------------------------------------------

// shareFixture: owner u1 (account a1, token "owner-access") owns writable
// calendar cal1; u2 is a plain viewer; u3 a stranger; team t1 = {u1, u2}.
type shareFixture struct {
	svc       *CalendarService
	accounts  *fakeAccountRepo
	calendars *fakeCalendarRepo
	events    *scopedEventRepo
	shares    *fakeShareRepo
	audit     *fakeAuditRepo
	teams     *fakeTeamRepo
	users     *fakeUserRepo
	provider  *recordingCalendarProvider
	clock     *fakeClock
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)

	users := newUserRepo()
	for _, id := range []string{"u1", "u2", "u3"} {
		if _, err := users.Upsert(ctx, domain.User{ID: id, Email: id + "@x.com"}); err != nil {
			t.Fatalf("seed user %s: %v", id, err)
		}
	}

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "owner@x.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "owner-access", RefreshToken: "r", ExpiresAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	calendars := newCalendarRepo()
	calendars.accounts = accounts
	if _, err := calendars.Upsert(ctx, domain.Calendar{
		ID: "cal1", AccountID: "a1", ProviderCalendarID: "prov-cal-1",
		Name: "Work", CanWrite: true, IsVisible: true,
	}); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	events := &scopedEventRepo{byID: map[string]domain.Event{}, cals: calendars, accts: accounts}

	teams := newTeamRepo()
	if _, err := teams.Create(ctx, domain.Team{ID: "t1", Name: "Crew", CreatedBy: "u1"},
		domain.TeamMember{TeamID: "t1", UserID: "u1", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: "t1", UserID: "u2", Role: domain.TeamRoleMember}); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	shares := newFakeShareRepo()
	audit := &fakeAuditRepo{}
	provider := &recordingCalendarProvider{}

	svc := NewCalendarService(CalendarServiceDeps{
		Subscriptions:     newSubscriptionRepo(),
		Accounts:          accounts,
		Calendars:         calendars,
		Events:            events,
		Templates:         newEventTemplateRepo(),
		Sets:              newCalendarSetRepo(),
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: provider},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
		SelfHosted:        true,
		Shares:            shares,
		Audit:             audit,
		Teams:             teams,
		Users:             users,
	})
	return &shareFixture{svc, accounts, calendars, events, shares, audit, teams, users, provider, clock}
}

// seedSecretEvent stores a detail-rich event on cal1 (every field a
// free_busy viewer must never see is populated).
func (f *shareFixture) seedSecretEvent(id string) domain.Event {
	base := f.clock.Now()
	ev := domain.Event{
		ID: id, CalendarID: "cal1", ProviderEventID: "pe-" + id,
		Title:       "Secret 1:1",
		Description: ptr("comp discussion"),
		Location:    ptr("HQ room 4"),
		Start:       base.Add(time.Hour), End: base.Add(2 * time.Hour),
		Attendees:    []domain.Attendee{{Email: "boss@x.com", Response: domain.RsvpAccepted}},
		Conferencing: &domain.Conferencing{Provider: domain.ConferencingMeet, URL: "https://meet/x"},
		Status:       domain.EventConfirmed, Visibility: domain.VisibilityPrivate,
		RecurrenceRule:  ptr("FREQ=WEEKLY"),
		ReminderMinutes: []int{10},
	}
	f.events.byID[ev.ID] = ev
	f.events.order = append(f.events.order, ev.ID)
	return ev
}

func (f *shareFixture) share(t *testing.T, in port.CalendarShareInput) domain.CalendarShare {
	t.Helper()
	sh, err := f.svc.ShareCalendar(context.Background(), "u1", "cal1", in)
	if err != nil {
		t.Fatalf("ShareCalendar(%+v): %v", in, err)
	}
	return sh
}

func (f *shareFixture) window() (time.Time, time.Time) {
	base := f.clock.Now()
	return base, base.Add(24 * time.Hour)
}

// --- share management --------------------------------------------------------

func TestShareCalendarToUserAndTeam(t *testing.T) {
	f := newShareFixture(t)

	userShare := f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})
	if userShare.CalendarID != "cal1" || userShare.GranteeUserID == nil || *userShare.GranteeUserID != "u2" {
		t.Fatalf("user share = %+v", userShare)
	}
	if userShare.Permission != domain.PermissionReader || userShare.CreatedBy != "u1" {
		t.Fatalf("user share = %+v", userShare)
	}

	teamShare := f.share(t, port.CalendarShareInput{GranteeTeamID: "t1"}) // default permission
	if teamShare.GranteeTeamID == nil || *teamShare.GranteeTeamID != "t1" {
		t.Fatalf("team share = %+v", teamShare)
	}
	if teamShare.Permission != domain.PermissionFreeBusy {
		t.Fatalf("default permission = %q, want free_busy", teamShare.Permission)
	}

	shares, err := f.svc.ListCalendarShares(context.Background(), "u1", "cal1")
	if err != nil || len(shares) != 2 {
		t.Fatalf("ListCalendarShares = %v, %v; want 2 shares", shares, err)
	}
}

func TestShareCalendarNonOwnerGets404(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()

	// u2 (not the owner) and u3 (stranger) both get ErrNotFound — never 403.
	for _, uid := range []string{"u2", "u3"} {
		_, err := f.svc.ShareCalendar(ctx, uid, "cal1", port.CalendarShareInput{GranteeUserID: "u3", Permission: "reader"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ShareCalendar as %s: err = %v, want ErrNotFound", uid, err)
		}
		if _, err := f.svc.ListCalendarShares(ctx, uid, "cal1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ListCalendarShares as %s: err = %v, want ErrNotFound", uid, err)
		}
	}
	if len(f.shares.order) != 0 {
		t.Fatalf("shares stored by non-owner: %d", len(f.shares.order))
	}
}

func TestShareCalendarValidation(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   port.CalendarShareInput
		want error
	}{
		{"no grantee", port.CalendarShareInput{Permission: "reader"}, domain.ErrValidation},
		{"both grantees", port.CalendarShareInput{GranteeUserID: "u2", GranteeTeamID: "t1"}, domain.ErrValidation},
		{"bad permission", port.CalendarShareInput{GranteeUserID: "u2", Permission: "admin"}, domain.ErrValidation},
		{"self grant", port.CalendarShareInput{GranteeUserID: "u1"}, domain.ErrValidation},
		{"unknown user", port.CalendarShareInput{GranteeUserID: "ghost"}, domain.ErrValidation},
		{"team the sharer is not in", port.CalendarShareInput{GranteeTeamID: "t-foreign"}, domain.ErrNotFound},
	}
	for _, tc := range cases {
		if _, err := f.svc.ShareCalendar(ctx, "u1", "cal1", tc.in); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// Duplicate grant → conflict.
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})
	if _, err := f.svc.ShareCalendar(ctx, "u1", "cal1", port.CalendarShareInput{GranteeUserID: "u2"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate share err = %v, want ErrConflict", err)
	}
}

func TestUpdateAndRevokeShare(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	sh := f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "free_busy"})

	up, err := f.svc.UpdateCalendarShare(ctx, "u1", "cal1", sh.ID, "editor")
	if err != nil || up.Permission != domain.PermissionEditor {
		t.Fatalf("UpdateCalendarShare = %+v, %v", up, err)
	}

	// Non-owner cannot update or revoke (404), unknown share is 404.
	if _, err := f.svc.UpdateCalendarShare(ctx, "u2", "cal1", sh.ID, "reader"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-owner update err = %v, want ErrNotFound", err)
	}
	if err := f.svc.RevokeCalendarShare(ctx, "u2", "cal1", sh.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-owner revoke err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.UpdateCalendarShare(ctx, "u1", "cal1", "nope", "reader"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown share err = %v, want ErrNotFound", err)
	}

	if err := f.svc.RevokeCalendarShare(ctx, "u1", "cal1", sh.ID); err != nil {
		t.Fatalf("RevokeCalendarShare: %v", err)
	}
	if len(f.shares.order) != 0 {
		t.Fatalf("share not deleted")
	}
}

// --- viewer-side reads -------------------------------------------------------

func TestListCalendarsIncludesSharedWithAnnotation(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})

	cals, err := f.svc.ListCalendars(ctx, "u2")
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 1 || cals[0].ID != "cal1" {
		t.Fatalf("viewer calendars = %+v, want [cal1]", cals)
	}
	if cals[0].SharedPermission == nil || *cals[0].SharedPermission != domain.PermissionReader {
		t.Fatalf("SharedPermission = %v, want reader", cals[0].SharedPermission)
	}
	if cals[0].CanWrite {
		t.Fatalf("reader-shared calendar must not be writable")
	}

	// The owner's listing is untouched: no annotation, still writable.
	ownerCals, err := f.svc.ListCalendars(ctx, "u1")
	if err != nil || len(ownerCals) != 1 {
		t.Fatalf("owner calendars = %+v, %v", ownerCals, err)
	}
	if ownerCals[0].SharedPermission != nil || !ownerCals[0].CanWrite {
		t.Fatalf("owner calendar mutated by sharing: %+v", ownerCals[0])
	}

	// A stranger sees nothing (privacy default: no share → invisible).
	strangerCals, err := f.svc.ListCalendars(ctx, "u3")
	if err != nil || len(strangerCals) != 0 {
		t.Fatalf("stranger calendars = %+v, %v; want none", strangerCals, err)
	}
}

func TestFreeBusyViewerGetsRedactedEvents(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	secret := f.seedSecretEvent("ev1")
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "free_busy"})
	from, to := f.window()

	for _, calIDs := range [][]string{nil, {"cal1"}} {
		evs, err := f.svc.ListEvents(ctx, "u2", from, to, calIDs)
		if err != nil {
			t.Fatalf("ListEvents(calIDs=%v): %v", calIDs, err)
		}
		if len(evs) != 1 {
			t.Fatalf("ListEvents(calIDs=%v) = %d events, want 1", calIDs, len(evs))
		}
		ev := evs[0]
		if !ev.FreeBusyOnly {
			t.Fatalf("FreeBusyOnly = false")
		}
		if ev.Title != "Busy" {
			t.Fatalf("title = %q leaked; want \"Busy\"", ev.Title)
		}
		if !ev.Start.Equal(secret.Start) || !ev.End.Equal(secret.End) {
			t.Fatalf("busy window %v-%v, want %v-%v", ev.Start, ev.End, secret.Start, secret.End)
		}
		// The privacy surface: every detail field must be zeroed.
		if ev.Description != nil || ev.Location != nil || ev.RecurrenceRule != nil {
			t.Fatalf("details leaked: desc=%v loc=%v rrule=%v", ev.Description, ev.Location, ev.RecurrenceRule)
		}
		if len(ev.Attendees) != 0 {
			t.Fatalf("attendees leaked: %+v", ev.Attendees)
		}
		if ev.Conferencing != nil {
			t.Fatalf("conferencing leaked: %+v", ev.Conferencing)
		}
		if ev.ProviderEventID != "" || ev.Visibility != "" || ev.Status != "" {
			t.Fatalf("metadata leaked: provider=%q vis=%q status=%q", ev.ProviderEventID, ev.Visibility, ev.Status)
		}
		if len(ev.ReminderMinutes) != 0 {
			t.Fatalf("reminders leaked: %v", ev.ReminderMinutes)
		}
	}
}

func TestReaderSeesFullEventsButCannotWrite(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	secret := f.seedSecretEvent("ev1")
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})
	from, to := f.window()

	evs, err := f.svc.ListEvents(ctx, "u2", from, to, nil)
	if err != nil || len(evs) != 1 {
		t.Fatalf("ListEvents = %v, %v", evs, err)
	}
	if evs[0].Title != secret.Title || evs[0].FreeBusyOnly {
		t.Fatalf("reader event = %+v, want full read", evs[0])
	}

	// Reader writes → 403 (known grantee, insufficient permission).
	_, err = f.svc.CreateEvent(ctx, "u2", domain.EventInput{
		CalendarID: "cal1", Title: "Sneaky", Start: from.Add(time.Hour), End: from.Add(2 * time.Hour),
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader CreateEvent err = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.UpdateEvent(ctx, "u2", "ev1", domain.EventPatch{Title: ptr("Hijack")}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader UpdateEvent err = %v, want ErrForbidden", err)
	}
	if err := f.svc.DeleteEvent(ctx, "u2", "ev1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader DeleteEvent err = %v, want ErrForbidden", err)
	}
	if len(f.audit.entries) != 0 {
		t.Fatalf("forbidden writes must not audit: %+v", f.audit.entries)
	}
}

func TestStrangerAndCrossTenantIsolation(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	// A share to u2 exists; u3 has none.
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "editor"})
	from, to := f.window()

	// Explicitly requesting the foreign calendar id leaks nothing.
	evs, err := f.svc.ListEvents(ctx, "u3", from, to, []string{"cal1"})
	if err != nil || len(evs) != 0 {
		t.Fatalf("stranger ListEvents = %v, %v; want empty", evs, err)
	}
	// Writes are indistinguishable from a missing calendar (404, never 403).
	_, err = f.svc.CreateEvent(ctx, "u3", domain.EventInput{
		CalendarID: "cal1", Title: "X", Start: from, End: from.Add(time.Hour),
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger CreateEvent err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.UpdateEvent(ctx, "u3", "ev1", domain.EventPatch{Title: ptr("X")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger UpdateEvent err = %v, want ErrNotFound", err)
	}
	if err := f.svc.DeleteEvent(ctx, "u3", "ev1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger DeleteEvent err = %v, want ErrNotFound", err)
	}
}

// --- editor write path -------------------------------------------------------

func TestEditorWritesThroughOwnerTokensAndAudits(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "editor"})
	from, _ := f.window()

	f.provider.createdEvent = domain.Event{
		ProviderEventID: "pe-new", Title: "Pairing",
		Start: from.Add(time.Hour), End: from.Add(2 * time.Hour),
		Status: domain.EventConfirmed,
	}
	got, err := f.svc.CreateEvent(ctx, "u2", domain.EventInput{
		CalendarID: "cal1", Title: "Pairing", Start: from.Add(time.Hour), End: from.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("editor CreateEvent: %v", err)
	}
	if f.provider.lastCreateToken != "owner-access" {
		t.Fatalf("provider token = %q, want the OWNER's token", f.provider.lastCreateToken)
	}
	if f.provider.lastCreateCalendarID != "prov-cal-1" {
		t.Fatalf("provider calendar = %q, want prov-cal-1", f.provider.lastCreateCalendarID)
	}
	if got.CalendarID != "cal1" {
		t.Fatalf("created event calendar = %q", got.CalendarID)
	}
	if _, err := f.events.GetByID(ctx, got.ID); err != nil {
		t.Fatalf("created event not mirrored: %v", err)
	}

	f.provider.updatedEvent = domain.Event{ProviderEventID: "pe-new", Title: "Pairing v2", Start: got.Start, End: got.End, Status: domain.EventConfirmed}
	if _, err := f.svc.UpdateEvent(ctx, "u2", got.ID, domain.EventPatch{Title: ptr("Pairing v2")}); err != nil {
		t.Fatalf("editor UpdateEvent: %v", err)
	}
	if f.provider.lastUpdateToken != "owner-access" {
		t.Fatalf("update token = %q, want owner-access", f.provider.lastUpdateToken)
	}
	if err := f.svc.DeleteEvent(ctx, "u2", got.ID); err != nil {
		t.Fatalf("editor DeleteEvent: %v", err)
	}
	if f.provider.lastDeleteToken != "owner-access" || f.provider.deleteCalls != 1 {
		t.Fatalf("delete token = %q calls = %d", f.provider.lastDeleteToken, f.provider.deleteCalls)
	}

	wantActions := []string{"event.create", "event.update", "event.delete"}
	if len(f.audit.entries) != len(wantActions) {
		t.Fatalf("audit entries = %d, want %d (%+v)", len(f.audit.entries), len(wantActions), f.audit.entries)
	}
	for i, e := range f.audit.entries {
		if e.Action != wantActions[i] || e.ActorID != "u2" || e.PrincipalID != "u1" || e.ResourceType != "event" {
			t.Fatalf("audit[%d] = %+v", i, e)
		}
		if e.Metadata["calendarId"] != "cal1" {
			t.Fatalf("audit[%d] metadata = %+v", i, e.Metadata)
		}
	}
}

func TestRevokeHidesCalendar(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	sh := f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "editor"})
	from, to := f.window()

	if evs, _ := f.svc.ListEvents(ctx, "u2", from, to, nil); len(evs) != 1 {
		t.Fatalf("precondition: editor sees 1 event, got %d", len(evs))
	}
	if err := f.svc.RevokeCalendarShare(ctx, "u1", "cal1", sh.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if cals, _ := f.svc.ListCalendars(ctx, "u2"); len(cals) != 0 {
		t.Fatalf("revoked calendar still visible: %+v", cals)
	}
	if evs, _ := f.svc.ListEvents(ctx, "u2", from, to, nil); len(evs) != 0 {
		t.Fatalf("revoked events still visible: %+v", evs)
	}
	if _, err := f.svc.UpdateEvent(ctx, "u2", "ev1", domain.EventPatch{Title: ptr("X")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("post-revoke write err = %v, want ErrNotFound", err)
	}
}

func TestTeamGrantAppliesToAllMembers(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	f.share(t, port.CalendarShareInput{GranteeTeamID: "t1", Permission: "reader"})
	from, to := f.window()

	// u2 (member) sees the calendar and full events.
	cals, err := f.svc.ListCalendars(ctx, "u2")
	if err != nil || len(cals) != 1 || cals[0].SharedPermission == nil || *cals[0].SharedPermission != domain.PermissionReader {
		t.Fatalf("member calendars = %+v, %v", cals, err)
	}
	if evs, _ := f.svc.ListEvents(ctx, "u2", from, to, nil); len(evs) != 1 || evs[0].Title != "Secret 1:1" {
		t.Fatalf("member events = %+v", evs)
	}

	// u3 (not a member) sees nothing.
	if cals, _ := f.svc.ListCalendars(ctx, "u3"); len(cals) != 0 {
		t.Fatalf("non-member sees shared calendar: %+v", cals)
	}

	// The owner's own listing is not duplicated by the loop-back team grant.
	ownerCals, _ := f.svc.ListCalendars(ctx, "u1")
	if len(ownerCals) != 1 || ownerCals[0].SharedPermission != nil {
		t.Fatalf("owner listing polluted: %+v", ownerCals)
	}
}

// TestTeamGrantDormantAfterOwnerLeavesTeam: team-audience grants are
// membership-checked at resolution time, not just at grant time — the owner
// leaving the team cuts the whole team's view immediately (no manual revoke
// needed), while direct user grants are unaffected.
func TestTeamGrantDormantAfterOwnerLeavesTeam(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	f.share(t, port.CalendarShareInput{GranteeTeamID: "t1", Permission: "editor"})
	from, to := f.window()

	// Precondition: member u2 sees the calendar via the live team grant.
	if cals, err := f.svc.ListCalendars(ctx, "u2"); err != nil || len(cals) != 1 {
		t.Fatalf("precondition calendars = %+v, %v; want 1", cals, err)
	}

	// Owner u1 leaves team t1.
	if err := f.teams.RemoveMember(ctx, "t1", "u1"); err != nil {
		t.Fatalf("remove owner from team: %v", err)
	}

	// u2 loses access immediately: no listing, no events, 404 on write.
	if cals, err := f.svc.ListCalendars(ctx, "u2"); err != nil || len(cals) != 0 {
		t.Fatalf("post-leave calendars = %+v, %v; want none", cals, err)
	}
	if evs, err := f.svc.ListEvents(ctx, "u2", from, to, nil); err != nil || len(evs) != 0 {
		t.Fatalf("post-leave events = %+v, %v; want none", evs, err)
	}
	if _, err := f.svc.UpdateEvent(ctx, "u2", "ev1", domain.EventPatch{Title: ptr("X")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("post-leave write err = %v, want ErrNotFound", err)
	}

	// A direct user grant is untouched by the owner's team departure.
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})
	cals, err := f.svc.ListCalendars(ctx, "u2")
	if err != nil || len(cals) != 1 || cals[0].SharedPermission == nil || *cals[0].SharedPermission != domain.PermissionReader {
		t.Fatalf("direct-grant calendars = %+v, %v; want reader grant intact", cals, err)
	}
}

func TestMostPermissiveGrantWins(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "free_busy"})
	f.share(t, port.CalendarShareInput{GranteeTeamID: "t1", Permission: "editor"})
	from, to := f.window()

	cals, err := f.svc.ListCalendars(ctx, "u2")
	if err != nil || len(cals) != 1 {
		t.Fatalf("calendars = %+v, %v", cals, err)
	}
	if cals[0].SharedPermission == nil || *cals[0].SharedPermission != domain.PermissionEditor {
		t.Fatalf("effective permission = %v, want editor", cals[0].SharedPermission)
	}
	// Full read (no redaction) …
	evs, err := f.svc.ListEvents(ctx, "u2", from, to, nil)
	if err != nil || len(evs) != 1 || evs[0].FreeBusyOnly || evs[0].Title != "Secret 1:1" {
		t.Fatalf("events = %+v, %v; want full read", evs, err)
	}
	// … and writes allowed.
	f.provider.updatedEvent = domain.Event{ProviderEventID: "pe-ev1", Title: "Moved", Start: evs[0].Start, End: evs[0].End, Status: domain.EventConfirmed}
	if _, err := f.svc.UpdateEvent(ctx, "u2", "ev1", domain.EventPatch{Title: ptr("Moved")}); err != nil {
		t.Fatalf("editor-by-team UpdateEvent: %v", err)
	}
}

// --- team availability (Task 13) ---------------------------------------------

// seedEvent stores a bare confirmed event on any calendar (details present so
// a leak would be observable through anything richer than start/end).
func (f *shareFixture) seedEvent(id, calID string, start, end time.Time, allDay bool) domain.Event {
	ev := domain.Event{
		ID: id, CalendarID: calID, ProviderEventID: "pe-" + id,
		Title: "Secret " + id, Description: ptr("private detail"),
		Start: start, End: end, AllDay: allDay,
		Status: domain.EventConfirmed,
	}
	f.events.byID[ev.ID] = ev
	f.events.order = append(f.events.order, ev.ID)
	return ev
}

func TestTeamAvailabilityMembershipAndRangeValidation(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	from, to := f.window()

	// Non-member and unknown team are both the membership primitive's 404.
	if _, err := f.svc.TeamAvailability(ctx, "u3", "t1", from, to); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("non-member err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.TeamAvailability(ctx, "u1", "t-ghost", from, to); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown team err = %v, want ErrNotFound", err)
	}

	for _, badTo := range []time.Time{from, from.Add(-time.Hour)} {
		if _, err := f.svc.TeamAvailability(ctx, "u1", "t1", from, badTo); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("to=%v err = %v, want ErrValidation", badTo, err)
		}
	}
	if _, err := f.svc.TeamAvailability(ctx, "u1", "t1", from, from.Add(35*24*time.Hour+time.Second)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("oversized span err = %v, want ErrValidation", err)
	}
	if _, err := f.svc.TeamAvailability(ctx, "u1", "t1", from, from.Add(35*24*time.Hour)); err != nil {
		t.Fatalf("exactly-35-day span: %v", err)
	}
}

func TestTeamAvailabilityWithoutTeamGrantSharesNothing(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	f.seedSecretEvent("ev1")
	// A DIRECT user grant is a 1:1 share, not a team-availability opt-in.
	f.share(t, port.CalendarShareInput{GranteeUserID: "u2", Permission: "reader"})
	from, to := f.window()

	rows, err := f.svc.TeamAvailability(ctx, "u2", "t1", from, to)
	if err != nil {
		t.Fatalf("TeamAvailability: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (u1, u2)", len(rows))
	}
	for _, row := range rows {
		if row.Shared {
			t.Fatalf("member %s Shared = true without a team grant", row.UserID)
		}
		if len(row.Busy) != 0 {
			t.Fatalf("member %s leaked busy data: %+v", row.UserID, row.Busy)
		}
	}
}

func TestTeamAvailabilityExposesOnlyGrantedCalendarBusy(t *testing.T) {
	f := newShareFixture(t)
	ctx := context.Background()
	base := f.clock.Now()

	// Second calendar for the same owner, NOT granted to the team.
	if _, err := f.calendars.Upsert(ctx, domain.Calendar{
		ID: "cal2", AccountID: "a1", ProviderCalendarID: "prov-cal-2",
		Name: "Private", CanWrite: true, IsVisible: true,
	}); err != nil {
		t.Fatalf("seed cal2: %v", err)
	}

	secret := f.seedSecretEvent("ev1") // cal1: base+1h .. base+2h
	f.seedEvent("ev-overlap", "cal1", base.Add(90*time.Minute), base.Add(150*time.Minute), false)
	f.seedEvent("ev-allday", "cal1", base, base.Add(24*time.Hour), true) // must not blanket the day
	f.seedEvent("ev-private", "cal2", base.Add(5*time.Hour), base.Add(6*time.Hour), false)

	f.share(t, port.CalendarShareInput{GranteeTeamID: "t1", Permission: "free_busy"})
	from, to := f.window()

	rows, err := f.svc.TeamAvailability(ctx, "u2", "t1", from, to)
	if err != nil {
		t.Fatalf("TeamAvailability: %v", err)
	}
	byUser := map[string]port.MemberAvailability{}
	for _, row := range rows {
		byUser[row.UserID] = row
	}

	owner := byUser["u1"]
	if !owner.Shared {
		t.Fatalf("granting owner not Shared: %+v", owner)
	}
	// Overlapping events merge into ONE opaque block; the ungranted cal2
	// event (base+5h..6h) and the all-day banner contribute nothing.
	if len(owner.Busy) != 1 {
		t.Fatalf("owner busy = %+v, want a single merged block", owner.Busy)
	}
	if !owner.Busy[0].Start.Equal(secret.Start) || !owner.Busy[0].End.Equal(base.Add(150*time.Minute)) {
		t.Fatalf("busy block %v..%v, want %v..%v",
			owner.Busy[0].Start, owner.Busy[0].End, secret.Start, base.Add(150*time.Minute))
	}

	viewer := byUser["u2"]
	if viewer.Shared || len(viewer.Busy) != 0 {
		t.Fatalf("non-granting member row = %+v, want Shared=false and no data", viewer)
	}
}
