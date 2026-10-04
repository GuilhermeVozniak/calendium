import type { PaymentRequiredReason, Subscription } from './types';

/**
 * Client-side mirror of backend/internal/domain/subscription.go. The server
 * is authoritative (every gated call answers 402); these helpers let the
 * shells decide what to render from the subscription document they already
 * fetched, without a round-trip per screen.
 */

export const DAY_MS = 24 * 60 * 60 * 1000;
/** Card-free trial granted server-side at signup. */
export const TRIAL_LENGTH_DAYS = 14;
/** An active row stays entitled this long past currentPeriodEnd (late renewal webhook). */
export const ACTIVE_GRACE_MS = 3 * DAY_MS;
/** A past_due row stays entitled this long past currentPeriodEnd (dunning window). */
export const PAST_DUE_GRACE_MS = 7 * DAY_MS;
/** Show the trial banner during the last N days of the trial. */
export const TRIAL_BANNER_DAYS = 3;

/** Why the subscription denies access at `nowMs`, or null when it grants it. */
export function subscriptionDenialReason(
  sub: Subscription,
  nowMs: number = Date.now()
): PaymentRequiredReason | null {
  switch (sub.status) {
    case 'trialing':
      return sub.trialEndsAt && nowMs < Date.parse(sub.trialEndsAt) ? null : 'trial_ended';
    case 'active':
      return !sub.currentPeriodEnd || nowMs < Date.parse(sub.currentPeriodEnd) + ACTIVE_GRACE_MS
        ? null
        : 'past_due';
    case 'past_due':
      return sub.currentPeriodEnd && nowMs < Date.parse(sub.currentPeriodEnd) + PAST_DUE_GRACE_MS
        ? null
        : 'past_due';
    case 'paused':
      return 'paused';
    case 'canceled':
      return 'canceled';
    default:
      return 'none';
  }
}

export function subscriptionHasAccess(sub: Subscription, nowMs: number = Date.now()): boolean {
  return subscriptionDenialReason(sub, nowMs) === null;
}

/** Whole days left in the trial (ceil, floored at 0); null when not trialing. */
export function trialDaysLeft(sub: Subscription, nowMs: number = Date.now()): number | null {
  if (sub.status !== 'trialing' || !sub.trialEndsAt) return null;
  return Math.max(0, Math.ceil((Date.parse(sub.trialEndsAt) - nowMs) / DAY_MS));
}

/** True when a live provider subscription exists (manage it in the portal rather than starting a checkout). */
export function hasBillingSubscription(sub: Subscription): boolean {
  return sub.status === 'active' || sub.status === 'past_due' || sub.status === 'paused';
}
