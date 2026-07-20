import type { CalendarSubscription, Event } from '@calendium/shared';

/**
 * Helpers for interesting-calendar ICS feed subscriptions (M2.8 Task 15).
 * Subscription events are read-only mirrors: they carry `subscriptionId`,
 * no calendarId, and are rendered with the feed's color and a dashed border.
 */

/** True when the event is a read-only mirror from an ICS feed subscription. */
export function isSubscriptionEvent(event: Pick<Event, 'subscriptionId'>): boolean {
  return !!event.subscriptionId;
}

/** subscriptionId → feed color lookup for grid rendering. */
export function subscriptionColorMap(
  subs: CalendarSubscription[] | undefined
): Map<string, string> {
  return new Map((subs ?? []).map((s) => [s.id, s.color]));
}

/**
 * Short status caption for the settings manager: a failed refresh names the
 * problem (honesty — stale data must look stale), otherwise the last
 * successful sync time or a "never" placeholder.
 */
export function subscriptionStatus(
  sub: Pick<CalendarSubscription, 'lastFetchedAt' | 'lastError'>
): { kind: 'error' | 'ok' | 'pending'; label: string } {
  if (sub.lastError) return { kind: 'error', label: sub.lastError };
  if (!sub.lastFetchedAt) return { kind: 'pending', label: 'Not fetched yet' };
  return { kind: 'ok', label: `Updated ${new Date(sub.lastFetchedAt).toLocaleString()}` };
}
