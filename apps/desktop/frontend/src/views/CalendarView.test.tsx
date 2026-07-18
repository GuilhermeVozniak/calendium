import type { Calendar, Event } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CalendarView, emitFocusDate } from './CalendarView';

// ---------------------------------------------------------------------------
// CalendarView always calls orMock(real, mock); in this jsdom test env
// isDemoMode() defaults false (no server-config / demo flag in localStorage),
// so the *real* api branch would run unless '@/lib/api' is replaced outright.
// Mocking it makes every query go through the mock callback deterministically,
// and mocking '@/lib/mock' supplies test-controlled fixtures instead of the
// app's built-in demo dataset — same approach the existing lib/*.test.ts
// suite uses for isolating modules under test.
// ---------------------------------------------------------------------------

const fixtures = vi.hoisted(() => ({
  events: [] as Event[],
  calendars: [] as Calendar[],
}));

vi.mock('@/lib/api', () => ({
  api: {
    listEvents: vi.fn(),
    listCalendars: vi.fn(),
    getMe: vi.fn(),
    createEvent: vi.fn(),
    updateEvent: vi.fn(),
    deleteEvent: vi.fn(),
    rsvp: vi.fn(),
  },
  orMock: async (_real: () => unknown, mock: () => unknown) => mock(),
}));

vi.mock('@/lib/mock', () => ({
  mockEvents: (from: string, to: string) =>
    fixtures.events.filter((e) => {
      const t = new Date(e.start).getTime();
      return t >= new Date(from).getTime() && t < new Date(to).getTime();
    }),
  get mockCalendars() {
    return fixtures.calendars;
  },
  mockUser: {
    id: 'usr_me',
    email: 'me@calendium.app',
    name: 'Me',
    avatarUrl: null,
    createdAt: new Date().toISOString(),
  },
}));

function makeCalendar(overrides: Partial<Calendar> = {}): Calendar {
  return {
    id: 'cal-1',
    accountId: 'acc-1',
    name: 'Work',
    color: '#3b82f6',
    timeZone: 'UTC',
    isPrimary: true,
    isVisible: true,
    canWrite: true,
    ...overrides,
  };
}

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    title: 'Event',
    description: null,
    location: null,
    start: new Date().toISOString(),
    end: new Date(Date.now() + 30 * 60_000).toISOString(),
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

function renderCalendar() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <CalendarView />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  fixtures.events = [];
  fixtures.calendars = [makeCalendar()];
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('CalendarView', () => {
  it('renders a Join control for a Zoom-in-location fixture', async () => {
    const now = new Date();
    fixtures.events = [
      makeEvent({
        id: 'evt-zoom',
        title: 'Vendor sync',
        location: 'Join via https://zoom.us/j/1234567890?pwd=abc',
        start: now.toISOString(),
        end: new Date(now.getTime() + 30 * 60_000).toISOString(),
      }),
    ];
    renderCalendar();
    // Anchored: the event card wrapper is itself `role="button"` (it hosts
    // the real Join <button> inline, so it can't be a <button> too — nesting
    // interactive elements is invalid HTML) and its accessible name includes
    // the whole card's text, which also contains "Join Zoom" as a substring.
    expect(await screen.findByRole('button', { name: /^join zoom$/i })).toBeTruthy();
  });

  it('renders no Join control when no conference link is present', async () => {
    const now = new Date();
    fixtures.events = [
      makeEvent({ id: 'evt-plain', title: 'Plain meeting', start: now.toISOString() }),
    ];
    renderCalendar();
    await screen.findByText('Plain meeting');
    expect(screen.queryByRole('button', { name: /join/i })).toBeNull();
  });

  it('renders the right number of month-grid cells for a 5-week and a 6-week month', async () => {
    renderCalendar();
    await screen.findByText('Today');

    act(() => emitFocusDate(new Date(2026, 0, 15).toISOString()));
    fireEvent.keyDown(window, { key: 'm' });
    // January 2026 (Monday-start weeks, matching this view's WEEK_OPTS):
    // Jan 1 is a Thursday, full leading/trailing weeks -> 5 weeks -> 35 cells.
    await waitFor(() => expect(screen.getAllByRole('gridcell')).toHaveLength(35));

    act(() => emitFocusDate(new Date(2026, 2, 15).toISOString()));
    // March 2026: Mar 1 is a Sunday, pushing the trailing week into a 6th row
    // under Monday-start weeks -> 42 cells.
    await waitFor(() => expect(screen.getAllByRole('gridcell')).toHaveLength(42));
  });

  it('switches to a single-column day view on the d key and back to week on w', async () => {
    renderCalendar();
    await screen.findByText('Today');

    fireEvent.keyDown(window, { key: 'd' });
    await waitFor(() =>
      expect(screen.getAllByText(/^(mon|tue|wed|thu|fri|sat|sun)$/i)).toHaveLength(1)
    );

    fireEvent.keyDown(window, { key: 'w' });
    await waitFor(() =>
      expect(screen.getAllByText(/^(mon|tue|wed|thu|fri|sat|sun)$/i)).toHaveLength(7)
    );
  });

  it('steps the anchor forward/back a week with j/k and resets with t', async () => {
    renderCalendar();
    const initialHeading = await screen.findByText(/^week of/i);
    const initialText = initialHeading.textContent;

    fireEvent.keyDown(window, { key: 'j' });
    await waitFor(() => expect(screen.getByText(/^week of/i).textContent).not.toBe(initialText));

    fireEvent.keyDown(window, { key: 't' });
    await waitFor(() => expect(screen.getByText(/^week of/i).textContent).toBe(initialText));
  });

  it('warns about a double-booking conflict when editing an overlapping event', async () => {
    const now = new Date();
    now.setMinutes(0, 0, 0);
    const a = makeEvent({
      id: 'evt-a',
      title: 'Design review',
      start: now.toISOString(),
      end: new Date(now.getTime() + 60 * 60_000).toISOString(),
    });
    const b = makeEvent({
      id: 'evt-b',
      title: 'Overlapping 1:1',
      start: new Date(now.getTime() + 30 * 60_000).toISOString(),
      end: new Date(now.getTime() + 90 * 60_000).toISOString(),
    });
    fixtures.events = [a, b];
    renderCalendar();

    const card = await screen.findByText('Design review');
    fireEvent.click(card);

    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText(/overlapping 1:1/i)).toBeTruthy();
  });

  it('does not warn when editing an event with no overlap', async () => {
    const now = new Date();
    now.setMinutes(0, 0, 0);
    fixtures.events = [
      makeEvent({
        id: 'evt-solo',
        title: 'Solo focus block',
        start: now.toISOString(),
        end: new Date(now.getTime() + 60 * 60_000).toISOString(),
      }),
    ];
    renderCalendar();

    const card = await screen.findByText('Solo focus block');
    fireEvent.click(card);
    await screen.findByLabelText('Title');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('passes addConferencing on create when the toggle is checked', async () => {
    const { api } = await import('@/lib/api');
    (api.createEvent as ReturnType<typeof vi.fn>).mockResolvedValue(makeEvent());
    renderCalendar();

    fireEvent.click(await screen.findByRole('button', { name: /new event/i }));
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Planning' } });
    fireEvent.click(screen.getByLabelText(/add video conferencing/i));
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() =>
      expect(api.createEvent).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Planning', addConferencing: true })
      )
    );
  });

  it('shows a Join button in the event detail pane for a joinable event', async () => {
    const now = new Date();
    fixtures.events = [
      makeEvent({
        id: 'evt-meet',
        title: 'Standup',
        conferencing: { provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' },
        start: now.toISOString(),
        end: new Date(now.getTime() + 30 * 60_000).toISOString(),
      }),
    ];
    renderCalendar();

    fireEvent.click(await screen.findByText('Standup'));
    const dialog = await screen.findByRole('dialog');
    // Scoped to the dialog: the same event's card is still rendered
    // underneath it in the week grid, with its own "Join Meet" control.
    expect(within(dialog).getByRole('button', { name: /^join meet$/i })).toBeTruthy();
  });
});
