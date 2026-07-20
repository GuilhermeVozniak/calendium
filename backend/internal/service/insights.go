package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// insightsMaxRangeDays caps a TimeInsights request (one quarter).
const insightsMaxRangeDays = 92

// insightsTopPeople is how many PersonStat rows the panel shows.
const insightsTopPeople = 5

// InsightsServiceDeps wires the time-insights aggregation (M2.8 Task 17).
// Everything is read from the LOCAL mirror — this service never talks to a
// provider.
type InsightsServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Users         port.UserRepo
	Accounts      port.AccountRepo
	Events        port.EventRepo
	Managed       port.InsightsManagedEventRepo
	Tasks         port.TaskRepo
	Prefs         port.CalendarPrefsRepo
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// InsightsSvc implements port.InsightsService: pure aggregation over the
// mirrored events, the managed-events ledger, and scheduled task blocks.
type InsightsSvc struct {
	users    port.UserRepo
	accounts port.AccountRepo
	events   port.EventRepo
	managed  port.InsightsManagedEventRepo
	tasks    port.TaskRepo
	prefs    port.CalendarPrefsRepo
	ent      entitlement
}

// NewInsightsService wires an InsightsSvc.
func NewInsightsService(d InsightsServiceDeps) *InsightsSvc {
	return &InsightsSvc{
		users:    d.Users,
		accounts: d.Accounts,
		events:   d.Events,
		managed:  d.Managed,
		tasks:    d.Tasks,
		prefs:    d.Prefs,
		ent:      entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
	}
}

var _ port.InsightsService = (*InsightsSvc)(nil)

// TimeInsights aggregates [from, to) into the analytics document.
//
// Classification rules (each table-tested):
//   - cancelled events, all-day events, and ICS subscription mirrors never
//     count toward anything;
//   - managed events are categorized generically by their managed_events
//     kind: "focus" counts as focus time, EVERY other kind (buffer, travel,
//     and kinds that don't exist yet) is excluded from meeting time — new
//     automation kinds Just Work;
//   - events the user declined are excluded;
//   - user events titled like focus ("focus" substring, case-insensitive)
//     count as focus;
//   - events with >= 2 attendees count as meetings;
//   - overlap is clipped to [from, to).
func (s *InsightsSvc) TimeInsights(ctx context.Context, userID string, from, to time.Time) (domain.TimeInsights, error) {
	zero := domain.TimeInsights{}
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return zero, fmt.Errorf("%w: from must be before to", domain.ErrValidation)
	}
	if to.Sub(from) > insightsMaxRangeDays*24*time.Hour {
		return zero, fmt.Errorf("%w: range must be %d days or less", domain.ErrValidation, insightsMaxRangeDays)
	}

	selfEmails, err := s.selfEmails(ctx, userID)
	if err != nil {
		return zero, err
	}
	events, err := s.events.ListInRange(ctx, userID, from, to, nil)
	if err != nil {
		return zero, err
	}
	managed, err := s.managed.ListAllByUser(ctx, userID)
	if err != nil {
		return zero, err
	}
	kindByEvent := make(map[string]domain.ManagedKind, len(managed))
	for _, m := range managed {
		kindByEvent[m.EventID] = m.Kind
	}

	loc := from.Location()
	out := domain.TimeInsights{
		From:      from,
		To:        to,
		TopPeople: []domain.PersonStat{},
		ByDay:     dayScaffold(from, to, loc),
	}
	dayIndex := make(map[string]int, len(out.ByDay))
	for i, d := range out.ByDay {
		dayIndex[d.Date] = i
	}
	// addSpan spreads a clipped interval across the per-day rows, splitting
	// at midnight in from's location.
	addSpan := func(start, end time.Time, meeting bool) {
		for t := start; t.Before(end); {
			lt := t.In(loc)
			dayStart := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
			next := dayStart.AddDate(0, 0, 1)
			segEnd := end
			if next.Before(segEnd) {
				segEnd = next
			}
			if i, ok := dayIndex[dayStart.Format("2006-01-02")]; ok {
				mins := int(segEnd.Sub(t) / time.Minute)
				if meeting {
					out.ByDay[i].MeetingMinutes += mins
				} else {
					out.ByDay[i].FocusMinutes += mins
				}
			}
			t = segEnd
		}
	}

	people := map[string]*domain.PersonStat{}
	for _, ev := range events {
		if ev.Status == domain.EventCancelled || ev.AllDay || ev.SubscriptionID != nil {
			continue
		}
		start, end := clipInterval(ev.Start, ev.End, from, to)
		mins := int(end.Sub(start) / time.Minute)
		if mins <= 0 {
			continue
		}
		if kind, isManaged := kindByEvent[ev.ID]; isManaged {
			if kind == domain.ManagedFocus {
				out.FocusMinutes += mins
				addSpan(start, end, false)
			}
			// Buffers, travel blocks, and any future managed kind: never
			// meeting time.
			continue
		}
		if declinedBy(ev, selfEmails) {
			continue
		}
		if domain.IsFocusTitle(ev.Title) {
			out.FocusMinutes += mins
			addSpan(start, end, false)
			continue
		}
		if len(ev.Attendees) < 2 {
			continue // solo block: neither meeting nor focus
		}
		out.MeetingMinutes += mins
		out.MeetingCount++
		addSpan(start, end, true)
		for _, a := range ev.Attendees {
			email := strings.ToLower(strings.TrimSpace(a.Email))
			if email == "" || selfEmails[email] || a.Response == domain.RsvpDeclined {
				continue
			}
			p, ok := people[email]
			if !ok {
				p = &domain.PersonStat{Email: email}
				people[email] = p
			}
			if p.Name == "" && a.Name != nil {
				p.Name = *a.Name
			}
			p.Meetings++
			p.Minutes += mins
		}
	}

	// Scheduled task blocks overlapping the range, completed ones included
	// (time spent is time spent).
	tasks, err := s.tasks.List(ctx, port.TaskQuery{
		UserID:           userID,
		IncludeCompleted: true,
		ScheduledFrom:    from,
		ScheduledTo:      to,
	})
	if err != nil {
		return zero, err
	}
	for _, t := range tasks {
		if !t.Scheduled() {
			continue
		}
		start, end := clipInterval(*t.ScheduledStart, *t.ScheduledEnd, from, to)
		if mins := int(end.Sub(start) / time.Minute); mins > 0 {
			out.TaskMinutes += mins
		}
	}

	// Weekly focus goal scaled to the requested range.
	prefs, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return zero, err
	}
	if prefs.FocusGoalMinutesPerWeek > 0 {
		rangeMinutes := int(to.Sub(from) / time.Minute)
		out.FocusGoalMinutes = prefs.FocusGoalMinutesPerWeek * rangeMinutes / (7 * 24 * 60)
	}

	for _, p := range people {
		out.TopPeople = append(out.TopPeople, *p)
	}
	sort.Slice(out.TopPeople, func(i, j int) bool {
		a, b := out.TopPeople[i], out.TopPeople[j]
		if a.Minutes != b.Minutes {
			return a.Minutes > b.Minutes
		}
		if a.Meetings != b.Meetings {
			return a.Meetings > b.Meetings
		}
		return a.Email < b.Email
	})
	if len(out.TopPeople) > insightsTopPeople {
		out.TopPeople = out.TopPeople[:insightsTopPeople]
	}
	return out, nil
}

// selfEmails collects every address that identifies the user (profile email
// plus each connected account), lowercased, for self-exclusion and the
// declined-by-user check.
func (s *InsightsSvc) selfEmails(ctx context.Context, userID string) (map[string]bool, error) {
	out := map[string]bool{}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if err == nil && u.Email != "" {
		out[strings.ToLower(u.Email)] = true
	}
	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		if a.Email != "" {
			out[strings.ToLower(a.Email)] = true
		}
	}
	return out, nil
}

// declinedBy reports whether any attendee identified by emails declined.
func declinedBy(ev domain.Event, emails map[string]bool) bool {
	for _, a := range ev.Attendees {
		if emails[strings.ToLower(strings.TrimSpace(a.Email))] && a.Response == domain.RsvpDeclined {
			return true
		}
	}
	return false
}

// clipInterval clamps [start, end) to [from, to).
func clipInterval(start, end, from, to time.Time) (time.Time, time.Time) {
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	return start, end
}

// dayScaffold returns one zeroed DayStat per calendar day covering [from,
// to) in loc, ascending.
func dayScaffold(from, to time.Time, loc *time.Location) []domain.DayStat {
	lf := from.In(loc)
	cur := time.Date(lf.Year(), lf.Month(), lf.Day(), 0, 0, 0, 0, loc)
	out := []domain.DayStat{}
	for cur.Before(to) {
		out = append(out, domain.DayStat{Date: cur.Format("2006-01-02")})
		cur = cur.AddDate(0, 0, 1)
	}
	return out
}
