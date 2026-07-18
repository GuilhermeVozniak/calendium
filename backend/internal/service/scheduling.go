package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// defaultMaxAdvanceDays is used whenever a booking link leaves MaxAdvanceDays
// at its zero value.
const defaultMaxAdvanceDays = 60

// maxPublicSlotsWindow bounds how far apart from/to may be on a single
// PublicSlots query, independent of the link's own MaxAdvanceDays.
const maxPublicSlotsWindow = 31 * 24 * time.Hour

// SchedulingServiceDeps wires a SchedulingService. Only the fields consumed
// by the booking-link CRUD, public booking page, and slot engine (this file)
// are stored today; Polls/Proposals/Tx/CalendarProviders/MailProviders/OAuth/
// PublicWebURL are carried here so later scheduling work (Book, meeting
// polls, propose-new-time, guest free/busy — separate tasks) can extend
// NewSchedulingService's construction without changing this struct's shape.
type SchedulingServiceDeps struct {
	Subscriptions     port.SubscriptionRepo
	Users             port.UserRepo
	Accounts          port.AccountRepo
	Calendars         port.CalendarRepo
	Events            port.EventRepo
	Links             port.BookingLinkRepo
	Bookings          port.BookingRepo
	Polls             port.PollRepo
	Proposals         port.TimeProposalRepo
	Settings          port.UserSettingsRepo
	Tx                port.TxRunner
	CalendarProviders map[domain.Provider]port.CalendarProvider
	MailProviders     map[domain.Provider]port.MailProvider
	OAuth             map[domain.Provider]port.OAuthGateway
	Clock             port.Clock
	SelfHosted        bool
	// PublicWebURL builds booking/poll URLs in emails (config.PublicWebURL).
	PublicWebURL string
}

// SchedulingService implements the booking-link surface of
// port.SchedulingService (CRUD, public page, public slots). Book, meeting
// polls, propose-new-time, and guest free/busy are added by later tasks in
// additional files in this package.
type SchedulingService struct {
	ent       entitlement
	users     port.UserRepo
	accounts  port.AccountRepo
	calendars port.CalendarRepo
	events    port.EventRepo
	links     port.BookingLinkRepo
	bookings  port.BookingRepo
	settings  port.UserSettingsRepo
	polls     port.PollRepo
	cal       map[domain.Provider]port.CalendarProvider
	mail      map[domain.Provider]port.MailProvider
	tokens    tokenSource
	clock     port.Clock
}

func NewSchedulingService(d SchedulingServiceDeps) *SchedulingService {
	return &SchedulingService{
		ent:       entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		users:     d.Users,
		accounts:  d.Accounts,
		calendars: d.Calendars,
		events:    d.Events,
		links:     d.Links,
		bookings:  d.Bookings,
		settings:  d.Settings,
		polls:     d.Polls,
		cal:       d.CalendarProviders,
		mail:      d.MailProviders,
		tokens:    tokenSource{accounts: d.Accounts, oauth: d.OAuth, clock: d.Clock},
		clock:     d.Clock,
	}
}

// --- booking-link CRUD --------------------------------------------------------

func validateBookingLinkInput(in port.BookingLinkInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return fmt.Errorf("%w: title is required", domain.ErrValidation)
	}
	if err := domain.ValidateSlug(in.Slug); err != nil {
		return err
	}
	if in.DurationMinutes <= 0 {
		return fmt.Errorf("%w: durationMinutes must be greater than 0", domain.ErrValidation)
	}
	if _, err := time.LoadLocation(in.TimeZone); err != nil {
		return fmt.Errorf("%w: invalid timeZone %q", domain.ErrValidation, in.TimeZone)
	}
	for _, w := range in.Windows {
		if err := w.Validate(); err != nil {
			return err
		}
	}
	if in.BufferBeforeMin < 0 || in.BufferAfterMin < 0 || in.DailyLimit < 0 ||
		in.MinNoticeMin < 0 || in.MaxAdvanceDays < 0 {
		return fmt.Errorf("%w: buffer/limit/notice/advance fields must be non-negative", domain.ErrValidation)
	}
	return nil
}

func bookingLinkFromInput(in port.BookingLinkInput) domain.BookingLink {
	l := domain.BookingLink{
		Slug:                in.Slug,
		Title:               in.Title,
		CalendarID:          in.CalendarID,
		DurationMinutes:     in.DurationMinutes,
		TimeZone:            in.TimeZone,
		Windows:             in.Windows,
		BufferBeforeMin:     in.BufferBeforeMin,
		BufferAfterMin:      in.BufferAfterMin,
		DailyLimit:          in.DailyLimit,
		MinNoticeMin:        in.MinNoticeMin,
		MaxAdvanceDays:      in.MaxAdvanceDays,
		RespectWorkingHours: in.RespectWorkingHours,
		AddConferencing:     in.AddConferencing,
		Active:              in.Active,
	}
	if l.Windows == nil {
		l.Windows = []domain.AvailabilityWindow{}
	}
	if in.Description != "" {
		l.Description = ptr(in.Description)
	}
	return l
}

// ownedWritableCalendar loads a calendar and enforces that it belongs to
// userID (via its account) and is writable; foreign/missing calendars are
// indistinguishable (404, never 403), matching CalendarService.ownedCalendar.
func (s *SchedulingService) ownedWritableCalendar(ctx context.Context, userID, calendarID string) (domain.Calendar, error) {
	c, err := s.calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, err
	}
	acct, err := s.accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, err
	}
	if acct.UserID != userID {
		return domain.Calendar{}, domain.ErrNotFound
	}
	if !c.CanWrite {
		return domain.Calendar{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}
	return c, nil
}

// ownedLink loads a booking link and enforces ownership; a foreign link is
// indistinguishable from a missing one.
func (s *SchedulingService) ownedLink(ctx context.Context, userID, linkID string) (domain.BookingLink, error) {
	l, err := s.links.GetByID(ctx, linkID)
	if err != nil {
		return domain.BookingLink{}, err
	}
	if l.UserID != userID {
		return domain.BookingLink{}, domain.ErrNotFound
	}
	return l, nil
}

func (s *SchedulingService) CreateLink(ctx context.Context, userID string, in port.BookingLinkInput) (domain.BookingLink, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.BookingLink{}, err
	}
	if err := validateBookingLinkInput(in); err != nil {
		return domain.BookingLink{}, err
	}
	if _, err := s.ownedWritableCalendar(ctx, userID, in.CalendarID); err != nil {
		return domain.BookingLink{}, err
	}
	link := bookingLinkFromInput(in)
	link.ID = newID()
	link.UserID = userID
	return s.links.Create(ctx, link)
}

func (s *SchedulingService) UpdateLink(ctx context.Context, userID, linkID string, in port.BookingLinkInput) (domain.BookingLink, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.BookingLink{}, err
	}
	existing, err := s.ownedLink(ctx, userID, linkID)
	if err != nil {
		return domain.BookingLink{}, err
	}
	if err := validateBookingLinkInput(in); err != nil {
		return domain.BookingLink{}, err
	}
	if _, err := s.ownedWritableCalendar(ctx, userID, in.CalendarID); err != nil {
		return domain.BookingLink{}, err
	}
	updated := bookingLinkFromInput(in)
	updated.ID = existing.ID
	updated.UserID = userID
	updated.CreatedAt = existing.CreatedAt
	if err := s.links.Update(ctx, updated); err != nil {
		return domain.BookingLink{}, err
	}
	return updated, nil
}

func (s *SchedulingService) ListLinks(ctx context.Context, userID string) ([]domain.BookingLink, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	links, err := s.links.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if links == nil {
		links = []domain.BookingLink{}
	}
	return links, nil
}

func (s *SchedulingService) DeleteLink(ctx context.Context, userID, linkID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	if _, err := s.ownedLink(ctx, userID, linkID); err != nil {
		return err
	}
	return s.links.Delete(ctx, linkID)
}

// --- public booking page + slots ----------------------------------------------

// ownerDisplayName falls back to the account email when the user has not set
// a display name (User.Name is nil/blank).
func ownerDisplayName(u domain.User) string {
	if u.Name != nil && strings.TrimSpace(*u.Name) != "" {
		return *u.Name
	}
	return u.Email
}

func (s *SchedulingService) PublicPage(ctx context.Context, slug string) (port.PublicBookingPage, error) {
	link, err := s.links.GetBySlug(ctx, slug)
	if err != nil {
		return port.PublicBookingPage{}, err
	}
	if !link.Active {
		return port.PublicBookingPage{}, domain.ErrNotFound
	}
	owner, err := s.users.GetByID(ctx, link.UserID)
	if err != nil {
		return port.PublicBookingPage{}, err
	}
	return port.PublicBookingPage{
		Slug:            link.Slug,
		Title:           link.Title,
		Description:     link.Description,
		OwnerName:       ownerDisplayName(owner),
		DurationMinutes: link.DurationMinutes,
		TimeZone:        link.TimeZone,
	}, nil
}

// ownerBusy computes busy intervals for the link owner from the local event
// mirror across all their calendars (the same source CalendarService.
// Availability draws on), excluding cancelled/all-day/declined-by-owner
// events.
func (s *SchedulingService) ownerBusy(ctx context.Context, ownerUserID string, from, to time.Time) ([]domain.BusyInterval, error) {
	evs, err := s.events.ListInRange(ctx, ownerUserID, from, to, nil)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accounts.ListByUser(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	ownEmails := make(map[string]struct{}, len(accounts))
	for _, a := range accounts {
		ownEmails[strings.ToLower(a.Email)] = struct{}{}
	}
	busy := make([]domain.BusyInterval, 0, len(evs))
	for _, ev := range evs {
		if ev.Status == domain.EventCancelled || ev.AllDay || declinedByUser(ev, ownEmails) {
			continue
		}
		busy = append(busy, domain.BusyInterval{Start: ev.Start, End: ev.End})
	}
	return busy, nil
}

// PublicSlots returns bookable start times between from and to: the query
// window is capped at 31 days, and the effective upper bound is clamped to
// the link's own MaxAdvanceDays (0 = default 60) measured from now.
func (s *SchedulingService) PublicSlots(ctx context.Context, slug string, from, to time.Time) ([]domain.AvailabilitySlot, error) {
	link, err := s.links.GetBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !link.Active {
		return nil, domain.ErrNotFound
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: `to` must be after `from`", domain.ErrValidation)
	}
	if to.Sub(from) > maxPublicSlotsWindow {
		return nil, fmt.Errorf("%w: query window must be at most 31 days", domain.ErrValidation)
	}

	now := s.clock.Now()
	maxAdvanceDays := link.MaxAdvanceDays
	if maxAdvanceDays <= 0 {
		maxAdvanceDays = defaultMaxAdvanceDays
	}
	if advanceLimit := now.AddDate(0, 0, maxAdvanceDays); to.After(advanceLimit) {
		to = advanceLimit
	}
	if !to.After(from) {
		return []domain.AvailabilitySlot{}, nil
	}

	settings, err := s.settings.Get(ctx, link.UserID)
	if err != nil {
		return nil, err
	}
	busy, err := s.ownerBusy(ctx, link.UserID, from, to)
	if err != nil {
		return nil, err
	}
	active, err := s.bookings.ListActiveInRange(ctx, link.ID, from, to)
	if err != nil {
		return nil, err
	}

	return computeSlots(link, settings, busy, active, from, to, now), nil
}

// --- pure slot engine ----------------------------------------------------------

// windowInstant resolves an AvailabilityWindow ("HH:MM"-"HH:MM") to concrete
// [start,end) instants on the given calendar day in loc. time.Date normalizes
// wall-clock time within loc, so this is DST-correct: a 09:00 start on a
// spring-forward day resolves to the true UTC instant for that day's offset.
func windowInstant(day time.Time, w domain.AvailabilityWindow, loc *time.Location) (start, end time.Time, ok bool) {
	st, err := time.Parse("15:04", w.Start)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	en, err := time.Parse("15:04", w.End)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	start = time.Date(day.Year(), day.Month(), day.Day(), st.Hour(), st.Minute(), 0, 0, loc)
	end = time.Date(day.Year(), day.Month(), day.Day(), en.Hour(), en.Minute(), 0, 0, loc)
	return start, end, true
}

// clipToWindows returns the portion(s) of slot that overlap a recurring
// weekly window (interpreted in loc), walking every calendar day the slot
// spans. A slot spanning multiple matching windows yields multiple pieces.
func clipToWindows(slot domain.AvailabilitySlot, windows []domain.AvailabilityWindow, loc *time.Location) []domain.AvailabilitySlot {
	var out []domain.AvailabilitySlot
	startLocal := slot.Start.In(loc)
	endLocal := slot.End.In(loc)
	day := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, loc)
	for !day.After(endLocal) {
		for _, w := range windows {
			if w.Weekday != int(day.Weekday()) {
				continue
			}
			wStart, wEnd, ok := windowInstant(day, w, loc)
			if !ok {
				continue
			}
			start, end := slot.Start, slot.End
			if wStart.After(start) {
				start = wStart
			}
			if wEnd.Before(end) {
				end = wEnd
			}
			if end.After(start) {
				out = append(out, domain.AvailabilitySlot{Start: start, End: end})
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// intersectWindows clips every slot down to the portion(s) overlapping
// windows (interpreted in tz); slots outside every window are dropped
// entirely. An empty windows list is "no constraint" and returns slots
// unchanged. Shared by CalendarService.Availability (arbitrary-length free
// gaps, kept as clipped fragments) and computeSlots (fixed-duration
// candidates, which additionally keeps only fully-contained results — see
// computeSlots).
func intersectWindows(slots []domain.AvailabilitySlot, windows []domain.AvailabilityWindow, tz *time.Location) []domain.AvailabilitySlot {
	if len(windows) == 0 {
		return slots
	}
	out := make([]domain.AvailabilitySlot, 0, len(slots))
	for _, sl := range slots {
		out = append(out, clipToWindows(sl, windows, tz)...)
	}
	return out
}

func filterSlots(slots []domain.AvailabilitySlot, keep func(domain.AvailabilitySlot) bool) []domain.AvailabilitySlot {
	out := make([]domain.AvailabilitySlot, 0, len(slots))
	for _, s := range slots {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

// computeSlots is the pure slot engine: it discretizes link.Windows (in
// link.TimeZone, DST-correct) into duration-sized starts stepping every
// duration, drops slots violating minNotice/maxAdvance or falling outside
// [from,to), subtracts owner busy events padded by the link's buffers,
// subtracts active (hold+confirmed) bookings, enforces DailyLimit (confirmed
// bookings per link-TZ calendar day — a day at/over the limit is hidden
// entirely), and — when link.RespectWorkingHours and settings.WorkingHours is
// set — keeps only slots fully contained in a working-hours window (in
// settings.TimeZone, which may differ from link.TimeZone).
func computeSlots(link domain.BookingLink, settings domain.UserSettings,
	busy []domain.BusyInterval, active []domain.Booking,
	from, to, now time.Time,
) []domain.AvailabilitySlot {
	loc, err := time.LoadLocation(link.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	duration := time.Duration(link.DurationMinutes) * time.Minute
	if duration <= 0 {
		return []domain.AvailabilitySlot{}
	}

	maxAdvanceDays := link.MaxAdvanceDays
	if maxAdvanceDays <= 0 {
		maxAdvanceDays = defaultMaxAdvanceDays
	}
	advanceLimit := now.AddDate(0, 0, maxAdvanceDays)
	noticeLimit := now.Add(time.Duration(link.MinNoticeMin) * time.Minute)

	effectiveTo := to
	if advanceLimit.Before(effectiveTo) {
		effectiveTo = advanceLimit
	}
	if !effectiveTo.After(from) {
		return []domain.AvailabilitySlot{}
	}

	// Discretize every link window on every day the query spans (in the
	// link's own time zone) into duration-sized candidate starts.
	startLocal := from.In(loc)
	endLocal := effectiveTo.In(loc)
	day := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 0, 0, 0, 0, loc)

	var candidates []domain.AvailabilitySlot
	for !day.After(endLocal) {
		for _, w := range link.Windows {
			if w.Weekday != int(day.Weekday()) {
				continue
			}
			wStart, wEnd, ok := windowInstant(day, w, loc)
			if !ok {
				continue
			}
			for cur := wStart; !cur.Add(duration).After(wEnd); cur = cur.Add(duration) {
				slot := domain.AvailabilitySlot{Start: cur, End: cur.Add(duration)}
				if slot.Start.Before(from) || slot.End.After(effectiveTo) {
					continue
				}
				if slot.Start.Before(noticeLimit) {
					continue
				}
				candidates = append(candidates, slot)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Start.Before(candidates[j].Start) })

	// Subtract owner busy events, padded by the link's buffers.
	before := time.Duration(link.BufferBeforeMin) * time.Minute
	after := time.Duration(link.BufferAfterMin) * time.Minute
	candidates = filterSlots(candidates, func(s domain.AvailabilitySlot) bool {
		for _, b := range busy {
			paddedStart, paddedEnd := b.Start.Add(-before), b.End.Add(after)
			if s.Start.Before(paddedEnd) && paddedStart.Before(s.End) {
				return false
			}
		}
		return true
	})

	// Subtract active (hold or confirmed) bookings.
	candidates = filterSlots(candidates, func(s domain.AvailabilitySlot) bool {
		for _, b := range active {
			if b.Status == domain.BookingCancelled {
				continue
			}
			if s.Start.Before(b.End) && b.Start.Before(s.End) {
				return false
			}
		}
		return true
	})

	// Enforce DailyLimit: a link-TZ calendar day with confirmedPerDay >=
	// DailyLimit is hidden entirely (every candidate slot on that day drops).
	if link.DailyLimit > 0 {
		confirmedPerDay := map[string]int{}
		for _, b := range active {
			if b.Status != domain.BookingConfirmed {
				continue
			}
			confirmedPerDay[b.Start.In(loc).Format("2006-01-02")]++
		}
		candidates = filterSlots(candidates, func(s domain.AvailabilitySlot) bool {
			return confirmedPerDay[s.Start.In(loc).Format("2006-01-02")] < link.DailyLimit
		})
	}

	// Optionally intersect with working hours, in settings.TimeZone (which
	// may differ from the link's own TimeZone). A candidate survives only
	// when fully contained in a working window — booking slots can't be
	// shortened the way a free-gap slot can.
	if link.RespectWorkingHours && len(settings.WorkingHours) > 0 {
		wtz, err := time.LoadLocation(settings.TimeZone)
		if err != nil {
			wtz = time.UTC
		}
		candidates = filterSlots(candidates, func(s domain.AvailabilitySlot) bool {
			for _, p := range clipToWindows(s, settings.WorkingHours, wtz) {
				if p.Start.Equal(s.Start) && p.End.Equal(s.End) {
					return true
				}
			}
			return false
		})
	}

	if candidates == nil {
		candidates = []domain.AvailabilitySlot{}
	}
	return candidates
}

const (
	minPollOptions   = 2
	maxPollOptions   = 10
	pollTokenBytes   = 16 // 32 hex chars
	maxTokenAttempts = 5
)

// ownedCalendarByID loads a calendar and enforces ownership via its account,
// mirroring CalendarService.ownedCalendar (kept package-level here so it can
// be shared without reaching into another task's file).

func ownedCalendarByID(ctx context.Context, calendars port.CalendarRepo, accounts port.AccountRepo, userID, calendarID string) (domain.Calendar, domain.ConnectedAccount, error) {
	c, err := calendars.GetByID(ctx, calendarID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	acct, err := accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return domain.Calendar{}, domain.ConnectedAccount{}, err
	}
	if acct.UserID != userID {
		return domain.Calendar{}, domain.ConnectedAccount{}, domain.ErrNotFound
	}
	return c, acct, nil
}

// ownedPoll loads a poll and enforces ownership; foreign rows are 404, never 403.
func (s *SchedulingService) ownedPoll(ctx context.Context, userID, pollID string) (domain.MeetingPoll, error) {
	p, err := s.polls.GetByID(ctx, pollID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if p.UserID != userID {
		return domain.MeetingPoll{}, domain.ErrNotFound
	}
	return p, nil
}

// CreatePoll validates the payload (2-10 options, every option end = start +
// duration), assigns server-side option ids and an unguessable 32-hex-char
// public token, and persists the poll in "open" status. A token collision at
// the repo (domain.ErrConflict) is retried with a freshly generated token.
func (s *SchedulingService) CreatePoll(ctx context.Context, userID string, in port.PollInput) (domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.MeetingPoll{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return domain.MeetingPoll{}, fmt.Errorf("%w: title is required", domain.ErrValidation)
	}
	if in.DurationMinutes <= 0 {
		return domain.MeetingPoll{}, fmt.Errorf("%w: durationMinutes must be positive", domain.ErrValidation)
	}
	if len(in.Options) < minPollOptions || len(in.Options) > maxPollOptions {
		return domain.MeetingPoll{}, fmt.Errorf("%w: a poll needs between %d and %d options, got %d", domain.ErrValidation, minPollOptions, maxPollOptions, len(in.Options))
	}
	if in.CalendarID == "" {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendarId is required", domain.ErrValidation)
	}
	c, _, err := ownedCalendarByID(ctx, s.calendars, s.accounts, userID, in.CalendarID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if !c.CanWrite {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	duration := time.Duration(in.DurationMinutes) * time.Minute
	options := make([]domain.PollOption, len(in.Options))
	for i, o := range in.Options {
		if !o.End.Equal(o.Start.Add(duration)) {
			return domain.MeetingPoll{}, fmt.Errorf("%w: option %d end must equal start + duration", domain.ErrValidation, i)
		}
		options[i] = domain.PollOption{ID: newID(), Start: o.Start, End: o.End}
	}

	var desc *string
	if in.Description != "" {
		desc = ptr(in.Description)
	}

	p := domain.MeetingPoll{
		UserID:          userID,
		Title:           in.Title,
		Description:     desc,
		CalendarID:      in.CalendarID,
		DurationMinutes: in.DurationMinutes,
		Options:         options,
		Status:          domain.PollOpen,
		CreatedAt:       s.clock.Now(),
	}

	var lastErr error
	for attempt := 0; attempt < maxTokenAttempts; attempt++ {
		p.Token = randomToken(pollTokenBytes)
		created, err := s.polls.Create(ctx, p)
		if err == nil {
			return created, nil
		}
		if !errors.Is(err, domain.ErrConflict) {
			return domain.MeetingPoll{}, err
		}
		lastErr = err
	}
	return domain.MeetingPoll{}, fmt.Errorf("service: could not generate a unique poll token after %d attempts: %w", maxTokenAttempts, lastErr)
}

// ListPolls returns every poll owned by userID.
func (s *SchedulingService) ListPolls(ctx context.Context, userID string) ([]domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	polls, err := s.polls.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if polls == nil {
		polls = []domain.MeetingPoll{}
	}
	return polls, nil
}

// DeletePoll removes a poll owned by userID (foreign/unknown ids are 404).
func (s *SchedulingService) DeletePoll(ctx context.Context, userID, pollID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	p, err := s.ownedPoll(ctx, userID, pollID)
	if err != nil {
		return err
	}
	return s.polls.Delete(ctx, p.ID)
}

// publicPollView builds the anonymized public document for a poll: per-option
// vote tallies only — voter identities never cross this boundary.
func (s *SchedulingService) publicPollView(ctx context.Context, p domain.MeetingPoll) (port.PublicPoll, error) {
	votes, err := s.polls.ListVotes(ctx, p.ID)
	if err != nil {
		return port.PublicPoll{}, err
	}
	tallies := make(map[string]port.PollTally, len(p.Options))
	for _, o := range p.Options {
		tallies[o.ID] = port.PollTally{}
	}
	for _, v := range votes {
		t := tallies[v.OptionID]
		switch v.Choice {
		case domain.VoteYes:
			t.Yes++
		case domain.VoteNo:
			t.No++
		case domain.VoteIfNeeded:
			t.IfNeeded++
		}
		tallies[v.OptionID] = t
	}

	var organizerName string
	if s.users != nil {
		if u, err := s.users.GetByID(ctx, p.UserID); err == nil {
			if u.Name != nil && *u.Name != "" {
				organizerName = *u.Name
			} else {
				organizerName = u.Email
			}
		}
	}

	return port.PublicPoll{
		Token:           p.Token,
		Title:           p.Title,
		Description:     p.Description,
		OrganizerName:   organizerName,
		DurationMinutes: p.DurationMinutes,
		Status:          p.Status,
		Options:         p.Options,
		Tallies:         tallies,
		WinnerOptionID:  p.WinnerOptionID,
	}, nil
}

// PublicPollByToken serves the public poll page: title, options, and
// anonymized tallies. No authentication required.
func (s *SchedulingService) PublicPollByToken(ctx context.Context, token string) (port.PublicPoll, error) {
	p, err := s.polls.GetByToken(ctx, token)
	if err != nil {
		return port.PublicPoll{}, err
	}
	return s.publicPollView(ctx, p)
}

// VotePoll records (or replaces) one voter's ballot on an open poll and
// returns the refreshed public view. The poll must be open; every option id
// in the ballot must be a known option on the poll; the voter email must be
// syntactically valid. Re-voting (same email, same option) replaces the
// previous choice rather than appending a duplicate.
func (s *SchedulingService) VotePoll(ctx context.Context, token string, ballot port.PollBallot) (port.PublicPoll, error) {
	p, err := s.polls.GetByToken(ctx, token)
	if err != nil {
		return port.PublicPoll{}, err
	}
	if p.Status != domain.PollOpen {
		return port.PublicPoll{}, fmt.Errorf("%w: poll is not open for voting", domain.ErrConflict)
	}

	email := strings.TrimSpace(ballot.VoterEmail)
	if email == "" {
		return port.PublicPoll{}, fmt.Errorf("%w: voterEmail is required", domain.ErrValidation)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return port.PublicPoll{}, fmt.Errorf("%w: invalid voterEmail %q", domain.ErrValidation, email)
	}
	if len(ballot.Choices) == 0 {
		return port.PublicPoll{}, fmt.Errorf("%w: ballot must include at least one choice", domain.ErrValidation)
	}

	known := make(map[string]bool, len(p.Options))
	for _, o := range p.Options {
		known[o.ID] = true
	}

	now := s.clock.Now()
	votes := make([]domain.PollVote, 0, len(ballot.Choices))
	for optionID, choice := range ballot.Choices {
		if !known[optionID] {
			return port.PublicPoll{}, fmt.Errorf("%w: unknown option id %q", domain.ErrValidation, optionID)
		}
		switch choice {
		case domain.VoteYes, domain.VoteNo, domain.VoteIfNeeded:
		default:
			return port.PublicPoll{}, fmt.Errorf("%w: unknown vote choice %q", domain.ErrValidation, choice)
		}
		votes = append(votes, domain.PollVote{
			PollID:     p.ID,
			OptionID:   optionID,
			VoterEmail: email,
			VoterName:  strings.TrimSpace(ballot.VoterName),
			Choice:     choice,
			CreatedAt:  now,
		})
	}

	if err := s.polls.UpsertVotes(ctx, votes); err != nil {
		return port.PublicPoll{}, err
	}
	return s.publicPollView(ctx, p)
}

// ConfirmPoll picks optionID as the winner: it creates the event via
// provider write-through (attendees = every yes/if_needed voter on that
// option), marks the poll confirmed, and emails every one of those voters a
// "time confirmed" notice through the organizer's own account. Confirming an
// already-confirmed poll with the same option is a no-op that returns the
// poll unchanged; a different option is domain.ErrConflict.
func (s *SchedulingService) ConfirmPoll(ctx context.Context, userID, pollID, optionID string) (domain.MeetingPoll, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.MeetingPoll{}, err
	}
	p, err := s.ownedPoll(ctx, userID, pollID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}

	var winner *domain.PollOption
	for i := range p.Options {
		if p.Options[i].ID == optionID {
			winner = &p.Options[i]
			break
		}
	}
	if winner == nil {
		return domain.MeetingPoll{}, fmt.Errorf("%w: unknown option id %q", domain.ErrValidation, optionID)
	}

	if p.Status == domain.PollConfirmed {
		if p.WinnerOptionID != nil && *p.WinnerOptionID == optionID {
			return p, nil // idempotent: already confirmed with this option
		}
		return domain.MeetingPoll{}, fmt.Errorf("%w: poll already confirmed with a different option", domain.ErrConflict)
	}
	if p.Status != domain.PollOpen {
		return domain.MeetingPoll{}, fmt.Errorf("%w: poll is %s, not open", domain.ErrConflict, p.Status)
	}

	c, acct, err := ownedCalendarByID(ctx, s.calendars, s.accounts, userID, p.CalendarID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	if !c.CanWrite {
		return domain.MeetingPoll{}, fmt.Errorf("%w: calendar is read-only", domain.ErrValidation)
	}

	votes, err := s.polls.ListVotes(ctx, p.ID)
	if err != nil {
		return domain.MeetingPoll{}, err
	}
	seen := map[string]bool{}
	var attendeeEmails []string
	for _, v := range votes {
		if v.OptionID != optionID {
			continue
		}
		if v.Choice != domain.VoteYes && v.Choice != domain.VoteIfNeeded {
			continue
		}
		key := strings.ToLower(v.VoterEmail)
		if seen[key] {
			continue
		}
		seen[key] = true
		attendeeEmails = append(attendeeEmails, v.VoterEmail)
	}
	sort.Strings(attendeeEmails)

	in := domain.EventInput{
		CalendarID:     p.CalendarID,
		Title:          p.Title,
		Start:          winner.Start,
		End:            winner.End,
		AttendeeEmails: attendeeEmails,
	}
	if p.Description != nil {
		in.Description = *p.Description
	}

	ev := eventFromInput(in, c.ID)
	if provider, ok := s.cal[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return domain.MeetingPoll{}, err
		}
		created, err := provider.CreateEvent(ctx, token, c.ProviderCalendarID, in)
		if err != nil {
			return domain.MeetingPoll{}, fmt.Errorf("provider write-through failed: %w", err)
		}
		created.ID = ev.ID
		created.CalendarID = c.ID
		ev = created
	}
	if _, err := s.events.Upsert(ctx, ev); err != nil {
		return domain.MeetingPoll{}, err
	}

	// Flip the poll to confirmed (winner + event ID persisted) before sending
	// any confirmation email. This must happen first: the provider event and
	// the local mirror already committed above, so at this point the poll
	// MUST end up confirmed no matter what happens next. Confirming first
	// also makes the confirmation email genuinely best-effort (see below) and
	// puts a subsequent retry on the idempotent short-circuit at the top of
	// this method (Status == PollConfirmed) instead of re-running provider
	// event creation and producing a duplicate calendar event.
	p.Status = domain.PollConfirmed
	winnerID := optionID
	p.WinnerOptionID = &winnerID
	eventID := ev.ID
	p.EventID = &eventID
	if err := s.polls.Update(ctx, p); err != nil {
		return domain.MeetingPoll{}, err
	}

	// Email every yes/if_needed voter; best-effort like notifyThread in
	// sync.go — a send failure never rolls back the now-confirmed poll, it is
	// simply discarded.
	if provider, ok := s.mail[acct.Provider]; ok && len(attendeeEmails) > 0 {
		if token, err := s.tokens.accessToken(ctx, acct); err == nil {
			for _, email := range attendeeEmails {
				subject, body := buildPollConfirmationEmail(p, *winner, email)
				_, _ = provider.Send(ctx, token, port.OutgoingMessage{
					From:     domain.EmailAddress{Email: acct.Email},
					To:       []domain.EmailAddress{{Email: email}},
					Subject:  subject,
					BodyText: body,
				})
			}
		}
	}

	return p, nil
}

// buildPollConfirmationEmail renders the "time confirmed" notice sent to
// each yes/if_needed voter once the organizer picks a winning option. Pure:
// no I/O, so it is trivially unit-testable independent of the mail provider.
func buildPollConfirmationEmail(p domain.MeetingPoll, winner domain.PollOption, voterEmail string) (subject, body string) {
	subject = fmt.Sprintf("Time confirmed: %s", p.Title)

	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\n%q has been confirmed for %s - %s.\n\n",
		p.Title, winner.Start.Format(time.RFC1123), winner.End.Format(time.RFC1123))
	if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
		fmt.Fprintf(&b, "%s\n\n", *p.Description)
	}
	fmt.Fprintf(&b, "You're receiving this at %s because you voted on this meeting poll.\n", voterEmail)
	return subject, b.String()
}
