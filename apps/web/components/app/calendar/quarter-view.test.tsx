import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Event } from '@calendium/shared';

import { QuarterView } from './quarter-view';

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    title: 'Event',
    description: null,
    location: null,
    start: new Date(2026, 6, 14, 10, 0, 0).toISOString(),
    end: new Date(2026, 6, 14, 11, 0, 0).toISOString(),
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

describe('QuarterView', () => {
  it('renders exactly the 3 months of the anchor quarter (Q3 2026: Jul, Aug, Sep)', () => {
    render(<QuarterView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={vi.fn()} />);
    expect(screen.getByTestId('quarter-month-2026-07')).toBeInTheDocument();
    expect(screen.getByTestId('quarter-month-2026-08')).toBeInTheDocument();
    expect(screen.getByTestId('quarter-month-2026-09')).toBeInTheDocument();
    expect(screen.queryByTestId('quarter-month-2026-06')).not.toBeInTheDocument();
    expect(screen.queryByTestId('quarter-month-2026-10')).not.toBeInTheDocument();
  });

  it('computes a density map: a multi-day event counts on every day it touches', () => {
    const events = [
      makeEvent({
        id: 'evt-multi',
        allDay: true,
        start: new Date(2026, 6, 14, 0, 0, 0).toISOString(),
        end: new Date(2026, 6, 17, 0, 0, 0).toISOString(), // touches 14, 15, 16 (end exclusive)
      }),
    ];
    render(<QuarterView anchor={new Date(2026, 6, 17)} events={events} onDayClick={vi.fn()} />);
    expect(screen.getByTestId('mini-dots-2026-07-14').children).toHaveLength(1);
    expect(screen.getByTestId('mini-dots-2026-07-15').children).toHaveLength(1);
    expect(screen.getByTestId('mini-dots-2026-07-16').children).toHaveLength(1);
    expect(screen.getByTestId('mini-dots-2026-07-17').children).toHaveLength(0);
  });

  it('caps intensity dots at 3 even with more overlapping events', () => {
    const events = Array.from({ length: 5 }, (_, i) =>
      makeEvent({
        id: `evt-${i}`,
        start: new Date(2026, 6, 14, i, 0, 0).toISOString(),
        end: new Date(2026, 6, 14, i + 1, 0, 0).toISOString(),
      })
    );
    render(<QuarterView anchor={new Date(2026, 6, 17)} events={events} onDayClick={vi.fn()} />);
    expect(screen.getByTestId('mini-dots-2026-07-14').children).toHaveLength(3);
  });

  it('fires onDayClick with the clicked day', async () => {
    const user = userEvent.setup();
    const onDayClick = vi.fn();
    render(<QuarterView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={onDayClick} />);
    await user.click(screen.getByTestId('mini-day-2026-07-15'));
    expect(onDayClick).toHaveBeenCalledTimes(1);
    expect(onDayClick.mock.calls[0][0].getFullYear()).toBe(2026);
    expect(onDayClick.mock.calls[0][0].getMonth()).toBe(6);
    expect(onDayClick.mock.calls[0][0].getDate()).toBe(15);
  });

  it('highlights the real-world current month', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 7, 3, 9, 0, 0)); // Aug 3, 2026 -> within Q3
    try {
      render(<QuarterView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={vi.fn()} />);
      expect(screen.getByTestId('quarter-month-2026-08')).toHaveAttribute('data-current-month', 'true');
      expect(screen.getByTestId('quarter-month-2026-07')).toHaveAttribute('data-current-month', 'false');
      expect(screen.getByTestId('quarter-month-2026-09')).toHaveAttribute('data-current-month', 'false');
    } finally {
      vi.useRealTimers();
    }
  });
});
