import type { Calendar, ConnectedAccount } from '@calendium/shared';
import { describe, expect, it } from 'vitest';

import { groupCalendarsByAccount } from '@/lib/calendar-accounts';

function calendar(overrides: Partial<Calendar> = {}): Calendar {
  return {
    id: 'cal-1',
    accountId: 'acc-1',
    name: 'Personal',
    color: '#6366f1',
    timeZone: 'UTC',
    isPrimary: true,
    isVisible: true,
    canWrite: true,
    ...overrides,
  };
}

function account(overrides: Partial<ConnectedAccount> = {}): ConnectedAccount {
  return {
    id: 'acc-1',
    provider: 'google',
    email: 'me@acme.com',
    status: 'active',
    scopes: [],
    vipSenders: [],
    lastSyncedAt: null,
    createdAt: '2026-01-01T00:00:00.000Z',
    ...overrides,
  };
}

describe('groupCalendarsByAccount', () => {
  it('groups calendars under their account, keyed by the account email', () => {
    const acc1 = account({ id: 'acc-1', email: 'work@acme.com' });
    const acc2 = account({ id: 'acc-2', email: 'me@gmail.com' });
    const calWork = calendar({ id: 'cal-work', accountId: 'acc-1', name: 'Work' });
    const calPersonal = calendar({ id: 'cal-personal', accountId: 'acc-2', name: 'Personal' });

    const groups = groupCalendarsByAccount([calWork, calPersonal], [acc1, acc2]);

    expect(groups).toEqual([
      { accountId: 'acc-1', email: 'work@acme.com', calendars: [calWork] },
      { accountId: 'acc-2', email: 'me@gmail.com', calendars: [calPersonal] },
    ]);
  });

  it('preserves the accounts array order for group order, and calendar order within a group', () => {
    const acc1 = account({ id: 'acc-1', email: 'a@acme.com' });
    const acc2 = account({ id: 'acc-2', email: 'b@acme.com' });
    const calB1 = calendar({ id: 'cal-b1', accountId: 'acc-2', name: 'B1' });
    const calA1 = calendar({ id: 'cal-a1', accountId: 'acc-1', name: 'A1' });
    const calA2 = calendar({ id: 'cal-a2', accountId: 'acc-1', name: 'A2' });

    // Accounts passed acc2-before-acc1, but the accounts *array* order still
    // governs group order (acc1 group, then acc2 group) — the group order
    // comes from the accounts argument's own order.
    const groups = groupCalendarsByAccount([calB1, calA1, calA2], [acc1, acc2]);

    expect(groups.map((g) => g.accountId)).toEqual(['acc-1', 'acc-2']);
    expect(groups[0]!.calendars.map((c) => c.id)).toEqual(['cal-a1', 'cal-a2']);
    expect(groups[1]!.calendars.map((c) => c.id)).toEqual(['cal-b1']);
  });

  it('falls back to the raw accountId as the heading when no matching account is found, instead of dropping the calendars', () => {
    const orphanCalendar = calendar({ id: 'cal-orphan', accountId: 'acc-stale' });

    const groups = groupCalendarsByAccount([orphanCalendar], []);

    expect(groups).toEqual([
      { accountId: 'acc-stale', email: 'acc-stale', calendars: [orphanCalendar] },
    ]);
  });

  it('omits accounts that have no calendars at all', () => {
    const acc1 = account({ id: 'acc-1', email: 'a@acme.com' });
    const acc2 = account({ id: 'acc-2', email: 'b@acme.com' });
    const cal = calendar({ id: 'cal-1', accountId: 'acc-1' });

    const groups = groupCalendarsByAccount([cal], [acc1, acc2]);

    expect(groups).toEqual([{ accountId: 'acc-1', email: 'a@acme.com', calendars: [cal] }]);
  });

  it('returns an empty array for no calendars', () => {
    expect(groupCalendarsByAccount([], [account()])).toEqual([]);
  });
});
