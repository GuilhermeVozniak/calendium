import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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
  TimeGrid: () => <div data-testid="view-timegrid" />,
}));
vi.mock('@/components/app/calendar/quick-add-bar', () => ({
  QuickAddBar: () => <input aria-label="Quick add event" />,
}));
vi.mock('@/components/app/calendar/template-manager', () => ({
  TemplateManager: () => null,
}));
vi.mock('@/components/app/event-dialog', () => ({
  EventDialog: ({ open }: { open: boolean }) =>
    open ? (
      <div role="dialog" data-state="open" data-testid="event-dialog">
        Event dialog
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
const fetchCalendarSetsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
  patchCalendar: (...args: unknown[]) => patchCalendarMock(...args),
  fetchEvents: (...args: unknown[]) => fetchEventsMock(...args),
  fetchBusyEvents: (...args: unknown[]) => fetchBusyEventsMock(...args),
  fetchCalendarSets: (...args: unknown[]) => fetchCalendarSetsMock(...args),
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
  fetchCalendarsMock.mockResolvedValue([CALENDAR_A, CALENDAR_B]);
  fetchAccountsMock.mockResolvedValue([]);
  fetchEventsMock.mockResolvedValue([]);
  fetchBusyEventsMock.mockResolvedValue([]);
  fetchCalendarSetsMock.mockResolvedValue([]);
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

  it('toggles every calendar in the set off, then back on, for a "toggle-set" command', async () => {
    fetchCalendarSetsMock.mockResolvedValue([
      { id: 'set-1', name: 'Work', calendarIds: ['cal-a', 'cal-b'], position: 0 },
    ]);
    patchCalendarMock.mockImplementation((id: string, patch: { isVisible: boolean }) =>
      Promise.resolve({ ...(id === 'cal-a' ? CALENDAR_A : CALENDAR_B), ...patch })
    );
    await renderReady();

    dispatchCalendarCommand({ type: 'toggle-set', setId: 'set-1' });

    await waitFor(() => {
      expect(patchCalendarMock).toHaveBeenCalledWith('cal-a', { isVisible: false });
      expect(patchCalendarMock).toHaveBeenCalledWith('cal-b', { isVisible: false });
    });
  });
});
