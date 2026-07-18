import type { Calendar, CalendarSet } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const listCalendarSetsMock = vi.fn();
const createCalendarSetMock = vi.fn();
const updateCalendarSetMock = vi.fn();
const deleteCalendarSetMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listCalendarSets: (...args: unknown[]) => listCalendarSetsMock(...args),
    createCalendarSet: (...args: unknown[]) => createCalendarSetMock(...args),
    updateCalendarSet: (...args: unknown[]) => updateCalendarSetMock(...args),
    deleteCalendarSet: (...args: unknown[]) => deleteCalendarSetMock(...args),
  }),
}));

const mockListCalendarSetsMock = vi.fn();
vi.mock('@/lib/calendar-mock', () => ({
  calendarMock: {
    listCalendarSets: (...args: unknown[]) => mockListCalendarSetsMock(...args),
  },
}));

const patchCalendarMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  patchCalendar: (...args: unknown[]) => patchCalendarMock(...args),
}));

import {
  ALL_CALENDARS_SET_ID,
  activateSet,
  allCalendarsSet,
  calendarsMatchSet,
  fetchCalendarSets,
  getActiveSetId,
  setActiveSetId,
} from '@/lib/set-data';

function cal(id: string, isVisible: boolean): Calendar {
  return {
    id,
    accountId: 'acct-1',
    name: id,
    color: '#000000',
    timeZone: 'UTC',
    isPrimary: false,
    isVisible,
    canWrite: true,
  };
}

const WORK_SET: CalendarSet = {
  id: 'set-work',
  name: 'Work',
  calendarIds: ['cal-work', 'cal-team'],
  position: 0,
};

beforeEach(() => {
  vi.clearAllMocks();
  demoState.value = false;
  window.localStorage.clear();
});

describe('fetchCalendarSets', () => {
  it('falls back to the demo mock when the API fails and DEMO_MODE is on', async () => {
    demoState.value = true;
    listCalendarSetsMock.mockRejectedValue(new Error('offline'));
    mockListCalendarSetsMock.mockReturnValue([WORK_SET]);

    const result = await fetchCalendarSets();

    expect(result).toEqual([WORK_SET]);
  });

  it('propagates the API failure outside demo mode', async () => {
    listCalendarSetsMock.mockRejectedValue(new Error('offline'));
    await expect(fetchCalendarSets()).rejects.toThrow('offline');
    expect(mockListCalendarSetsMock).not.toHaveBeenCalled();
  });
});

describe('activateSet', () => {
  it('PATCHes only calendars whose visibility must change, partitioned into visible/hidden', async () => {
    patchCalendarMock.mockImplementation((id: string, patch: unknown) =>
      Promise.resolve({ ...cal(id, false), ...(patch as object) })
    );
    const calendars = [
      cal('cal-work', true), // in set, already visible -> no patch
      cal('cal-team', false), // in set, hidden -> patch visible
      cal('cal-personal', true), // not in set, visible -> patch hidden
      cal('cal-other', false), // not in set, already hidden -> no patch
    ];

    await activateSet(WORK_SET, calendars);

    expect(patchCalendarMock).toHaveBeenCalledTimes(2);
    expect(patchCalendarMock).toHaveBeenCalledWith('cal-team', { isVisible: true });
    expect(patchCalendarMock).toHaveBeenCalledWith('cal-personal', { isVisible: false });
  });

  it('persists the set id as the active-set preference on success', async () => {
    patchCalendarMock.mockResolvedValue(cal('cal-work', true));
    await activateSet(WORK_SET, [cal('cal-work', false)]);
    expect(getActiveSetId()).toBe('set-work');
  });
});

describe('allCalendarsSet', () => {
  it('builds a pseudo-set whose calendarIds cover every loaded calendar', () => {
    const calendars = [cal('cal-a', true), cal('cal-b', false)];
    const set = allCalendarsSet(calendars);
    expect(set.id).toBe(ALL_CALENDARS_SET_ID);
    expect(set.calendarIds).toEqual(['cal-a', 'cal-b']);
  });

  it('activating it makes every calendar visible', async () => {
    patchCalendarMock.mockResolvedValue(cal('cal-b', true));
    const calendars = [cal('cal-a', true), cal('cal-b', false)];
    await activateSet(allCalendarsSet(calendars), calendars);
    expect(patchCalendarMock).toHaveBeenCalledTimes(1);
    expect(patchCalendarMock).toHaveBeenCalledWith('cal-b', { isVisible: true });
  });
});

describe('calendarsMatchSet', () => {
  it('is true when visibility exactly matches set membership', () => {
    const calendars = [cal('cal-work', true), cal('cal-team', true), cal('cal-personal', false)];
    expect(calendarsMatchSet(calendars, WORK_SET)).toBe(true);
  });

  it('is false once a calendar has been manually toggled out of sync', () => {
    const calendars = [cal('cal-work', true), cal('cal-team', false), cal('cal-personal', false)];
    expect(calendarsMatchSet(calendars, WORK_SET)).toBe(false);
  });
});

describe('getActiveSetId / setActiveSetId', () => {
  it('returns null when nothing is stored', () => {
    expect(getActiveSetId()).toBeNull();
  });

  it('round-trips an id through localStorage', () => {
    setActiveSetId('set-work');
    expect(getActiveSetId()).toBe('set-work');
  });

  it('clears the stored id when set to null', () => {
    setActiveSetId('set-work');
    setActiveSetId(null);
    expect(getActiveSetId()).toBeNull();
  });
});
