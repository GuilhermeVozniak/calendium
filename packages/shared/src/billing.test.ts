import { describe, expect, it } from 'vitest';
import {
  ACTIVE_GRACE_MS,
  hasBillingSubscription,
  PAST_DUE_GRACE_MS,
  subscriptionDenialReason,
  subscriptionHasAccess,
  TRIAL_BANNER_DAYS,
  trialDaysLeft,
} from './billing';
import type { Subscription, SubscriptionStatus } from './types';

const NOW = Date.parse('2026-10-04T12:00:00Z');
const HOUR = 60 * 60 * 1000;

function sub(status: SubscriptionStatus, extra: Partial<Subscription> = {}): Subscription {
  return {
    status,
    plan: 'annual',
    priceUsd: 50,
    currentPeriodEnd: null,
    cancelAtPeriodEnd: false,
    trialEndsAt: null,
    ...extra,
  };
}
const iso = (ms: number) => new Date(ms).toISOString();

describe('subscriptionDenialReason (mirror of domain.Subscription.DenialReason)', () => {
  it.each<[string, Subscription, ReturnType<typeof subscriptionDenialReason>]>([
    ['trialing before end', sub('trialing', { trialEndsAt: iso(NOW + HOUR) }), null],
    ['trialing at end', sub('trialing', { trialEndsAt: iso(NOW) }), 'trial_ended'],
    ['trialing without end', sub('trialing'), 'trial_ended'],
    ['active no period', sub('active'), null],
    ['active inside grace', sub('active', { currentPeriodEnd: iso(NOW - ACTIVE_GRACE_MS + HOUR) }), null],
    ['active past grace', sub('active', { currentPeriodEnd: iso(NOW - ACTIVE_GRACE_MS) }), 'past_due'],
    ['past_due inside grace', sub('past_due', { currentPeriodEnd: iso(NOW - PAST_DUE_GRACE_MS + HOUR) }), null],
    ['past_due past grace', sub('past_due', { currentPeriodEnd: iso(NOW - PAST_DUE_GRACE_MS) }), 'past_due'],
    ['past_due without period', sub('past_due'), 'past_due'],
    ['paused', sub('paused', { currentPeriodEnd: iso(NOW + HOUR) }), 'paused'],
    ['canceled', sub('canceled', { currentPeriodEnd: iso(NOW + HOUR) }), 'canceled'],
    ['none', sub('none'), 'none'],
  ])('%s', (_name, s, want) => {
    expect(subscriptionDenialReason(s, NOW)).toBe(want);
    expect(subscriptionHasAccess(s, NOW)).toBe(want === null);
  });
});

describe('trialDaysLeft', () => {
  it('counts whole days up, never below zero, only while trialing', () => {
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW + 2 * 24 * HOUR + HOUR) }), NOW)).toBe(3);
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW + 24 * HOUR) }), NOW)).toBe(1);
    expect(trialDaysLeft(sub('trialing', { trialEndsAt: iso(NOW - HOUR) }), NOW)).toBe(0);
    expect(trialDaysLeft(sub('trialing'), NOW)).toBeNull();
    expect(trialDaysLeft(sub('active', { trialEndsAt: iso(NOW + HOUR) }), NOW)).toBeNull();
  });
  it('exposes the 3-day banner window', () => {
    expect(TRIAL_BANNER_DAYS).toBe(3);
  });
});

describe('hasBillingSubscription', () => {
  it('is true only for live provider statuses', () => {
    expect(hasBillingSubscription(sub('active'))).toBe(true);
    expect(hasBillingSubscription(sub('past_due'))).toBe(true);
    expect(hasBillingSubscription(sub('paused'))).toBe(true);
    expect(hasBillingSubscription(sub('trialing'))).toBe(false);
    expect(hasBillingSubscription(sub('canceled'))).toBe(false);
    expect(hasBillingSubscription(sub('none'))).toBe(false);
  });
});
