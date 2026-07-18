import { addDays, format } from 'date-fns';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { DayTicker } from './day-ticker';

const CAL: CalendarModel = {
  id: 'cal-1',
  accountId: 'acc',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};
const calendarById = new Map([[CAL.id, CAL]]);

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: CAL.id,
    title: 'Event',
    description: null,
    location: null,
    start: new Date(2026, 0, 14, 10, 0, 0).toISOString(),
    end: new Date(2026, 0, 14, 11, 0, 0).toISOString(),
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

function renderTicker(overrides: {
  anchor?: Date;
  events?: Event[];
} = {}) {
  const anchor = overrides.anchor ?? new Date(2026, 0, 15);
  const onAnchorChange = vi.fn();
  const onEventClick = vi.fn();
  const utils = render(
    <DayTicker
      anchor={anchor}
      events={overrides.events ?? []}
      calendarById={calendarById}
      onAnchorChange={onAnchorChange}
      onEventClick={onEventClick}
    />
  );
  return { ...utils, anchor, onAnchorChange, onEventClick };
}

describe('DayTicker', () => {
  it('renders 14 dated strip cells with weekday initials', () => {
    const anchor = new Date(2026, 0, 15);
    renderTicker({ anchor });
    const cells = screen.getAllByTestId(/^ticker-day-/);
    expect(cells).toHaveLength(14);
    // Jan 15, 2026 is a Thursday -> initial "T".
    const anchorCell = screen.getByTestId(`ticker-day-${format(anchor, 'yyyy-MM-dd')}`);
    expect(within(anchorCell).getByText('T')).toBeInTheDocument();
    expect(within(anchorCell).getByText('15')).toBeInTheDocument();
  });

  it('calls onAnchorChange when a strip day is clicked', async () => {
    const user = userEvent.setup();
    const anchor = new Date(2026, 0, 15);
    const { onAnchorChange } = renderTicker({ anchor });
    const target = addDays(anchor, 2);
    await user.click(screen.getByTestId(`ticker-day-${format(target, 'yyyy-MM-dd')}`));
    expect(onAnchorChange).toHaveBeenCalledTimes(1);
    const called: Date = onAnchorChange.mock.calls[0][0];
    expect(format(called, 'yyyy-MM-dd')).toBe(format(target, 'yyyy-MM-dd'));
  });

  it('groups events by day and hides empty days', () => {
    const anchor = new Date(2026, 0, 15);
    const events = [
      makeEvent({
        id: 'evt-a',
        title: 'Standup',
        start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
        end: new Date(2026, 0, 15, 9, 15, 0).toISOString(),
      }),
      makeEvent({
        id: 'evt-b',
        title: 'Dentist',
        start: new Date(2026, 0, 18, 11, 0, 0).toISOString(),
        end: new Date(2026, 0, 18, 12, 0, 0).toISOString(),
      }),
    ];
    renderTicker({ anchor, events });

    expect(screen.getByText('Standup')).toBeInTheDocument();
    expect(screen.getByText('Dentist')).toBeInTheDocument();
    // Only 2 populated day-group headings should render, not one per fetched day.
    expect(screen.getAllByRole('heading', { level: 2 })).toHaveLength(2);
  });

  it('shows the empty state when there are no events in the window', () => {
    renderTicker({ events: [] });
    expect(screen.getByText(/No events in the next/)).toBeInTheDocument();
  });

  describe('today badge', () => {
    beforeEach(() => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date(2026, 0, 14, 9, 0, 0));
    });
    afterEach(() => vi.useRealTimers());

    it('marks the strip cell for today', () => {
      const anchor = new Date(2026, 0, 15);
      renderTicker({ anchor });
      const todayCell = screen.getByTestId(`ticker-day-${format(new Date(2026, 0, 14), 'yyyy-MM-dd')}`);
      expect(within(todayCell).getByText('Today')).toBeInTheDocument();
      const selectedCell = screen.getByTestId(`ticker-day-${format(anchor, 'yyyy-MM-dd')}`);
      expect(within(selectedCell).queryByText('Today')).not.toBeInTheDocument();
    });
  });

  it('auto-scrolls the list toward the selected day on mount', () => {
    const scrollSpy = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scrollSpy;
    try {
      const anchor = new Date(2026, 0, 15);
      const events = [
        makeEvent({
          id: 'evt-a',
          start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
          end: new Date(2026, 0, 15, 9, 15, 0).toISOString(),
        }),
      ];
      renderTicker({ anchor, events });
      expect(scrollSpy).toHaveBeenCalled();
    } finally {
      Element.prototype.scrollIntoView = original;
    }
  });

  it('fires onEventClick when a list item is clicked', async () => {
    const user = userEvent.setup();
    const anchor = new Date(2026, 0, 15);
    const event = makeEvent({
      title: 'Standup',
      start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
      end: new Date(2026, 0, 15, 9, 15, 0).toISOString(),
    });
    const { onEventClick } = renderTicker({ anchor, events: [event] });
    await user.click(screen.getByText('Standup'));
    expect(onEventClick).toHaveBeenCalledWith(event);
  });

  it('re-scrolls when events arrive after initial empty render', () => {
    const scrollSpy = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scrollSpy;
    try {
      const anchor = new Date(2026, 0, 15);
      const calendarById = new Map([[CAL.id, CAL]]);
      const onAnchorChange = vi.fn();
      const onEventClick = vi.fn();
      const { rerender } = render(
        <DayTicker
          anchor={anchor}
          events={[]}
          calendarById={calendarById}
          onAnchorChange={onAnchorChange}
          onEventClick={onEventClick}
        />
      );
      expect(scrollSpy).not.toHaveBeenCalled();
      scrollSpy.mockClear();

      const events = [
        makeEvent({
          id: 'evt-a',
          start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
          end: new Date(2026, 0, 15, 9, 15, 0).toISOString(),
        }),
      ];
      rerender(
        <DayTicker
          anchor={anchor}
          events={events}
          calendarById={calendarById}
          onAnchorChange={onAnchorChange}
          onEventClick={onEventClick}
        />
      );
      expect(scrollSpy).toHaveBeenCalled();
    } finally {
      Element.prototype.scrollIntoView = original;
    }
  });
});
