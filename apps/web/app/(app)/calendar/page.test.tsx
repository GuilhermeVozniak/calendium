import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Calendar } from '@calendium/shared';

import { dispatchCalendarCommand } from '@/lib/calendar-commands';
import { rangeLabel, stepAnchor } from '@/lib/calendar-views';

import CalendarPage from './page';

// ---------------------------------------------------------------------------
// Mocks — the calendar page's own keyboard/command wiring is under test here,
// not the heavy view components (month grid, time grid, …) another task owns,
// so those render as trivial stand-ins. QuickAddBar keeps the real component's
// accessible name so the "/" focus shortcut can be asserted against it.
// ---------------------------------------------------------------------------

vi.mock('@/components/app/calendar/day-ticker', () => ({
  DayTicker: () => <div data-testid="view-ticker" />,
}));
vi.mock('@/components/app/calendar/mini-month', () => ({
  MiniMonth: () => <div data-testid="mini-month" />,
}));
vi.mock('@/components/app/calendar/month-view', () => ({
  MonthView: () => <div data-testid="view-month" />,
}));
vi.mock('@/components/app/calendar/quarter-view', () => ({
  QuarterView: () => <div data-testid="view-quarter" />,
}));
vi.mock('@/components/app/calendar/year-view', () => ({
  YearView: () => <div data-testid="view-year" />,
}));
vi.mock('@/components/app/calendar/time-grid', () => ({
  TimeGrid: ({
    timeTravelZone,
    onExitTimeTravel,
  }: {
    timeTravelZone?: string | null;
    onExitTimeTravel?: () => void;
  }) => (
    <div data-testid="view-timegrid" data-time-travel-zone={timeTravelZone ?? ''}>
      {timeTravelZone && (
        <button type="button" onClick={onExitTimeTravel}>
          exit time travel (grid)
        </button>
      )}
    </div>
  ),
}));
vi.mock('@/components/app/calendar/quick-add-bar', () => ({
  QuickAddBar: () => <input aria-label="Quick add event" />,
}));
vi.mock('@/components/app/calendar/template-manager', () => ({
  TemplateManager: () => null,
}));
vi.mock('@/components/app/event-dialog', () => ({
  EventDialog: ({ open, defaults }: { open: boolean; defaults: { title?: string } | null }) =>
    open ? (
      <div role="dialog" data-state="open" data-testid="event-dialog">
        Event dialog
        {defaults?.title && <span data-testid="event-dialog-title">{defaults.title}</span>}
      </div>
    ) : null,
}));
vi.mock('@/components/app/availability', () => ({
  AvailabilityDialog: ({ open }: { open: boolean }) =>
    open ? (
      <div role="dialog" data-state="open" data-testid="availability-dialog">
        Availability dialog
      </div>
    ) : null,
}));

const fetchCalendarsMock = vi.fn();
const patchCalendarMock = vi.fn();
const fetchEventsMock = vi.fn();
const fetchBusyEventsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
  patchCalendar: (...args: unknown[]) => patchCalendarMock(...args),
  fetchEvents: (...args: unknown[]) => fetchEventsMock(...args),
  fetchBusyEvents: (...args: unknown[]) => fetchBusyEventsMock(...args),
}));

const fetchCalendarSetsMock = vi.fn();
const activateSetMock = vi.fn();
const getActiveSetIdMock = vi.fn();
const setActiveSetIdMock = vi.fn();
vi.mock('@/lib/set-data', () => ({
  fetchCalendarSets: (...args: unknown[]) => fetchCalendarSetsMock(...args),
  activateSet: (...args: unknown[]) => activateSetMock(...args),
  getActiveSetId: (...args: unknown[]) => getActiveSetIdMock(...args),
  setActiveSetId: (...args: unknown[]) => setActiveSetIdMock(...args),
  allCalendarsSet: (calendars: Calendar[]) => ({ id: '__all__', name: 'All calendars', calendarIds: calendars.map((c) => c.id), position: -1 }),
  calendarsMatchSet: () => false,
  ACTIVE_SET_STORAGE_KEY: 'test-key',
  ALL_CALENDARS_SET_ID: '__all__',
}));

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
}));

const fetchEventTemplatesMock = vi.fn();
const applyTemplateMock = vi.fn();
const recordTemplateUsageMock = vi.fn();
vi.mock('@/lib/template-data', () => ({
  fetchEventTemplates: (...args: unknown[]) => fetchEventTemplatesMock(...args),
  applyTemplate: (...args: unknown[]) => applyTemplateMock(...args),
  recordTemplateUsage: (...args: unknown[]) => recordTemplateUsageMock(...args),
}));

const toastErrorMock = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastErrorMock(...args),
  },
}));

const CALENDAR_A = {
  id: 'cal-a',
  accountId: 'acc-1',
  name: 'Personal',
  color: '#4285F4',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};
const CALENDAR_B = {
  id: 'cal-b',
  accountId: 'acc-1',
  name: 'Team',
  color: '#0F9D58',
  isPrimary: false,
  isVisible: true,
  canWrite: true,
};

const NOW = new Date('2026-07-15T12:00:00.000Z'); // Wednesday

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <CalendarPage />
    </QueryClientProvider>
  );
}

/** Waits past the mount-gated skeleton and the initial data fetches. */
async function renderReady() {
  renderPage();
  await waitFor(() => expect(screen.getByText(rangeLabel('week', NOW))).toBeInTheDocument());
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: NOW });
  vi.clearAllMocks();
  window.localStorage.clear();
  fetchCalendarsMock.mockResolvedValue([CALENDAR_A, CALENDAR_B]);
  fetchAccountsMock.mockResolvedValue([]);
  fetchEventsMock.mockResolvedValue([]);
  fetchBusyEventsMock.mockResolvedValue([]);
  fetchCalendarSetsMock.mockResolvedValue([]);
  activateSetMock.mockResolvedValue(undefined);
  getActiveSetIdMock.mockReturnValue(null);
  setActiveSetIdMock.mockReturnValue(undefined);
  fetchEventTemplatesMock.mockResolvedValue([]);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('CalendarPage — keyboard shortcuts', () => {
  it('defaults to week view anchored at today', async () => {
    await renderReady();
    expect(screen.getByText(rangeLabel('week', NOW))).toBeInTheDocument();
  });

  it('switches views via VIEW_KEYS (d/w/m/q/y/a)', async () => {
    await renderReady();

    fireEvent.keyDown(document.body, { key: 'm' });
    await waitFor(() => expect(screen.getByTestId('view-month')).toBeInTheDocument());
    expect(screen.getByText(rangeLabel('month', NOW))).toBeInTheDocument();

    fireEvent.keyDown(document.body, { key: 'q' });
    await waitFor(() => expect(screen.getByTestId('view-quarter')).toBeInTheDocument());

    fireEvent.keyDown(document.body, { key: 'y' });
    await waitFor(() => expect(screen.getByTestId('view-year')).toBeInTheDocument());

    fireEvent.keyDown(document.body, { key: 'a' });
    await waitFor(() => expect(screen.getByTestId('view-ticker')).toBeInTheDocument());

    fireEvent.keyDown(document.body, { key: 'd' });
    await waitFor(() => expect(screen.getByText(rangeLabel('day', NOW))).toBeInTheDocument());

    fireEvent.keyDown(document.body, { key: 'w' });
    await waitFor(() => expect(screen.getByText(rangeLabel('week', NOW))).toBeInTheDocument());
  });

  it('moves the anchor forward/back by the view\'s step on j/k', async () => {
    await renderReady();

    fireEvent.keyDown(document.body, { key: 'j' });
    const next = stepAnchor('week', NOW, 1);
    await waitFor(() => expect(screen.getByText(rangeLabel('week', next))).toBeInTheDocument());

    fireEvent.keyDown(document.body, { key: 'k' });
    fireEvent.keyDown(document.body, { key: 'k' });
    const prev = stepAnchor('week', next, -1);
    const prevPrev = stepAnchor('week', prev, -1);
    await waitFor(() =>
      expect(screen.getByText(rangeLabel('week', prevPrev))).toBeInTheDocument()
    );
  });

  it('re-anchors to today on t after navigating away', async () => {
    await renderReady();

    fireEvent.keyDown(document.body, { key: 'j' });
    await waitFor(() =>
      expect(screen.queryByText(rangeLabel('week', NOW))).not.toBeInTheDocument()
    );

    fireEvent.keyDown(document.body, { key: 't' });
    await waitFor(() => expect(screen.getByText(rangeLabel('week', NOW))).toBeInTheDocument());
  });

  it('opens the new-event dialog on c', async () => {
    await renderReady();
    fireEvent.keyDown(document.body, { key: 'c' });
    await waitFor(() => expect(screen.getByTestId('event-dialog')).toBeInTheDocument());
  });

  it('opens the share-availability dialog on s', async () => {
    await renderReady();
    fireEvent.keyDown(document.body, { key: 's' });
    await waitFor(() => expect(screen.getByTestId('availability-dialog')).toBeInTheDocument());
  });

  it('focuses the quick-add input on /', async () => {
    await renderReady();
    fireEvent.keyDown(document.body, { key: '/' });
    await waitFor(() =>
      expect(screen.getByLabelText('Quick add event')).toHaveFocus()
    );
  });

  it('does not fire shortcuts while an input has focus', async () => {
    await renderReady();
    const input = screen.getByLabelText('Quick add event');
    input.focus();
    fireEvent.keyDown(input, { key: 'm' });
    expect(screen.queryByTestId('view-month')).not.toBeInTheDocument();
  });
});

describe('CalendarPage — cross-route palette commands', () => {
  it('reacts to a "today" command dispatched from the palette', async () => {
    await renderReady();
    fireEvent.keyDown(document.body, { key: 'j' });
    await waitFor(() =>
      expect(screen.queryByText(rangeLabel('week', NOW))).not.toBeInTheDocument()
    );

    dispatchCalendarCommand({ type: 'today' });
    await waitFor(() => expect(screen.getByText(rangeLabel('week', NOW))).toBeInTheDocument());
  });

  it('reacts to a "view" command dispatched from the palette', async () => {
    await renderReady();
    dispatchCalendarCommand({ type: 'view', view: 'month' });
    await waitFor(() => expect(screen.getByTestId('view-month')).toBeInTheDocument());
  });

  it('reacts to a "new-event" command dispatched from the palette', async () => {
    await renderReady();
    dispatchCalendarCommand({ type: 'new-event' });
    await waitFor(() => expect(screen.getByTestId('event-dialog')).toBeInTheDocument());
  });

  it('reacts to a "share-availability" command dispatched from the palette', async () => {
    await renderReady();
    dispatchCalendarCommand({ type: 'share-availability' });
    await waitFor(() => expect(screen.getByTestId('availability-dialog')).toBeInTheDocument());
  });

  it('applies a set via activateSet (exclusive activation, not an additive toggle) for a "toggle-set" command', async () => {
    const SET = { id: 'set-1', name: 'Work', calendarIds: ['cal-a', 'cal-b'], position: 0 };
    fetchCalendarSetsMock.mockResolvedValue([SET]);
    await renderReady();

    dispatchCalendarCommand({ type: 'toggle-set', setId: 'set-1' });

    await waitFor(() => {
      expect(activateSetMock).toHaveBeenCalledWith(SET, [CALENDAR_A, CALENDAR_B]);
    });
  });

  it('ignores an unknown set id without throwing', async () => {
    fetchCalendarSetsMock.mockResolvedValue([
      { id: 'set-1', name: 'Work', calendarIds: ['cal-a'], position: 0 },
    ]);
    await renderReady();

    dispatchCalendarCommand({ type: 'toggle-set', setId: 'does-not-exist' });

    await waitFor(() => expect(fetchCalendarSetsMock).toHaveBeenCalled());
    expect(activateSetMock).not.toHaveBeenCalled();
  });

  it('toasts an error and does not crash when applying a set fails', async () => {
    fetchCalendarSetsMock.mockResolvedValue([
      { id: 'set-1', name: 'Work', calendarIds: ['cal-a'], position: 0 },
    ]);
    activateSetMock.mockRejectedValueOnce(new Error('network'));
    await renderReady();

    dispatchCalendarCommand({ type: 'toggle-set', setId: 'set-1' });

    await waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith('Could not apply the set'));
  });

  it('toasts an error when loading a template for a "new-from-template" command fails', async () => {
    fetchEventTemplatesMock.mockRejectedValueOnce(new Error('network'));
    await renderReady();

    dispatchCalendarCommand({ type: 'new-from-template', templateId: 'tpl-1' });

    await waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith('Could not load templates'));
    expect(screen.queryByTestId('event-dialog')).not.toBeInTheDocument();
  });
});

describe('CalendarPage — ?template= deep link', () => {
  const TEMPLATE = {
    id: 'tpl-1',
    name: '1:1',
    title: '1:1 with teammate',
    description: '',
    location: '',
    durationMinutes: 30,
    allDay: false,
    calendarId: null,
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [],
    recurrenceRule: null,
    usageCount: 0,
  };

  afterEach(() => {
    window.history.pushState({}, '', '/calendar');
  });

  it('mounts with a known ?template= id: opens the dialog prefilled and strips the param', async () => {
    window.history.pushState({}, '', '/calendar?template=tpl-1');
    fetchEventTemplatesMock.mockResolvedValue([TEMPLATE]);
    applyTemplateMock.mockReturnValue({ title: TEMPLATE.title });

    await renderReady();

    await waitFor(() => expect(screen.getByTestId('event-dialog')).toBeInTheDocument());
    expect(screen.getByTestId('event-dialog-title')).toHaveTextContent(TEMPLATE.title);
    expect(recordTemplateUsageMock).toHaveBeenCalledWith('tpl-1');
    expect(window.location.search).toBe('');
  });

  it('mounts with an unknown ?template= id: no dialog opens, the param is stripped, and it does not crash', async () => {
    window.history.pushState({}, '', '/calendar?template=does-not-exist');
    fetchEventTemplatesMock.mockResolvedValue([TEMPLATE]);

    await renderReady();

    await waitFor(() => expect(window.location.search).toBe(''));
    expect(screen.queryByTestId('event-dialog')).not.toBeInTheDocument();
  });

  it('does not refire the ?template= effect on rerender (once-guard)', async () => {
    window.history.pushState({}, '', '/calendar?template=tpl-1');
    fetchEventTemplatesMock.mockResolvedValue([TEMPLATE]);

    await renderReady();
    await waitFor(() => expect(fetchEventTemplatesMock).toHaveBeenCalledTimes(1));

    // A rerender-causing state update (view switch, dispatched via the
    // command bus rather than a raw keydown - the event dialog opened above
    // suppresses keyboard shortcuts while open) must not refire the
    // mount-once ?template= effect.
    dispatchCalendarCommand({ type: 'view', view: 'month' });
    await waitFor(() => expect(screen.getByTestId('view-month')).toBeInTheDocument());

    expect(fetchEventTemplatesMock).toHaveBeenCalledTimes(1);
  });
});

describe('CalendarPage — Time Travel overlay', () => {
  it('opens the Time Travel search popover on shift+z', async () => {
    await renderReady();
    expect(screen.queryByPlaceholderText('Jump to a city…')).not.toBeInTheDocument();

    fireEvent.keyDown(document.body, { key: 'Z', shiftKey: true });
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Jump to a city…')).toBeInTheDocument()
    );
  });

  it('reacts to a "time-travel" command dispatched from the palette by opening the search popover', async () => {
    await renderReady();
    dispatchCalendarCommand({ type: 'time-travel' });
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Jump to a city…')).toBeInTheDocument()
    );
  });

  it('applies a picked city to the grid overlay and persists it to localStorage', async () => {
    await renderReady();

    fireEvent.keyDown(document.body, { key: 'Z', shiftKey: true });
    const search = await screen.findByPlaceholderText('Jump to a city…');
    fireEvent.change(search, { target: { value: 'Tokyo' } });
    fireEvent.click(await screen.findByText('Asia/Tokyo'));

    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute(
        'data-time-travel-zone',
        'Asia/Tokyo'
      )
    );
    expect(window.localStorage.getItem('calendium.timetravel')).toBe('Asia/Tokyo');
  });

  it('restores a previously persisted Time Travel zone from localStorage on mount', async () => {
    window.localStorage.setItem('calendium.timetravel', 'Europe/London');
    await renderReady();

    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute(
        'data-time-travel-zone',
        'Europe/London'
      )
    );
  });

  it('exits Time Travel on a second shift+z once a zone is active', async () => {
    window.localStorage.setItem('calendium.timetravel', 'Europe/London');
    await renderReady();
    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute(
        'data-time-travel-zone',
        'Europe/London'
      )
    );

    fireEvent.keyDown(document.body, { key: 'Z', shiftKey: true });

    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute('data-time-travel-zone', '')
    );
    expect(window.localStorage.getItem('calendium.timetravel')).toBeNull();
  });

  it('exits Time Travel from the grid\'s own exit control', async () => {
    window.localStorage.setItem('calendium.timetravel', 'Europe/London');
    await renderReady();
    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute(
        'data-time-travel-zone',
        'Europe/London'
      )
    );

    fireEvent.click(screen.getByRole('button', { name: 'exit time travel (grid)' }));

    await waitFor(() =>
      expect(screen.getByTestId('view-timegrid')).toHaveAttribute('data-time-travel-zone', '')
    );
    expect(window.localStorage.getItem('calendium.timetravel')).toBeNull();
  });
});
