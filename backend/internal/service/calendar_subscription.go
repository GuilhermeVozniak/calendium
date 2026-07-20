package service

import (
	"context"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/ics"
	"calendium/backend/internal/port"
)

// Subscription expansion horizon: occurrences are materialized over
// [now-1mo, now+12mo) on every (re)fetch.
func subscriptionHorizon(now time.Time) (from, to time.Time) {
	return now.AddDate(0, -1, 0), now.AddDate(1, 0, 0)
}

// ListCalendarSubscriptions returns the caller's ICS feed subscriptions.
func (s *CalendarService) ListCalendarSubscriptions(ctx context.Context, userID string) ([]domain.CalendarSubscription, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if s.calendarSubs == nil {
		return nil, domain.ErrNotImplemented
	}
	subs, err := s.calendarSubs.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if subs == nil {
		subs = []domain.CalendarSubscription{}
	}
	return subs, nil
}

// CreateCalendarSubscription validates the URL (https only), fetches the
// feed once synchronously — so the caller sees immediate events or a clear
// error — and persists the subscription plus its expanded occurrence set.
// An unreachable/unparseable feed is domain.ErrUnprocessable (422) and
// nothing is persisted; a duplicate (user, url) is the repo's ErrConflict.
func (s *CalendarService) CreateCalendarSubscription(ctx context.Context, userID string, in port.CalendarSubscriptionInput) (domain.CalendarSubscription, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarSubscription{}, err
	}
	if s.calendarSubs == nil || s.icsFetcher == nil {
		return domain.CalendarSubscription{}, domain.ErrNotImplemented
	}
	sub, err := domain.NewCalendarSubscription(userID, in.URL, in.Name, in.Color)
	if err != nil {
		return domain.CalendarSubscription{}, err
	}

	cal, etag, _, err := s.icsFetcher.Fetch(ctx, sub.URL, "")
	if err != nil {
		return domain.CalendarSubscription{}, fmt.Errorf("%w: %v", domain.ErrUnprocessable, err)
	}
	sub.ResolveName(cal.Name)
	now := s.clock.Now()
	sub.LastFetchedAt = &now

	created, err := s.calendarSubs.Create(ctx, sub)
	if err != nil {
		return domain.CalendarSubscription{}, err
	}
	if err := s.calendarSubs.ReplaceEvents(ctx, created.ID, expandFeedEvents(cal, now)); err != nil {
		// The subscription row exists but etag-less: the next refresh pass
		// refetches in full instead of 304ing against an empty event set.
		return domain.CalendarSubscription{}, err
	}
	// Persist the validator only after the event swap landed (etag-after-swap
	// — same rule as SubscriptionRefresher).
	created.Etag = etag
	if err := s.calendarSubs.Update(ctx, created); err != nil {
		return domain.CalendarSubscription{}, err
	}
	return created, nil
}

// UpdateCalendarSubscription patches name/color/visibility. Another user's
// subscription is ErrNotFound — existence is never leaked cross-tenant.
func (s *CalendarService) UpdateCalendarSubscription(ctx context.Context, userID, subscriptionID string, patch port.CalendarSubscriptionPatch) (domain.CalendarSubscription, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.CalendarSubscription{}, err
	}
	sub, err := s.ownedSubscription(ctx, userID, subscriptionID)
	if err != nil {
		return domain.CalendarSubscription{}, err
	}
	if patch.Name != nil {
		sub.Name = *patch.Name
	}
	if patch.Color != nil {
		sub.Color = *patch.Color
	}
	if patch.IsVisible != nil {
		sub.IsVisible = *patch.IsVisible
	}
	if err := s.calendarSubs.Update(ctx, sub); err != nil {
		return domain.CalendarSubscription{}, err
	}
	return sub, nil
}

// DeleteCalendarSubscription removes the subscription; its mirrored events
// go with it (ON DELETE CASCADE).
func (s *CalendarService) DeleteCalendarSubscription(ctx context.Context, userID, subscriptionID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	sub, err := s.ownedSubscription(ctx, userID, subscriptionID)
	if err != nil {
		return err
	}
	return s.calendarSubs.Delete(ctx, sub.ID)
}

// ownedSubscription loads a subscription and enforces ownership: another
// user's subscription is indistinguishable from a missing one.
func (s *CalendarService) ownedSubscription(ctx context.Context, userID, subscriptionID string) (domain.CalendarSubscription, error) {
	if s.calendarSubs == nil {
		return domain.CalendarSubscription{}, domain.ErrNotImplemented
	}
	sub, err := s.calendarSubs.GetByID(ctx, subscriptionID)
	if err != nil {
		return domain.CalendarSubscription{}, err
	}
	if sub.UserID != userID {
		return domain.CalendarSubscription{}, fmt.Errorf("%w: subscription %s", domain.ErrNotFound, subscriptionID)
	}
	return sub, nil
}

// expandFeedEvents materializes a parsed feed into concrete, read-only
// domain.Event occurrences over the subscription horizon. CANCELLED events
// are dropped; recurring events expand via ics.Expand (bounded). The ICS
// UID travels in ProviderEventID (never serialized to clients).
func expandFeedEvents(cal ics.Calendar, now time.Time) []domain.Event {
	from, to := subscriptionHorizon(now)
	var out []domain.Event
	for _, ev := range cal.Events {
		if ev.Status == "CANCELLED" {
			continue
		}
		for _, oc := range ics.Expand(ev, from, to) {
			de := domain.Event{
				ProviderEventID: oc.UID,
				Title:           oc.Summary,
				Start:           oc.Start,
				End:             oc.End,
				AllDay:          oc.AllDay,
				Status:          domain.EventConfirmed,
			}
			if oc.Description != "" {
				d := oc.Description
				de.Description = &d
			}
			if oc.Location != "" {
				l := oc.Location
				de.Location = &l
			}
			out = append(out, de)
		}
	}
	return out
}
