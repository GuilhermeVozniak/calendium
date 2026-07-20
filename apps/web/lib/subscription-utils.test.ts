import { describe, expect, it } from 'vitest';

import type { CalendarSubscription } from '@calendium/shared';

import {
  isSubscriptionEvent,
  subscriptionColorMap,
  subscriptionStatus,
} from './subscription-utils';

const SUB: CalendarSubscription = {
  id: 'sub1',
  url: 'https://example.com/holidays.ics',
  name: 'US Holidays',
  color: '#8b5cf6',
  isVisible: true,
  lastFetchedAt: '2026-07-19T11:00:00Z',
  lastError: null,
  createdAt: '2026-07-01T00:00:00Z',
};

describe('isSubscriptionEvent', () => {
  it('is true only when subscriptionId is set', () => {
    expect(isSubscriptionEvent({ subscriptionId: 'sub1' })).toBe(true);
    expect(isSubscriptionEvent({ subscriptionId: undefined })).toBe(false);
    expect(isSubscriptionEvent({})).toBe(false);
  });
});

describe('subscriptionColorMap', () => {
  it('maps subscription ids to their colors', () => {
    const map = subscriptionColorMap([SUB, { ...SUB, id: 'sub2', color: '#ef4444' }]);
    expect(map.get('sub1')).toBe('#8b5cf6');
    expect(map.get('sub2')).toBe('#ef4444');
  });

  it('tolerates undefined input', () => {
    expect(subscriptionColorMap(undefined).size).toBe(0);
  });
});

describe('subscriptionStatus', () => {
  it('surfaces the fetch error verbatim (stale must look stale)', () => {
    const got = subscriptionStatus({ ...SUB, lastError: 'feed answered status 500' });
    expect(got.kind).toBe('error');
    expect(got.label).toBe('feed answered status 500');
  });

  it('reports never-fetched feeds as pending', () => {
    expect(subscriptionStatus({ ...SUB, lastFetchedAt: null }).kind).toBe('pending');
  });

  it('reports a healthy feed with its sync time', () => {
    const got = subscriptionStatus(SUB);
    expect(got.kind).toBe('ok');
    expect(got.label).toMatch(/^Updated /);
  });
});
