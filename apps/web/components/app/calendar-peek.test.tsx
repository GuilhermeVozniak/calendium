import type { Calendar, Event } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CalendarPeek, readStoredCalendarPeekOpen } from '@/components/app/calendar-peek';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchCalendarsMock = vi.fn();
const fetchEventsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
  fetchEvents: (...args: unknown[]) => fetchEventsMock(...args),
  createEventApi: vi.fn().mockResolvedValue({ id: 'ev-new' }),
  updateEventApi: vi.fn().mockResolvedValue({ id: 'ev1' }),
  deleteEventApi: vi.fn().mockResolvedValue(undefined),
  sendRsvpApi: vi.fn().mockResolvedValue({ id: 'ev1' }),
}));

// A module-level constant (not a fresh Set per call) — EventDialog's mount
// effect depends on `selfEmails` by reference, so a new Set on every render
// would re-fire that effect every render and infinite-loop.
const SELF_EMAILS = new Set<string>(['me@example.com']);
vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => SELF_EMAILS,
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

// ---------------------------------------------------------------------------
// Fixtures — "now" is pinned to Jul 7, 2026 09:00 local time.
// ---------------------------------------------------------------------------

const NOW = new Date(2026, 6, 7, 9, 0, 0);

const CAL: Calendar = {
  id: 'cal-1',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

function at(hours: number, minutes = 0): Date {
  return new Date(2026, 6, 7, hours, minutes, 0);
}

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: CAL.id,
    title: 'Event',
    description: null,
    location: null,
    start: at(10, 0).toISOString(),
    end: at(11, 0).toISOString(),
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
    ...overrides,
  };
}

function renderPeek(overrides: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const onOpenChange = overrides.onOpenChange ?? vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <CalendarPeek open={overrides.open ?? true} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
  return { ...utils, onOpenChange, queryClient };
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  fetchCalendarsMock.mockResolvedValue([CAL]);
  fetchEventsMock.mockResolvedValue([]);
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * Pins Date/now() to NOW for the assertions that depend on "past vs. future"
 * or the conference join window. shouldAdvanceTime keeps the fake clock
 * ticking in lockstep with real wall time so RTL's waitFor (which polls via
 * real setTimeout) still resolves. Scoped per-test (not global) because
 * mounting EventDialog while these fake timers are active spins CPU forever —
 * some Radix/EventDialog effect never settles under a faked clock, so tests
 * that open EventDialog run under real timers instead.
 */
function useFrozenNow() {
  vi.useFakeTimers({ shouldAdvanceTime: true, now: NOW });
}

describe('CalendarPeek', () => {
  it('renders null when closed', () => {
    const { container } = renderPeek({ open: false });
    expect(container).toBeEmptyDOMElement();
  });

  it("renders today's events from a fixture", async () => {
    useFrozenNow();
    fetchEventsMock.mockResolvedValue([
      makeEvent({ id: 'evt-standup', title: 'Standup', start: at(8, 0).toISOString(), end: at(8, 15).toISOString() }),
    ]);
    renderPeek();
    await waitFor(() => expect(screen.getAllByText('Standup').length).toBeGreaterThan(0));
  });

  it('next-event card picks the first non-past event', async () => {
    useFrozenNow();
    fetchEventsMock.mockResolvedValue([
      makeEvent({
        id: 'evt-past',
        title: 'Morning Standup',
        start: at(8, 0).toISOString(),
        end: at(8, 15).toISOString(),
      }),
      makeEvent({
        id: 'evt-next',
        title: 'Design Review',
        start: at(9, 30).toISOString(),
        end: at(10, 0).toISOString(),
      }),
      makeEvent({
        id: 'evt-later',
        title: 'Roadmap Sync',
        start: at(11, 0).toISOString(),
        end: at(12, 0).toISOString(),
      }),
    ]);
    renderPeek();
    const card = await screen.findByTestId('peek-next-event');
    expect(within(card).getByText('Design Review')).toBeInTheDocument();
    expect(within(card).queryByText('Morning Standup')).not.toBeInTheDocument();
    expect(within(card).queryByText('Roadmap Sync')).not.toBeInTheDocument();
  });

  it('shows a Join button for an in-window conference event', async () => {
    useFrozenNow();
    fetchEventsMock.mockResolvedValue([
      makeEvent({
        id: 'evt-live',
        title: 'Client Call',
        start: at(8, 55).toISOString(),
        end: at(9, 30).toISOString(),
        conferencing: { provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' },
      }),
    ]);
    renderPeek();
    const card = await screen.findByTestId('peek-next-event');
    expect(within(card).getByRole('link', { name: /join/i })).toBeInTheDocument();
  });

  it('hides the Join button when the conference event is not yet in its join window', async () => {
    useFrozenNow();
    fetchEventsMock.mockResolvedValue([
      makeEvent({
        id: 'evt-future',
        title: 'Later Sync',
        start: at(10, 0).toISOString(),
        end: at(10, 30).toISOString(),
        conferencing: { provider: 'zoom', url: 'https://zoom.us/j/1234567890' },
      }),
    ]);
    renderPeek();
    const card = await screen.findByTestId('peek-next-event');
    expect(within(card).queryByRole('link', { name: /join/i })).not.toBeInTheDocument();
  });

  it('toggles between Day and Week modes, revealing a 7-day strip in Week mode', async () => {
    const user = userEvent.setup();
    renderPeek();
    await waitFor(() => expect(fetchCalendarsMock).toHaveBeenCalled());
    expect(screen.queryAllByTestId(/^peek-day-/)).toHaveLength(0);
    await user.click(screen.getByRole('tab', { name: 'Week' }));
    await waitFor(() => expect(screen.getAllByTestId(/^peek-day-/)).toHaveLength(7));
  });

  it('opens the New event dialog', async () => {
    renderPeek();
    await waitFor(() => expect(fetchCalendarsMock).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: /new event/i }));
    expect(await screen.findByPlaceholderText('Add title')).toBeInTheDocument();
  });

  describe('open-state persistence', () => {
    const KEY = 'calendium.calendarPeek';

    it('reads false when nothing is stored', () => {
      expect(readStoredCalendarPeekOpen()).toBe(false);
    });

    it('reads back a previously stored value', () => {
      window.localStorage.setItem(KEY, '1');
      expect(readStoredCalendarPeekOpen()).toBe(true);
    });

    it('persists open/closed transitions to localStorage', () => {
      const { rerender, queryClient } = renderPeek({ open: true });
      expect(window.localStorage.getItem(KEY)).toBe('1');
      rerender(
        <QueryClientProvider client={queryClient}>
          <CalendarPeek open={false} onOpenChange={vi.fn()} />
        </QueryClientProvider>
      );
      expect(window.localStorage.getItem(KEY)).toBe('0');
    });
  });
});
