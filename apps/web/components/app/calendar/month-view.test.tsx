import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { viewRange } from '@/lib/calendar-views';

import { MonthView } from './month-view';

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

function renderMonth(overrides: {
  anchor?: Date;
  events?: Event[];
} = {}) {
  const anchor = overrides.anchor ?? new Date(2026, 0, 15);
  const { days } = viewRange('month', anchor);
  const onDayClick = vi.fn();
  const onEventClick = vi.fn();
  const utils = render(
    <MonthView
      anchor={anchor}
      days={days}
      events={overrides.events ?? []}
      calendarById={calendarById}
      onDayClick={onDayClick}
      onEventClick={onEventClick}
    />
  );
  return { ...utils, anchor, days, onDayClick, onEventClick };
}

describe('MonthView', () => {
  it('renders 35 cells for a 5-week month (January 2026)', () => {
    renderMonth({ anchor: new Date(2026, 0, 15) });
    expect(screen.getAllByRole('gridcell')).toHaveLength(35);
  });

  it('renders 42 cells for a 6-week month (May 2026)', () => {
    renderMonth({ anchor: new Date(2026, 4, 15) });
    expect(screen.getAllByRole('gridcell')).toHaveLength(42);
  });

  it('dims cells outside the anchor month', () => {
    const { days } = renderMonth({ anchor: new Date(2026, 0, 15) });
    const cells = screen.getAllByRole('gridcell');
    // January 2026's grid starts with trailing December days.
    expect(days[0].getMonth()).toBe(11);
    expect(cells[0]).toHaveAttribute('data-outside-month', 'true');

    // Find the cell for Jan 15 itself — it must not be dimmed.
    const janIndex = days.findIndex((d) => d.getMonth() === 0 && d.getDate() === 15);
    expect(cells[janIndex]).toHaveAttribute('data-outside-month', 'false');
  });

  it('rings the today cell', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 14, 9, 0, 0));
    try {
      const { days } = renderMonth({ anchor: new Date(2026, 0, 15) });
      const cells = screen.getAllByRole('gridcell');
      const todayIndex = days.findIndex((d) => d.getMonth() === 0 && d.getDate() === 14);
      expect(cells[todayIndex]).toHaveAttribute('data-today', 'true');
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows "+N more" beyond 3 events and opens the rest in a popover', async () => {
    const user = userEvent.setup();
    const events = Array.from({ length: 5 }, (_, i) =>
      makeEvent({
        id: `evt-${i}`,
        title: `Meeting ${i}`,
        start: new Date(2026, 0, 14, i, 0, 0).toISOString(),
        end: new Date(2026, 0, 14, i, 30, 0).toISOString(),
      })
    );
    renderMonth({ events });

    expect(screen.getByText('+2 more')).toBeInTheDocument();
    // Only the first 3 (by start time) render as pills directly in the cell.
    expect(screen.getByText('Meeting 0')).toBeInTheDocument();
    expect(screen.getByText('Meeting 2')).toBeInTheDocument();
    expect(screen.queryByText('Meeting 3')).not.toBeInTheDocument();

    await user.click(screen.getByText('+2 more'));
    // Popover should show only the overflow (3 and 4), NOT the visible pills (0, 1, 2)
    const popoverContent = screen.getByText('Meeting 3').closest('[role="dialog"]');
    expect(within(popoverContent as HTMLElement).getByText('Meeting 3')).toBeInTheDocument();
    expect(within(popoverContent as HTMLElement).getByText('Meeting 4')).toBeInTheDocument();
    expect(within(popoverContent as HTMLElement).queryByText('Meeting 0')).not.toBeInTheDocument();
    expect(within(popoverContent as HTMLElement).queryByText('Meeting 2')).not.toBeInTheDocument();
  });

  it('shows a multi-day event pill on each day it touches', () => {
    const events = [
      makeEvent({
        id: 'evt-multi',
        title: 'Conference',
        allDay: true,
        start: new Date(2026, 0, 12, 0, 0, 0).toISOString(),
        end: new Date(2026, 0, 15, 0, 0, 0).toISOString(), // touches 12, 13, 14
      }),
    ];
    renderMonth({ events });
    expect(screen.getAllByText('Conference')).toHaveLength(3);
  });

  it('fires onDayClick when a cell is clicked', async () => {
    const user = userEvent.setup();
    const { onDayClick, days } = renderMonth();
    const cells = screen.getAllByRole('gridcell');
    const janIndex = days.findIndex((d) => d.getMonth() === 0 && d.getDate() === 15);
    await user.click(cells[janIndex]);
    expect(onDayClick).toHaveBeenCalledTimes(1);
    expect(onDayClick.mock.calls[0][0].getDate()).toBe(15);
  });

  it('fires onEventClick (not onDayClick) when a pill is clicked', async () => {
    const user = userEvent.setup();
    const event = makeEvent({ title: 'Standup' });
    const { onDayClick, onEventClick } = renderMonth({ events: [event] });
    await user.click(screen.getByText('Standup'));
    expect(onEventClick).toHaveBeenCalledWith(event);
    expect(onDayClick).not.toHaveBeenCalled();
  });
});
