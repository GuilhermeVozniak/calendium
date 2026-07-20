package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// CalendarServiceDeps wires a CalendarService.
type CalendarServiceDeps struct {
	Subscriptions     port.SubscriptionRepo
	Accounts          port.AccountRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	Templates         port.EventTemplateRepo
	Sets              port.CalendarSetRepo
	CalendarProviders map[domain.Provider]port.CalendarProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Clock             port.Clock
	// Settings supplies the user's working hours for Availability. Left nil,
	// Availability skips the working-hours intersection (existing callers
	// that don't wire it up keep their prior free-gap-only behavior).
	Settings port.UserSettingsRepo
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool

	// --- Shared calendars (M2.7 Task 12). All four are optional: left nil,
	// sharing is disabled and personal-calendar behavior is unchanged. ---
	Shares port.CalendarShareRepo
	Audit  port.AuditRepo
	Teams  port.TeamRepo
	Users  port.UserRepo

	// Notes stores local-only event notes (M2.8 Task 4). Notes never reach
	// the provider, so event write-through is untouched by them.
	Notes port.EventNoteRepo

	// --- Interesting-calendar ICS subscriptions (M2.8 Task 15). Both are
	// optional: left nil, the subscription surface answers ErrNotImplemented
	// and ListEvents serves provider/shared events only. ---
	CalendarSubs port.CalendarSubscriptionRepo
	IcsFetcher   port.IcsFetcher
}

// CalendarService implements port.CalendarService. Event mutations write
// through to the provider and update the local mirror optimistically.
type CalendarService struct {
	ent       entitlement
	accounts  port.AccountRepo
	calendars port.CalendarRepo
	events    port.EventRepo
	templates port.EventTemplateRepo
	sets      port.CalendarSetRepo
	cal       map[domain.Provider]port.CalendarProvider
	tokens    tokenSource
	clock     port.Clock
	settings  port.UserSettingsRepo
	shares    port.CalendarShareRepo
	audit     port.AuditRepo
	teams     port.TeamRepo
	users     port.UserRepo
	notes     port.EventNoteRepo

	calendarSubs port.CalendarSubscriptionRepo
	icsFetcher   port.IcsFetcher
}

var _ port.CalendarService = (*CalendarService)(nil)

func NewCalendarService(d CalendarServiceDeps) *CalendarService {
	return &CalendarService{
		ent:       entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:  d.Accounts,
		calendars: d.Calendars,
		events:    d.Events,
		templates: d.Templates,
		sets:      d.Sets,
		cal:       d.CalendarProviders,
		tokens:    tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		clock:     d.Clock,
		settings:  d.Settings,
		shares:    d.Shares,
		audit:     d.Audit,
		teams:     d.Teams,
		users:     d.Users,
		notes:     d.Notes,

		calendarSubs: d.CalendarSubs,
		icsFetcher:   d.IcsFetcher,
	}
}

func (s *CalendarService) ListCalendars(ctx context.Context, userID string) ([]domain.Calendar, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	cals, err := s.calendars.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cals == nil {
		cals = []domain.Calendar{}
	}
	if s.shares != nil {
		shared, err := s.sharedCalendars(ctx, userID)
		if err != nil {
			return nil, err
		}
		cals = append(cals, shared...)
	}
	return cals, nil
}

func (s *CalendarService) UpdateCalendar(ctx context.Context, userID, calendarID string, patch port.CalendarPatch) (domain.Calendar, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Calendar{}, err
	}
	c, _, err := s.ownedCalendar(ctx, userID, calendarID)
	if err != nil {
		return domain.Calendar{}, err
	}
	if patch.IsVisible != nil {
		c.IsVisible = *patch.IsVisible
	}
	if patch.Color != nil {
		c.Color = *patch.Color
	}
	if err := s.calendars.Update(ctx, c); err != nil {
		return domain.Calendar{}, err
	}
	return c, nil
}

// ListEvents serves from the local mirror; recurrences are expanded at sync
// time (providers are queried with single-instance expansion).
func (s *CalendarService) ListEvents(ctx context.Context, userID string, from, to time.Time, calendarIDs []string) ([]domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	evs, err := s.events.ListInRange(ctx, userID, from, to, calendarIDs)
	if err != nil {
		return nil, err
	}
	if evs == nil {
		evs = []domain.Event{}
	}
	if s.shares != nil {
		shared, err := s.sharedEvents(ctx, userID, from, to, calendarIDs)
		if err != nil {
			return nil, err
		}
		evs = append(evs, shared...)
	}
	// M2.8 Task 15: merge read-only ICS subscription events (visible feeds
	// only — the repo filters is_visible in SQL). They carry SubscriptionID
	// and never reach Availability, which queries the event repo directly.
	if s.calendarSubs != nil && len(calendarIDs) == 0 {
		subEvs, err := s.calendarSubs.ListEventsInRange(ctx, userID, from, to)
		if err != nil {
			return nil, err
		}
		evs = append(evs, subEvs...)
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].Start.Equal(evs[j].Start) {
			return evs[i].ID < evs[j].ID
		}
		return evs[i].Start.Before(evs[j].Start)
	})
	return evs, nil
}

func (s *CalendarService) CreateEvent(ctx context.Context, userID string, in domain.EventInput) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	if in.CalendarID == "" {
		return domain.Event{}, fmt.Errorf("%w: calendarId is required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.Event{}, fmt.Errorf("%w: title is required", domain.ErrValidation)
	}
	if !in.End.After(in.Start) {
		return domain.Event{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}
	c, acct, err := s.ownedCalendar(ctx, userID, in.CalendarID)
	if err != nil {
		// Not the owner's calendar: it may be shared with the caller as
		// editor (enforced in createSharedEvent; no share stays a 404).
		if s.shares != nil && errors.Is(err, domain.ErrNotFound) {
			return s.createSharedEvent(ctx, userID, in)
		}
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	ev := eventFromInput(in, c.ID)
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		created, err := provider.CreateEvent(ctx, token, c.ProviderCalendarID, in)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = c.ID
		carryLocalGeo(&created, ev)
		ev = created
	}
	return s.events.Upsert(ctx, ev)
}

func (s *CalendarService) UpdateEvent(ctx context.Context, userID, eventID string, patch domain.EventPatch) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		if s.shares != nil && errors.Is(err, domain.ErrNotFound) {
			return s.updateSharedEvent(ctx, userID, eventID, patch)
		}
		return domain.Event{}, err
	}
	if !c.CanWrite {
		return domain.Event{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if patch.Start != nil && patch.End != nil && !patch.End.After(*patch.Start) {
		return domain.Event{}, fmt.Errorf("%w: end must be after start", domain.ErrValidation)
	}

	applyEventPatch(&ev, patch)
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		updated, err := provider.UpdateEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID, patch)
		if err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		updated.ID = ev.ID
		updated.CalendarID = c.ID
		carryLocalGeo(&updated, ev)
		ev = updated
	}
	saved, err := s.events.Upsert(ctx, ev)
	if err != nil {
		return domain.Event{}, err
	}
	if err := s.clearStaleGeo(ctx, &saved, patch); err != nil {
		return domain.Event{}, err
	}
	return saved, nil
}

func (s *CalendarService) DeleteEvent(ctx context.Context, userID, eventID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		if s.shares != nil && errors.Is(err, domain.ErrNotFound) {
			return s.deleteSharedEvent(ctx, userID, eventID)
		}
		return err
	}
	if !c.CanWrite {
		return fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return err
		}
		if err := provider.DeleteEvent(ctx, token, c.ProviderCalendarID, ev.ProviderEventID); err != nil {
			return fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return s.events.Delete(ctx, ev.ID)
}

func (s *CalendarService) RSVP(ctx context.Context, userID, eventID string, response domain.RsvpStatus, comment string) (domain.Event, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Event{}, err
	}
	ev, c, acct, err := s.ownedEvent(ctx, userID, eventID)
	if err != nil {
		return domain.Event{}, err
	}
	if provider, ok := s.cal[acct.Provider]; ok && ev.ProviderEventID != "" {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.Event{}, err
		}
		if err := provider.RSVP(ctx, token, c.ProviderCalendarID, ev.ProviderEventID, response, comment); err != nil {
			return domain.Event{}, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	for i := range ev.Attendees {
		if strings.EqualFold(ev.Attendees[i].Email, acct.Email) {
			ev.Attendees[i].Response = response
		}
	}
	return s.events.Upsert(ctx, ev)
}

// Availability computes free windows across every calendar of the user
// between from and to: busy intervals (non-cancelled, non-all-day events the
// user has not declined) are merged, and each remaining gap of at least
// slotDuration is returned as one slot for the share-availability flow.
func (s *CalendarService) Availability(ctx context.Context, userID string, from, to time.Time, slotDuration time.Duration) ([]domain.AvailabilitySlot, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	if slotDuration <= 0 {
		return nil, fmt.Errorf("%w: duration must be positive", domain.ErrValidation)
	}
	evs, err := s.events.ListInRange(ctx, userID, from, to, nil)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ownEmails := make(map[string]struct{}, len(accounts))
	for _, a := range accounts {
		ownEmails[strings.ToLower(a.Email)] = struct{}{}
	}

	slots := []domain.AvailabilitySlot{}
	cursor := from
	for _, b := range mergedBusySlots(evs, ownEmails, from, to) {
		if b.Start.Sub(cursor) >= slotDuration {
			slots = append(slots, domain.AvailabilitySlot{Start: cursor, End: b.Start})
		}
		if b.End.After(cursor) {
			cursor = b.End
		}
	}
	if to.Sub(cursor) >= slotDuration {
		slots = append(slots, domain.AvailabilitySlot{Start: cursor, End: to})
	}

	if s.settings != nil {
		settings, err := s.settings.Get(ctx, userID)
		if err != nil {
			return nil, err
		}
		if len(settings.WorkingHours) > 0 {
			tz, err := time.LoadLocation(settings.TimeZone)
			if err != nil {
				tz = time.UTC
			}
			slots = intersectWindows(slots, settings.WorkingHours, tz)
		}
	}

	return slots, nil
}

// mergedBusySlots is Availability's busy-interval core, exposed as the
// inverse view for TeamAvailability: non-cancelled, non-all-day events the
// user has not declined, clamped to [from, to), sorted and merged into
// non-overlapping opaque {Start, End} blocks. All-day events are usually
// informational (birthdays, OOO banners) and must not blanket the whole day
// as busy; cancelled or user-declined events are not busy either.
func mergedBusySlots(evs []domain.Event, ownEmails map[string]struct{}, from, to time.Time) []domain.AvailabilitySlot {
	type interval struct{ start, end time.Time }
	busy := make([]interval, 0, len(evs))
	for _, ev := range evs {
		if ev.Status == domain.EventCancelled || ev.AllDay || declinedByUser(ev, ownEmails) {
			continue
		}
		start, end := ev.Start, ev.End
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if end.After(start) {
			busy = append(busy, interval{start, end})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	out := []domain.AvailabilitySlot{}
	for _, b := range busy {
		if n := len(out); n > 0 && !b.start.After(out[n-1].End) {
			if b.end.After(out[n-1].End) {
				out[n-1].End = b.end
			}
			continue
		}
		out = append(out, domain.AvailabilitySlot{Start: b.start, End: b.end})
	}
	return out
}

// maxTeamAvailabilitySpan caps a TeamAvailability query range: wide enough
// for a month view, small enough to bound the per-member event scans.
const maxTeamAvailabilitySpan = 35 * 24 * time.Hour

// TeamAvailability returns each team member's opaque busy blocks in
// [from, to). Privacy contract (M2.7 Task 13): the caller must be a team
// member (non-members get the membership primitive's 404, no existence
// oracle); a member contributes busy data only from calendars THEY granted
// to this exact team at >= free_busy (every valid permission qualifies —
// direct user-to-user grants are NOT a team opt-in); everyone else appears
// Shared=false with an empty list. Blocks are start/end only — titles and
// details never cross this boundary.
func (s *CalendarService) TeamAvailability(ctx context.Context, userID, teamID string, from, to time.Time) ([]port.MemberAvailability, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if s.teams == nil || s.shares == nil {
		return nil, domain.ErrNotImplemented
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	if to.Sub(from) > maxTeamAvailabilitySpan {
		return nil, fmt.Errorf("%w: range must not exceed 35 days", domain.ErrValidation)
	}
	if _, err := s.teams.GetMember(ctx, teamID, userID); err != nil {
		return nil, err
	}
	members, err := s.teams.ListMembers(ctx, teamID)
	if err != nil {
		return nil, err
	}

	// Calendars granted to THIS team, grouped by their owning member. The
	// grantee-side repo query is reused with only this team's id; direct
	// user grants that come back for the caller are filtered out — they are
	// a 1:1 share, not a team-availability opt-in.
	shares, err := s.shares.ListForGrantee(ctx, userID, []string{teamID})
	if err != nil {
		return nil, err
	}
	calsByOwner := map[string][]string{}
	seen := map[string]struct{}{}
	for _, sh := range shares {
		if sh.GranteeTeamID == nil || *sh.GranteeTeamID != teamID {
			continue
		}
		if _, dup := seen[sh.CalendarID]; dup {
			continue
		}
		seen[sh.CalendarID] = struct{}{}
		_, owner, err := s.calendarOwner(ctx, sh.CalendarID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue // dangling grant; DB cascade normally prevents this
			}
			return nil, err
		}
		calsByOwner[owner.UserID] = append(calsByOwner[owner.UserID], sh.CalendarID)
	}

	out := make([]port.MemberAvailability, 0, len(members))
	for _, m := range members {
		row := port.MemberAvailability{UserID: m.UserID, Busy: []domain.AvailabilitySlot{}}
		calIDs := calsByOwner[m.UserID]
		if len(calIDs) == 0 {
			out = append(out, row) // not sharing — visibly opted out, zero data
			continue
		}
		row.Shared = true
		evs, err := s.events.ListInRange(ctx, m.UserID, from, to, calIDs)
		if err != nil {
			return nil, err
		}
		accounts, err := s.accounts.ListByUser(ctx, m.UserID)
		if err != nil {
			return nil, err
		}
		ownEmails := make(map[string]struct{}, len(accounts))
		for _, a := range accounts {
			ownEmails[strings.ToLower(a.Email)] = struct{}{}
		}
		row.Busy = mergedBusySlots(evs, ownEmails, from, to)
		out = append(out, row)
	}
	return out, nil
}

// --- Event Template methods ---

func (s *CalendarService) ListEventTemplates(ctx context.Context, userID string) ([]domain.EventTemplate, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	templates, err := s.templates.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if templates == nil {
		templates = []domain.EventTemplate{}
	}
	return templates, nil
}

func (s *CalendarService) CreateEventTemplate(ctx context.Context, userID string, in domain.EventTemplateInput) (domain.EventTemplate, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.EventTemplate{}, err
	}

	// Validate Name (required, trimmed non-empty)
	if strings.TrimSpace(in.Name) == "" {
		return domain.EventTemplate{}, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}

	// Validate DurationMinutes (> 0, default to 30 when 0)
	duration := in.DurationMinutes
	if duration == 0 {
		duration = 30
	} else if duration <= 0 {
		return domain.EventTemplate{}, fmt.Errorf("%w: durationMinutes must be greater than 0", domain.ErrValidation)
	}

	// A pointer to "" (rather than a nil pointer) can arrive when the JSON
	// body sends `"calendarId": ""` instead of omitting the field - treat it
	// the same as "no calendar override" by normalizing to nil BEFORE
	// validation/persist, so it never reaches ownedCalendar (unknown-id error)
	// or gets stored as a non-nil empty-string pointer.
	if in.CalendarID != nil && *in.CalendarID == "" {
		in.CalendarID = nil
	}
	if in.CalendarID != nil {
		_, _, err := s.ownedCalendar(ctx, userID, *in.CalendarID)
		if err != nil {
			return domain.EventTemplate{}, err
		}
	}

	template := domain.EventTemplate{
		ID:              newID(),
		Name:            strings.TrimSpace(in.Name),
		Title:           in.Title,
		Description:     in.Description,
		Location:        in.Location,
		DurationMinutes: duration,
		AllDay:          in.AllDay,
		CalendarID:      in.CalendarID,
		AttendeeEmails:  in.AttendeeEmails,
		AddConferencing: in.AddConferencing,
		ReminderMinutes: in.ReminderMinutes,
		RecurrenceRule:  in.RecurrenceRule,
		UsageCount:      0,
	}
	if template.ReminderMinutes == nil {
		template.ReminderMinutes = []int{}
	}

	return s.templates.Create(ctx, userID, template)
}

func (s *CalendarService) UpdateEventTemplate(ctx context.Context, userID, templateID string, in domain.EventTemplateInput) (domain.EventTemplate, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.EventTemplate{}, err
	}

	// Get template and verify ownership
	template, ownerUserID, err := s.templates.GetByID(ctx, templateID)
	if err != nil {
		return domain.EventTemplate{}, err
	}
	if ownerUserID != userID {
		return domain.EventTemplate{}, domain.ErrNotFound
	}

	// Validate Name (required, trimmed non-empty)
	if strings.TrimSpace(in.Name) == "" {
		return domain.EventTemplate{}, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}

	// Validate DurationMinutes (> 0, default to 30 when 0)
	duration := in.DurationMinutes
	if duration == 0 {
		duration = 30
	} else if duration <= 0 {
		return domain.EventTemplate{}, fmt.Errorf("%w: durationMinutes must be greater than 0", domain.ErrValidation)
	}

	// Same empty-string-pointer normalization as CreateEventTemplate above.
	if in.CalendarID != nil && *in.CalendarID == "" {
		in.CalendarID = nil
	}
	if in.CalendarID != nil {
		_, _, err := s.ownedCalendar(ctx, userID, *in.CalendarID)
		if err != nil {
			return domain.EventTemplate{}, err
		}
	}

	// Apply full replace update
	template.Name = strings.TrimSpace(in.Name)
	template.Title = in.Title
	template.Description = in.Description
	template.Location = in.Location
	template.DurationMinutes = duration
	template.AllDay = in.AllDay
	template.CalendarID = in.CalendarID
	template.AttendeeEmails = in.AttendeeEmails
	template.AddConferencing = in.AddConferencing
	template.ReminderMinutes = in.ReminderMinutes
	template.RecurrenceRule = in.RecurrenceRule
	if template.ReminderMinutes == nil {
		template.ReminderMinutes = []int{}
	}

	if err := s.templates.Update(ctx, template); err != nil {
		return domain.EventTemplate{}, err
	}

	return template, nil
}

func (s *CalendarService) DeleteEventTemplate(ctx context.Context, userID, templateID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}

	// Get template and verify ownership
	_, ownerUserID, err := s.templates.GetByID(ctx, templateID)
	if err != nil {
		return err
	}
	if ownerUserID != userID {
		return domain.ErrNotFound
	}

	return s.templates.Delete(ctx, templateID)
}

func (s *CalendarService) UseEventTemplate(ctx context.Context, userID, templateID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}

	// Verify ownership
	_, ownerUserID, err := s.templates.GetByID(ctx, templateID)
	if err != nil {
		return err
	}
	if ownerUserID != userID {
		return domain.ErrNotFound
	}

	return s.templates.IncrementUsage(ctx, templateID)
}

// --- Calendar Set methods ---

func (s *CalendarService) ListCalendarSets(ctx context.Context, userID string) ([]domain.CalendarSet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	sets, err := s.sets.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sets == nil {
		sets = []domain.CalendarSet{}
	}
	return sets, nil
}

func (s *CalendarService) CreateCalendarSet(ctx context.Context, userID string, in domain.CalendarSetInput) (domain.CalendarSet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarSet{}, err
	}

	// Validate Name (required)
	if strings.TrimSpace(in.Name) == "" {
		return domain.CalendarSet{}, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}

	// Validate CalendarIDs: each must belong to user
	userCalendars, err := s.calendars.ListByUser(ctx, userID)
	if err != nil {
		return domain.CalendarSet{}, err
	}
	userCalendarMap := make(map[string]struct{})
	for _, cal := range userCalendars {
		userCalendarMap[cal.ID] = struct{}{}
	}

	// De-dupe while preserving order
	seenIDs := make(map[string]struct{})
	deduped := []string{}
	for _, id := range in.CalendarIDs {
		if _, seen := seenIDs[id]; !seen {
			seenIDs[id] = struct{}{}
			// Validate: unknown id or empty string → ErrValidation
			if _, exists := userCalendarMap[id]; !exists {
				return domain.CalendarSet{}, fmt.Errorf("%w: calendar %s not found", domain.ErrValidation, id)
			}
			deduped = append(deduped, id)
		}
	}

	calendarSet := domain.CalendarSet{
		ID:          newID(),
		Name:        strings.TrimSpace(in.Name),
		CalendarIDs: deduped,
		Position:    in.Position,
	}

	return s.sets.Create(ctx, userID, calendarSet)
}

func (s *CalendarService) UpdateCalendarSet(ctx context.Context, userID, setID string, in domain.CalendarSetInput) (domain.CalendarSet, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarSet{}, err
	}

	// Get set and verify ownership
	calendarSet, ownerUserID, err := s.sets.GetByID(ctx, setID)
	if err != nil {
		return domain.CalendarSet{}, err
	}
	if ownerUserID != userID {
		return domain.CalendarSet{}, domain.ErrNotFound
	}

	// Validate Name (required)
	if strings.TrimSpace(in.Name) == "" {
		return domain.CalendarSet{}, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}

	// Validate CalendarIDs: each must belong to user
	userCalendars, err := s.calendars.ListByUser(ctx, userID)
	if err != nil {
		return domain.CalendarSet{}, err
	}
	userCalendarMap := make(map[string]struct{})
	for _, cal := range userCalendars {
		userCalendarMap[cal.ID] = struct{}{}
	}

	// De-dupe while preserving order
	seenIDs := make(map[string]struct{})
	deduped := []string{}
	for _, id := range in.CalendarIDs {
		if _, seen := seenIDs[id]; !seen {
			seenIDs[id] = struct{}{}
			// Validate: unknown id or empty string → ErrValidation
			if _, exists := userCalendarMap[id]; !exists {
				return domain.CalendarSet{}, fmt.Errorf("%w: calendar %s not found", domain.ErrValidation, id)
			}
			deduped = append(deduped, id)
		}
	}

	// Apply full replace update
	calendarSet.Name = strings.TrimSpace(in.Name)
	calendarSet.CalendarIDs = deduped
	calendarSet.Position = in.Position

	if err := s.sets.Update(ctx, calendarSet); err != nil {
		return domain.CalendarSet{}, err
	}

	return calendarSet, nil
}

func (s *CalendarService) DeleteCalendarSet(ctx context.Context, userID, setID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}

	// Get set and verify ownership
	_, ownerUserID, err := s.sets.GetByID(ctx, setID)
	if err != nil {
		return err
	}
	if ownerUserID != userID {
		return domain.ErrNotFound
	}

	return s.sets.Delete(ctx, setID)
}

// --- helpers ---

func (s *CalendarService) ownedCalendar(ctx context.Context, userID, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, err := s.calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	acct, err := s.accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	if acct.UserID != userID {
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return c, acct, nil
}

func (s *CalendarService) ownedEvent(ctx context.Context, userID, eventID string) (domain.Event, domain.Calendar, domain.ConnectedAccount, error) {
	ev, err := s.events.GetByID(ctx, eventID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	c, acct, err := s.ownedCalendar(ctx, userID, ev.CalendarID)
	if err != nil {
		return domain.Event{}, domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	return ev, c, acct, nil
}

// --- Event notes (M2.8 Task 4) ----------------------------------------------
//
// Notes are local-only user content: they are never written through to the
// provider (no provider payload carries them), so they survive provider
// syncs; the mirror row's ON DELETE CASCADE is their only lifecycle tie.

func (s *CalendarService) GetEventNote(ctx context.Context, userID, eventID string) (domain.EventNote, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.EventNote{}, err
	}
	if _, _, _, err := s.ownedEvent(ctx, userID, eventID); err != nil {
		return domain.EventNote{}, err
	}
	n, err := s.notes.GetByEventID(ctx, eventID)
	if errors.Is(err, domain.ErrNotFound) {
		// Missing note is an empty note, never a 404: the UI needs no
		// special case.
		return domain.EventNote{EventID: eventID, UserID: userID, Links: []string{}}, nil
	}
	if err != nil {
		return domain.EventNote{}, err
	}
	if n.Links == nil {
		n.Links = []string{}
	}
	return n, nil
}

func (s *CalendarService) PutEventNote(ctx context.Context, userID, eventID string, bodyMD string, links []string) (domain.EventNote, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.EventNote{}, err
	}
	if _, _, _, err := s.ownedEvent(ctx, userID, eventID); err != nil {
		return domain.EventNote{}, err
	}
	// Links are user content rendered as clickable "open" affordances:
	// restrict them to absolute http(s) URLs so a stored javascript: (or
	// other scheme) link can never become an XSS vector client-side.
	clean := make([]string, 0, len(links))
	for _, raw := range links {
		link := strings.TrimSpace(raw)
		if link == "" {
			continue
		}
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return domain.EventNote{}, fmt.Errorf("%w: link %q must be an absolute http(s) URL", domain.ErrValidation, raw)
		}
		clean = append(clean, link)
	}
	n, err := s.notes.Upsert(ctx, domain.EventNote{
		EventID: eventID,
		UserID:  userID,
		BodyMD:  bodyMD,
		Links:   clean,
	})
	if err != nil {
		return domain.EventNote{}, err
	}
	if n.Links == nil {
		n.Links = []string{}
	}
	return n, nil
}

func eventFromInput(in domain.EventInput, calendarID string) domain.Event {
	ev := domain.Event{
		ID:              newID(),
		CalendarID:      calendarID,
		Title:           in.Title,
		Start:           in.Start,
		End:             in.End,
		AllDay:          in.AllDay,
		Attendees:       []domain.Attendee{},
		Status:          domain.EventConfirmed,
		Visibility:      domain.VisibilityDefault,
		ReminderMinutes: in.ReminderMinutes,
	}
	if ev.ReminderMinutes == nil {
		ev.ReminderMinutes = []int{}
	}
	if in.Description != "" {
		ev.Description = ptr(in.Description)
	}
	if in.Location != "" {
		ev.Location = ptr(in.Location)
	}
	if in.RecurrenceRule != "" {
		ev.RecurrenceRule = ptr(in.RecurrenceRule)
	}
	for _, email := range in.AttendeeEmails {
		ev.Attendees = append(ev.Attendees, domain.Attendee{Email: email, Response: domain.RsvpNeedsAction})
	}
	// Coordinates arrive only when the location was picked from maps
	// autocomplete; free-typed locations keep them nil (travel features
	// skip such events).
	ev.LocationLat = in.LocationLat
	ev.LocationLon = in.LocationLon
	return ev
}

// carryLocalGeo re-applies the local-only coordinate fields after a provider
// write-through: providers know nothing about locationLat/Lon, so the event
// they return must not erase coordinates chosen locally.
func carryLocalGeo(dst *domain.Event, src domain.Event) {
	dst.LocationLat = src.LocationLat
	dst.LocationLon = src.LocationLon
}

func applyEventPatch(ev *domain.Event, patch domain.EventPatch) {
	if patch.Title != nil {
		ev.Title = *patch.Title
	}
	if patch.Description != nil {
		ev.Description = patch.Description
	}
	if patch.Location != nil {
		ev.Location = patch.Location
		if patchClearsGeo(patch) {
			// A location edit without a fresh autocomplete pick invalidates
			// the stored coordinates — they describe the OLD location (M2.8
			// Task 12 carry-forward).
			ev.LocationLat, ev.LocationLon = nil, nil
		}
	}
	if patch.Start != nil {
		ev.Start = *patch.Start
	}
	if patch.End != nil {
		ev.End = *patch.End
	}
	if patch.AllDay != nil {
		ev.AllDay = *patch.AllDay
	}
	if patch.RecurrenceRule != nil {
		ev.RecurrenceRule = patch.RecurrenceRule
	}
	if patch.AttendeeEmails != nil {
		attendees := make([]domain.Attendee, 0, len(*patch.AttendeeEmails))
		for _, email := range *patch.AttendeeEmails {
			response := domain.RsvpNeedsAction
			for _, existing := range ev.Attendees {
				if strings.EqualFold(existing.Email, email) {
					response = existing.Response
					break
				}
			}
			attendees = append(attendees, domain.Attendee{Email: email, Response: response})
		}
		ev.Attendees = attendees
	}
	if patch.ReminderMinutes != nil {
		ev.ReminderMinutes = *patch.ReminderMinutes
	}
	if patch.LocationLat != nil {
		ev.LocationLat = patch.LocationLat
	}
	if patch.LocationLon != nil {
		ev.LocationLon = patch.LocationLon
	}
}

// patchClearsGeo reports whether the patch edits the location text without
// supplying fresh coordinates. The stored coordinates then belong to the OLD
// location and must be cleared rather than trusted (M2.8 Task 12 carry-
// forward): on a location edit, the absence of a new autocomplete pick IS
// the clear signal — the same clearing-sentinel convention as
// recurrenceRule "" in this PATCH path.
func patchClearsGeo(patch domain.EventPatch) bool {
	return patch.Location != nil && patch.LocationLat == nil && patch.LocationLon == nil
}

// clearStaleGeo nulls persisted coordinates after a geo-clearing patch.
// EventRepo.Upsert COALESCE-preserves coordinates on NULL input (so
// coordinate-less provider syncs cannot wipe them), which means a deliberate
// clear needs this targeted follow-up write.
func (s *CalendarService) clearStaleGeo(ctx context.Context, ev *domain.Event, patch domain.EventPatch) error {
	if !patchClearsGeo(patch) {
		return nil
	}
	if err := s.events.ClearGeo(ctx, ev.ID); err != nil {
		return err
	}
	ev.LocationLat, ev.LocationLon = nil, nil
	return nil
}

func declinedByUser(ev domain.Event, ownEmails map[string]struct{}) bool {
	for _, a := range ev.Attendees {
		if _, ok := ownEmails[strings.ToLower(a.Email)]; ok {
			return a.Response == domain.RsvpDeclined
		}
	}
	return false
}
