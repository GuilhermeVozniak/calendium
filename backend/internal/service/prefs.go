package service

import (
	"context"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// PrefsService implements port.PrefsService: cheap per-user client
// preferences (no paywall — layout, not product value) plus the calendar
// automation preference document (M2.8 Task 5, entitlement-gated — the
// prefs unlock the automation engine).
type PrefsService struct {
	prefs    port.PrefsRepo
	calendar port.CalendarPrefsRepo
	ent      entitlement
}

var _ port.PrefsService = (*PrefsService)(nil)

func NewPrefsService(prefs port.PrefsRepo, calendar port.CalendarPrefsRepo, subs port.SubscriptionRepo, users port.UserRepo, clock port.Clock, selfHosted bool) *PrefsService {
	return &PrefsService{
		prefs:    prefs,
		calendar: calendar,
		ent:      entitlement{subs: subs, users: users, clock: clock, selfHost: selfHosted},
	}
}

func (s *PrefsService) GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error) {
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	return p, nil
}

func (s *PrefsService) UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error) {
	if err := p.Validate(); err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	if err := s.prefs.Save(ctx, userID, p); err != nil {
		return domain.UserPrefs{}, err
	}
	return p, nil
}

// --- Calendar automation preferences (M2.8 Task 5) ---------------------------

// GetCalendarPrefs returns the user's calendar automation preferences; the
// repo synthesizes DefaultCalendarPrefs when no row was ever saved.
func (s *PrefsService) GetCalendarPrefs(ctx context.Context, userID string) (domain.CalendarPrefs, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarPrefs{}, err
	}
	return s.calendar.Get(ctx, userID)
}

// UpdateCalendarPrefs merges the nil-means-unchanged patch into the current
// document (defaults when absent), validates the merged result, and persists
// it as one full-row upsert — the user_settings precedent: last write wins on
// the whole document, never a partial column update.
func (s *PrefsService) UpdateCalendarPrefs(ctx context.Context, userID string, patch domain.CalendarPrefsPatch) (domain.CalendarPrefs, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarPrefs{}, err
	}
	cur, err := s.calendar.Get(ctx, userID)
	if err != nil {
		return domain.CalendarPrefs{}, err
	}
	next := patch.Apply(cur)
	next.UserID = userID
	if next.WorkDays == nil {
		next.WorkDays = []time.Weekday{}
	}
	if err := next.Validate(); err != nil {
		return domain.CalendarPrefs{}, err
	}
	if err := s.calendar.Upsert(ctx, next); err != nil {
		return domain.CalendarPrefs{}, err
	}
	return next, nil
}
