import type { Calendar, Event } from '@calendium/shared';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { addMinutes, format, startOfDay } from 'date-fns';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { QuickAddBar } from '@/components/app/calendar/quick-add-bar';

const useSelfEmailsMock = vi.fn();
vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => useSelfEmailsMock(),
}));

const CAL_WORK: Calendar = {
  id: 'cal-work',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

// Anchored to *today* (not a hardcoded date) so the fixture stays valid
// without mocking the system clock — suggestFreeSlots only cares about the
// calendar day the busy blocks fall on, not the real time-of-day the test
// happens to run at.
const TODAY_9AM = addMinutes(startOfDay(new Date()), 9 * 60);

function busyEvent(id: string, offsetMinutes: number, durationMinutes: number): Event {
  const start = addMinutes(TODAY_9AM, offsetMinutes);
  const end = addMinutes(start, durationMinutes);
  return {
    id,
    calendarId: 'cal-work',
    title: 'Busy',
    description: null,
    location: null,
    start: start.toISOString(),
    end: end.toISOString(),
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
  };
}

// Four 30-minute busy blocks starting on the hour/half-hour from 9:00, each
// followed by a 30-minute gap — yields four candidate free slots so the
// component's 3-suggestion cap is actually exercised.
const BUSY_EVENTS: Event[] = [
  busyEvent('ev1', 0, 30),
  busyEvent('ev2', 60, 30),
  busyEvent('ev3', 120, 30),
  busyEvent('ev4', 180, 30),
];

function slotLabel(offsetMinutes: number, durationMinutes: number): string {
  const start = addMinutes(TODAY_9AM, offsetMinutes);
  const end = addMinutes(start, durationMinutes);
  return `${format(start, 'h:mm a')} – ${format(end, 'h:mm a')}`;
}

beforeEach(() => {
  useSelfEmailsMock.mockReturnValue(new Set<string>());
});

function renderBar(overrides: { events?: Event[] } = {}) {
  const onCreate = vi.fn();
  const utils = render(
    <QuickAddBar calendars={[CAL_WORK]} events={overrides.events ?? []} onCreate={onCreate} />
  );
  return { ...utils, onCreate };
}

describe('QuickAddBar — live preview chips', () => {
  it('renders a chip for every recognized facet and calls onCreate with the full prefill on Enter', async () => {
    const { onCreate } = renderBar();

    const input = screen.getByLabelText('Quick add event');
    fireEvent.change(input, {
      target: {
        value: 'lunch with ana@acme.com tomorrow 1pm at Cafe every friday alert 10 min before',
      },
    });

    // Location, recurrence, alert, and attendee chips each render with their
    // recognized value.
    expect(await screen.findByText('Cafe')).toBeInTheDocument();
    expect(screen.getByText('Every week on FR')).toBeInTheDocument();
    expect(screen.getByText('10 min before')).toBeInTheDocument();
    expect(screen.getByText('ana@acme.com')).toBeInTheDocument();

    // An explicit time was typed, so no free-slot suggestions are offered.
    expect(screen.queryByText('Suggested times')).not.toBeInTheDocument();

    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onCreate).toHaveBeenCalledTimes(1);
    const defaults = onCreate.mock.calls[0]![0];
    expect(defaults.location).toBe('Cafe');
    expect(defaults.recurrenceRule).toBe('FREQ=WEEKLY;BYDAY=FR');
    expect(defaults.reminderMinutes).toEqual([10]);
    expect(defaults.attendeeEmails).toEqual(['ana@acme.com']);
    // Input clears after submit.
    expect(input).toHaveValue('');
  });

  it('shows nothing when the input is empty', () => {
    renderBar();
    expect(screen.queryByText('Suggested times')).not.toBeInTheDocument();
  });
});

describe('QuickAddBar — heuristic/parser agreement on bare ranges', () => {
  it('renders no free-slot suggestions for a bare time range with no am/pm ("lunch 3-4")', () => {
    renderBar({ events: BUSY_EVENTS });

    const input = screen.getByLabelText('Quick add event');
    fireEvent.change(input, { target: { value: 'lunch 3-4' } });

    // The parser resolved an explicit time range (3pm-4pm via the "early
    // hours mean PM" heuristic), so free-slot suggestions must not appear —
    // even though the old local EXPLICIT_TIME_HINT_RE regex didn't recognize
    // a bare, unmarked range like this.
    expect(screen.queryByText('Suggested times')).not.toBeInTheDocument();
  });
});

describe('QuickAddBar — free-slot suggestions', () => {
  it('renders up to 3 free-slot suggestions derived from a busy fixture when no explicit time was typed', async () => {
    renderBar({ events: BUSY_EVENTS });

    const input = screen.getByLabelText('Quick add event');
    fireEvent.change(input, { target: { value: 'Focus time' } });

    await screen.findByText('Suggested times');
    const buttons = screen.getAllByRole('button', { name: /–/ });
    expect(buttons).toHaveLength(3);
    expect(buttons[0]).toHaveTextContent(slotLabel(30, 30));
    expect(buttons[1]).toHaveTextContent(slotLabel(90, 30));
    expect(buttons[2]).toHaveTextContent(slotLabel(150, 30));
  });

  it('prefills the clicked slot and the currently typed title on suggestion click', async () => {
    const { onCreate } = renderBar({ events: BUSY_EVENTS });

    const input = screen.getByLabelText('Quick add event');
    fireEvent.change(input, { target: { value: 'Focus time' } });
    const suggestedLabel = slotLabel(30, 30);
    await screen.findByText('Suggested times');

    fireEvent.click(screen.getByRole('button', { name: suggestedLabel }));

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    const defaults = onCreate.mock.calls[0]![0];
    expect(defaults.title).toBe('Focus time');
    expect(defaults.allDay).toBe(false);
    expect(new Date(defaults.start)).toEqual(addMinutes(TODAY_9AM, 30));
    expect(new Date(defaults.end)).toEqual(addMinutes(TODAY_9AM, 60));
    expect(input).toHaveValue('');
  });
});
