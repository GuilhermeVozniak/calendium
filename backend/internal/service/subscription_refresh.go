package service

import (
	"context"
	"io"
	"log/slog"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// subscriptionRefreshInterval is the per-feed refresh cadence: a
// subscription becomes due one hour after its last fetch ATTEMPT.
const subscriptionRefreshInterval = time.Hour

// SubscriptionRefresherDeps wires a SubscriptionRefresher.
type SubscriptionRefresherDeps struct {
	Subs    port.CalendarSubscriptionRepo
	Fetcher port.IcsFetcher
	Clock   port.Clock
	Logger  *slog.Logger // optional
}

// SubscriptionRefresher is the hourly ICS feed refresh pass (M2.8 Task 15).
// It stays a standalone service on its own worker loop by design: refresh
// is feed-cadenced (per-feed hourly), not per-user, and its only write
// surface is subscription_events — disjoint from RunAutomation's
// managed-events work.
type SubscriptionRefresher struct {
	subs    port.CalendarSubscriptionRepo
	fetcher port.IcsFetcher
	clock   port.Clock
	logger  *slog.Logger
}

func NewSubscriptionRefresher(d SubscriptionRefresherDeps) *SubscriptionRefresher {
	logger := d.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &SubscriptionRefresher{subs: d.Subs, fetcher: d.Fetcher, clock: d.Clock, logger: logger}
}

// RefreshDue refreshes every subscription not fetched within the last hour.
// Feeds fail independently: a failure records LastError on its subscription
// (honesty — the UI can show staleness) while the previous good event set
// is kept; it never stalls the rest of the pass.
func (r *SubscriptionRefresher) RefreshDue(ctx context.Context) error {
	now := r.clock.Now()
	due, err := r.subs.ListDue(ctx, now.Add(-subscriptionRefreshInterval))
	if err != nil {
		return err
	}
	for _, sub := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.refreshOne(ctx, sub)
	}
	return nil
}

func (r *SubscriptionRefresher) refreshOne(ctx context.Context, sub domain.CalendarSubscription) {
	now := r.clock.Now()
	// LastFetchedAt records the last ATTEMPT (success or failure) so a dead
	// feed is retried hourly, not hammered every pass; LastError — not a
	// fresh timestamp — is what signals staleness to the user.
	sub.LastFetchedAt = &now

	cal, etag, notModified, err := r.fetcher.Fetch(ctx, sub.URL, sub.Etag)
	if err != nil {
		msg := err.Error()
		sub.LastError = &msg
		r.logger.Warn("subscription refresh failed; keeping previous events",
			"subscription_id", sub.ID, "error", err)
		r.update(ctx, sub)
		return
	}
	sub.LastError = nil
	if notModified {
		r.update(ctx, sub) // validator honored: events already current
		return
	}
	if err := r.subs.ReplaceEvents(ctx, sub.ID, expandFeedEvents(cal, now)); err != nil {
		// The etag is NOT advanced on a failed swap: advancing it would make
		// every later fetch 304 against a stale event set forever. Keeping
		// the old validator means the next due pass refetches in full.
		msg := err.Error()
		sub.LastError = &msg
		r.logger.Error("subscription event swap failed", "subscription_id", sub.ID, "error", err)
		r.update(ctx, sub)
		return
	}
	sub.Etag = etag // validator advances only once the swap landed
	r.update(ctx, sub)
}

func (r *SubscriptionRefresher) update(ctx context.Context, sub domain.CalendarSubscription) {
	if err := r.subs.Update(ctx, sub); err != nil {
		r.logger.Error("subscription bookkeeping update failed", "subscription_id", sub.ID, "error", err)
	}
}
