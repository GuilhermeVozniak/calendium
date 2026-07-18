import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Event } from '@calendium/shared';

import { TimeGrid } from './time-grid';

const calendarById = new Map();

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    title: 'Standup',
    description: null,
    location: null,
    start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
    end: new Date(2026, 0, 15, 9, 30, 0).toISOString(),
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

function renderGrid(overrides: {
  days?: Date[];
  pinnedZones?: string[];
  events?: Event[];
  now?: Date;
} = {}) {
  const days = overrides.days ?? [new Date(2026, 0, 15)];
  return render(
    <TimeGrid
      days={days}
      events={overrides.events ?? []}
      calendarById={calendarById}
      now={overrides.now ?? new Date(2026, 0, 15, 9, 0)}
      gmtLabel="GMT+0"
      pinnedZones={overrides.pinnedZones ?? []}
      onSlotClick={vi.fn()}
      onEventClick={vi.fn()}
    />
  );
}

describe('TimeGrid — multi-timezone gutters', () => {
  let originalTz: string | undefined;

  beforeEach(() => {
    originalTz = process.env.TZ;
    process.env.TZ = 'UTC';
  });

  afterEach(() => {
    process.env.TZ = originalTz;
  });

  it('renders no extra gutters when no zones are pinned', () => {
    renderGrid();
    expect(screen.queryByTestId(/^tz-gutter-/)).not.toBeInTheDocument();
  });

  it('renders one extra gutter per pinned zone, left of the primary gutter', () => {
    renderGrid({ pinnedZones: ['Asia/Kolkata', 'America/New_York'] });
    const kolkata = screen.getByTestId('tz-gutter-Asia/Kolkata');
    const newYork = screen.getByTestId('tz-gutter-America/New_York');
    const primary = screen.getByTestId('time-gutter-primary');

    // Every pinned-zone gutter must appear before (i.e. left of, in DOM/flex
    // order) the primary hour gutter.
    for (const gutter of [kolkata, newYork]) {
      expect(gutter.compareDocumentPosition(primary) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
  });

  it('computes the pinned-zone gutter label from the reference day, DST-aware', () => {
    // Day is Jan 15 2026 UTC; Asia/Kolkata is a fixed +5:30 (no DST), so the
    // 12:00 (noon) UTC row must read 5:30 PM in the Kolkata gutter.
    renderGrid({ pinnedZones: ['Asia/Kolkata'] });
    const gutter = screen.getByTestId('tz-gutter-Asia/Kolkata');
    expect(within(gutter).getByText('5:30 PM')).toBeInTheDocument();
  });

  it('marks a pinned-zone label with a day-shift indicator when it lands on the next day', () => {
    // Los Angeles evening (hour 22) lands on the *next* day in Kolkata
    // (UTC+5:30): Jan 15 22:00 PST = Jan 16 11:30 Kolkata.
    process.env.TZ = 'America/Los_Angeles';
    renderGrid({ days: [new Date(2026, 0, 15)], pinnedZones: ['Asia/Kolkata'] });
    const gutter = screen.getByTestId('tz-gutter-Asia/Kolkata');
    expect(within(gutter).getByText('11:30 AM')).toBeInTheDocument();
    expect(within(gutter).getByTestId('tz-dayshift-22')).toHaveTextContent('+1');
  });

  it('renders a caption header (city + GMT offset) above each pinned-zone gutter', () => {
    renderGrid({ pinnedZones: ['Asia/Kolkata'] });
    const header = screen.getByTestId('tz-header-Asia/Kolkata');
    expect(within(header).getByText('Kolkata')).toBeInTheDocument();
    expect(within(header).getByText(/GMT\+5:30/)).toBeInTheDocument();
  });

  it('does not flag a pinned zone whose gutter labels are uniform across the displayed week', () => {
    const days = [
      new Date(2026, 0, 12),
      new Date(2026, 0, 13),
      new Date(2026, 0, 14),
      new Date(2026, 0, 15),
      new Date(2026, 0, 16),
    ];
    renderGrid({ days, pinnedZones: ['Asia/Kolkata'] });
    expect(screen.queryByTestId('tz-dst-marker-Asia/Kolkata')).not.toBeInTheDocument();
  });

  it('flags a pinned zone whose gutter labels are non-uniform across a DST-transition week', () => {
    // America/New_York falls back from EDT to EST at 2:00 AM local on
    // 2026-11-01. With the primary/system zone at UTC, the noon-UTC row
    // reads 8 AM (EDT, UTC-4) on the days before the transition and 7 AM
    // (EST, UTC-5) on the days at/after it — the shared reference-day
    // gutter can't express both, so the header caption must flag it.
    const days = [
      new Date(2026, 9, 29),
      new Date(2026, 9, 30),
      new Date(2026, 9, 31),
      new Date(2026, 10, 1),
      new Date(2026, 10, 2),
    ];
    renderGrid({ days, pinnedZones: ['America/New_York'] });
    expect(screen.getByTestId('tz-dst-marker-America/New_York')).toBeInTheDocument();
  });
});

describe('TimeGrid — conference join affordance', () => {
  it('shows a Join button on a tall event block (>= 2 rows) with a detected conference link', () => {
    const event = makeEvent({
      location: 'https://meet.google.com/abc-defg-hij',
      start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
      end: new Date(2026, 0, 15, 10, 0, 0).toISOString(), // 60min -> height 48px >= 40
    });
    renderGrid({ events: [event], now: new Date(2026, 0, 15, 9, 10) });
    expect(screen.getByRole('button', { name: /join meet/i })).toBeInTheDocument();
  });

  it('does not show a Join button on a short event block (< 2 rows)', () => {
    const event = makeEvent({
      location: 'https://meet.google.com/abc-defg-hij',
      start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
      end: new Date(2026, 0, 15, 9, 30, 0).toISOString(), // 30min -> height 24px < 40
    });
    renderGrid({ events: [event], now: new Date(2026, 0, 15, 9, 10) });
    expect(screen.queryByRole('button', { name: /join meet/i })).not.toBeInTheDocument();
  });

  it('shows no Join button on a tall event block without a detected conference link', () => {
    const event = makeEvent({
      start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
      end: new Date(2026, 0, 15, 10, 0, 0).toISOString(),
    });
    renderGrid({ events: [event], now: new Date(2026, 0, 15, 9, 10) });
    expect(screen.queryByRole('button', { name: /join/i })).not.toBeInTheDocument();
  });

  it('clicking the Join button does not also open the event dialog', async () => {
    const onEventClick = vi.fn();
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
    const event = makeEvent({
      location: 'https://meet.google.com/abc-defg-hij',
      start: new Date(2026, 0, 15, 9, 0, 0).toISOString(),
      end: new Date(2026, 0, 15, 10, 0, 0).toISOString(),
    });
    render(
      <TimeGrid
        days={[new Date(2026, 0, 15)]}
        events={[event]}
        calendarById={calendarById}
        now={new Date(2026, 0, 15, 9, 10)}
        gmtLabel="GMT+0"
        onSlotClick={vi.fn()}
        onEventClick={onEventClick}
      />
    );
    screen.getByRole('button', { name: /join meet/i }).click();
    expect(openSpy).toHaveBeenCalled();
    expect(onEventClick).not.toHaveBeenCalled();
    openSpy.mockRestore();
  });
});
