import type { Calendar, CalendarSet } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { SetSwitcher } from '@/components/app/calendar/set-switcher';

// ---------------------------------------------------------------------------
// Mocks
//
// '@/lib/calendar-data' and '@/lib/api' are mocked (the seams set-data.ts
// itself calls out to); '@/lib/set-data' is left UNMOCKED so activateSet's
// real visible/hidden partitioning logic runs and the resulting
// patchCalendar calls can be asserted here, on top of the direct unit
// coverage in lib/set-data.test.ts.
// ---------------------------------------------------------------------------

const fetchCalendarsMock = vi.fn();
const patchCalendarMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
  patchCalendar: (...args: unknown[]) => patchCalendarMock(...args),
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

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

function cal(id: string, isVisible: boolean, name = id): Calendar {
  return {
    id,
    accountId: 'acct-1',
    name,
    color: '#3b82f6',
    timeZone: 'UTC',
    isPrimary: false,
    isVisible,
    canWrite: true,
  };
}

const CAL_WORK = cal('cal-work', true, 'Work');
const CAL_TEAM = cal('cal-team', false, 'Team');
const CAL_PERSONAL = cal('cal-personal', true, 'Personal');
const CAL_OTHER = cal('cal-other', false, 'Other');

const SET_WORK: CalendarSet = {
  id: 'set-work',
  name: 'Work',
  calendarIds: ['cal-work', 'cal-team'],
  position: 0,
};

const SET_TEAM: CalendarSet = {
  id: 'set-team',
  name: 'Team',
  calendarIds: ['cal-team'],
  position: 1,
};

/** A promise plus externally-callable resolve, for pausing an async mock mid-flight. */
function createDeferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function renderSwitcher() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onOpenChange = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SetSwitcher open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
  return { ...utils, onOpenChange, queryClient };
}

// Mutable backing store so patchCalendar's effect is visible on the next
// fetchCalendars call - mirrors the real API (a PATCH mutates server state,
// so a subsequent GET reflects it), which the self-heal effect relies on.
let calendarsState: Calendar[] = [];

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  calendarsState = [CAL_WORK, CAL_TEAM, CAL_PERSONAL, CAL_OTHER].map((c) => ({ ...c }));
  fetchCalendarsMock.mockImplementation(() => Promise.resolve(calendarsState.map((c) => ({ ...c }))));
  listCalendarSetsMock.mockResolvedValue([SET_WORK]);
  // Async and non-synchronous: the backing array is only mutated once the
  // (deferred, macrotask-delayed) promise actually settles, not the instant
  // patchCalendar is called - mirroring a real PATCH request and giving the
  // race in apply's onSuccess (see set-switcher.tsx) room to manifest.
  patchCalendarMock.mockImplementation((id: string, patch: Partial<Calendar>) => {
    return new Promise((resolve) => {
      setTimeout(() => {
        const target = calendarsState.find((c) => c.id === id);
        if (target) Object.assign(target, patch);
        resolve({ ...(target ?? cal(id, false)), ...patch });
      }, 0);
    });
  });
  createCalendarSetMock.mockResolvedValue({ ...SET_WORK, id: 'set-new' });
  updateCalendarSetMock.mockResolvedValue({ ...SET_WORK });
  deleteCalendarSetMock.mockResolvedValue(undefined);
});

describe('SetSwitcher — listing', () => {
  it('lists sets fetched from the API, including the built-in All calendars set', async () => {
    renderSwitcher();
    expect(await screen.findByText('Work')).toBeInTheDocument();
    expect(screen.getByText('All calendars')).toBeInTheDocument();
  });
});

describe('SetSwitcher — activation', () => {
  it('applying a set PATCHes only calendars whose visibility must change, partitioned visible/hidden', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: 'Apply set Work' }));

    await waitFor(() => expect(patchCalendarMock).toHaveBeenCalledTimes(2));
    expect(patchCalendarMock).toHaveBeenCalledWith('cal-team', { isVisible: true });
    expect(patchCalendarMock).toHaveBeenCalledWith('cal-personal', { isVisible: false });
    expect(patchCalendarMock).not.toHaveBeenCalledWith('cal-work', expect.anything());
    expect(patchCalendarMock).not.toHaveBeenCalledWith('cal-other', expect.anything());
    expect(toastSuccess).toHaveBeenCalledWith('Applied "Work"');
  });

  it('shows an Active badge on the set after it is applied', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');
    await user.click(screen.getByRole('button', { name: 'Apply set Work' }));
    await waitFor(() => expect(patchCalendarMock).toHaveBeenCalledTimes(2));

    expect(await screen.findByText('Active')).toBeInTheDocument();
    expect(window.localStorage.getItem('calendium.activeCalendarSet')).toBe('set-work');
  });

  it('clears the active badge once a manual per-calendar toggle diverges from the active set', async () => {
    window.localStorage.setItem('calendium.activeCalendarSet', 'set-work');
    // Calendars already match SET_WORK exactly (cal-work + cal-team visible only).
    fetchCalendarsMock.mockResolvedValue([
      cal('cal-work', true, 'Work'),
      cal('cal-team', true, 'Team'),
      cal('cal-personal', false, 'Personal'),
    ]);

    const { queryClient } = renderSwitcher();
    await screen.findByText('Work');
    expect(await screen.findByText('Active')).toBeInTheDocument();

    // Simulate a manual toggle elsewhere in the app (e.g. the sidebar's own
    // per-calendar checkbox) invalidating/refreshing the shared ['calendars']
    // cache with a now-diverged visibility state.
    queryClient.setQueryData(
      ['calendars'],
      [
        cal('cal-work', true, 'Work'),
        cal('cal-team', false, 'Team'), // manually hidden -> no longer matches the set
        cal('cal-personal', false, 'Personal'),
      ]
    );

    await waitFor(() => expect(screen.queryByText('Active')).not.toBeInTheDocument());
    expect(window.localStorage.getItem('calendium.activeCalendarSet')).toBeNull();
  });

  it('keeps the Active badge and localStorage id once the background refetch settles, even though it races the self-heal effect', async () => {
    // The initial mount fetch resolves normally; the SECOND fetchCalendars
    // call - the refetch apply's onSuccess triggers via invalidateQueries -
    // is held open via a deferred promise so the test can assert that the
    // badge/localStorage survive regardless of when that refetch resolves.
    // Pre-fix, onSuccess only invalidates (never seeds the cache), so the
    // self-heal effect runs against the still-stale (pre-apply) ['calendars']
    // cache in the same render as the activeId update and incorrectly clears
    // it - and the null guard means it never comes back once that refetch
    // finally does resolve.
    const initialCalendars = calendarsState.map((c) => ({ ...c }));
    const deferredRefetch = createDeferred<Calendar[]>();
    let fetchCallCount = 0;
    fetchCalendarsMock.mockImplementation(() => {
      fetchCallCount += 1;
      return fetchCallCount === 1 ? Promise.resolve(initialCalendars) : deferredRefetch.promise;
    });

    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: 'Apply set Work' }));
    await waitFor(() => expect(patchCalendarMock).toHaveBeenCalledTimes(2));

    // Give the background refetch triggered by invalidateQueries a chance to
    // be dispatched (it will be, and will hang on deferredRefetch.promise).
    await waitFor(() => expect(fetchCallCount).toBeGreaterThanOrEqual(2));

    expect(await screen.findByText('Active')).toBeInTheDocument();
    expect(window.localStorage.getItem('calendium.activeCalendarSet')).toBe('set-work');

    // Now let the deferred background refetch resolve with the corrected
    // (post-apply) data.
    deferredRefetch.resolve(calendarsState.map((c) => ({ ...c })));

    expect(await screen.findByText('Active')).toBeInTheDocument();
    expect(window.localStorage.getItem('calendium.activeCalendarSet')).toBe('set-work');
  });

  it('invalidates the calendars cache on a partial-failure apply, without persisting an active id', async () => {
    // cal-personal's PATCH rejects; cal-team's succeeds - a realistic partial
    // batch failure. onError must still reconcile the sidebar against the
    // true (partially-mutated) server state.
    patchCalendarMock.mockImplementation((id: string, patch: Partial<Calendar>) => {
      if (id === 'cal-personal') return Promise.reject(new Error('patch failed'));
      return new Promise((resolve) => {
        setTimeout(() => {
          const target = calendarsState.find((c) => c.id === id);
          if (target) Object.assign(target, patch);
          resolve({ ...(target ?? cal(id, false)), ...patch });
        }, 0);
      });
    });

    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');
    const fetchCallsBefore = fetchCalendarsMock.mock.calls.length;

    await user.click(screen.getByRole('button', { name: 'Apply set Work' }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Could not apply the set'));
    await waitFor(() =>
      expect(fetchCalendarsMock.mock.calls.length).toBeGreaterThan(fetchCallsBefore)
    );
    expect(window.localStorage.getItem('calendium.activeCalendarSet')).toBeNull();
    expect(screen.queryByText('Active')).not.toBeInTheDocument();
  });
});

describe('SetSwitcher — duplicate set names', () => {
  it('rejects creating a set whose name duplicates an existing one', async () => {
    listCalendarSetsMock.mockResolvedValue([SET_WORK, SET_TEAM]);
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: /save current selection as set/i }));
    await user.type(await screen.findByLabelText('Name'), 'Team');
    await user.click(screen.getByRole('button', { name: /create set/i }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('A set with this name already exists')
    );
    expect(createCalendarSetMock).not.toHaveBeenCalled();
  });

  it('rejects renaming a set to a name that duplicates another existing set', async () => {
    listCalendarSetsMock.mockResolvedValue([SET_WORK, SET_TEAM]);
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: 'Rename set Work' }));
    const nameInput = await screen.findByLabelText('Name');
    await user.clear(nameInput);
    await user.type(nameInput, 'Team');
    await user.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('A set with this name already exists')
    );
    expect(updateCalendarSetMock).not.toHaveBeenCalled();
  });
});

describe('SetSwitcher — save current selection', () => {
  it('creates a set from the currently visible calendar ids', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: /save current selection as set/i }));
    await user.type(await screen.findByLabelText('Name'), 'My mix');
    await user.click(screen.getByRole('button', { name: /create set/i }));

    await waitFor(() => expect(createCalendarSetMock).toHaveBeenCalledTimes(1));
    const input = createCalendarSetMock.mock.calls[0]![0];
    expect(input.name).toBe('My mix');
    expect(input.calendarIds).toEqual(['cal-work', 'cal-personal']);
    expect(toastSuccess).toHaveBeenCalledWith('Set saved');
  });

  it('disables "Save current selection as set…" when zero calendars are visible', async () => {
    fetchCalendarsMock.mockResolvedValue([
      cal('cal-work', false, 'Work'),
      cal('cal-team', false, 'Team'),
    ]);
    renderSwitcher();
    await screen.findByText('Work');

    expect(screen.getByRole('button', { name: /save current selection as set/i })).toBeDisabled();
  });
});

describe('SetSwitcher — rename', () => {
  it('prefills the rename form and round-trips the new name, keeping calendarIds/position', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: 'Rename set Work' }));
    const nameInput = await screen.findByLabelText('Name');
    expect(nameInput).toHaveValue('Work');

    await user.clear(nameInput);
    await user.type(nameInput, 'Work (updated)');
    await user.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() =>
      expect(updateCalendarSetMock).toHaveBeenCalledWith('set-work', {
        name: 'Work (updated)',
        calendarIds: ['cal-work', 'cal-team'],
        position: 0,
      })
    );
    expect(toastSuccess).toHaveBeenCalledWith('Set renamed');
  });
});

describe('SetSwitcher — delete', () => {
  it('deletes a set, removes it from the list, and shows a success toast', async () => {
    const user = userEvent.setup();
    renderSwitcher();
    await screen.findByText('Work');

    await user.click(screen.getByRole('button', { name: 'Delete set Work' }));

    await waitFor(() => expect(deleteCalendarSetMock).toHaveBeenCalledWith('set-work'));
    expect(toastSuccess).toHaveBeenCalledWith('Set deleted');
    await waitFor(() => expect(screen.queryByText('Work')).not.toBeInTheDocument());
  });
});
