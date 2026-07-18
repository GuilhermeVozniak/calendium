import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Event } from '@calendium/shared';

import { YearView } from './year-view';

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    title: 'Event',
    description: null,
    location: null,
    start: new Date(2026, 0, 5, 9, 0, 0).toISOString(),
    end: new Date(2026, 0, 5, 10, 0, 0).toISOString(),
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

describe('YearView', () => {
  it('renders exactly the 12 months of the anchor year (2026)', () => {
    render(<YearView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={vi.fn()} />);
    for (let m = 1; m <= 12; m++) {
      expect(screen.getByTestId(`year-month-2026-${String(m).padStart(2, '0')}`)).toBeInTheDocument();
    }
    expect(screen.queryByTestId('year-month-2025-12')).not.toBeInTheDocument();
    expect(screen.queryByTestId('year-month-2027-01')).not.toBeInTheDocument();
  });

  it('computes a density map across the full year', () => {
    const events = [makeEvent()];
    render(<YearView anchor={new Date(2026, 6, 17)} events={events} onDayClick={vi.fn()} />);
    expect(screen.getByTestId('mini-dots-2026-01-05').children).toHaveLength(1);
    expect(screen.getByTestId('mini-dots-2026-01-06').children).toHaveLength(0);
  });

  it('caps intensity dots at 3 even with more overlapping events', () => {
    const events = Array.from({ length: 4 }, (_, i) =>
      makeEvent({
        id: `evt-${i}`,
        start: new Date(2026, 0, 5, i, 0, 0).toISOString(),
        end: new Date(2026, 0, 5, i + 1, 0, 0).toISOString(),
      })
    );
    render(<YearView anchor={new Date(2026, 6, 17)} events={events} onDayClick={vi.fn()} />);
    expect(screen.getByTestId('mini-dots-2026-01-05').children).toHaveLength(3);
  });

  it('fires onDayClick with the clicked day', async () => {
    const user = userEvent.setup();
    const onDayClick = vi.fn();
    render(<YearView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={onDayClick} />);
    await user.click(screen.getByTestId('mini-day-2026-03-10'));
    expect(onDayClick).toHaveBeenCalledTimes(1);
    expect(onDayClick.mock.calls[0][0].getFullYear()).toBe(2026);
    expect(onDayClick.mock.calls[0][0].getMonth()).toBe(2);
    expect(onDayClick.mock.calls[0][0].getDate()).toBe(10);
  });

  it('highlights the real-world current month', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 10, 20, 9, 0, 0)); // Nov 20, 2026
    try {
      render(<YearView anchor={new Date(2026, 6, 17)} events={[]} onDayClick={vi.fn()} />);
      expect(screen.getByTestId('year-month-2026-11')).toHaveAttribute('data-current-month', 'true');
      expect(screen.getByTestId('year-month-2026-01')).toHaveAttribute('data-current-month', 'false');
    } finally {
      vi.useRealTimers();
    }
  });
});
