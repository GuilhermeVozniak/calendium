import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TimeGrid } from './time-grid';

const calendarById = new Map();

function renderGrid(overrides: {
  days?: Date[];
  pinnedZones?: string[];
} = {}) {
  const days = overrides.days ?? [new Date(2026, 0, 15)];
  return render(
    <TimeGrid
      days={days}
      events={[]}
      calendarById={calendarById}
      now={new Date(2026, 0, 15, 9, 0)}
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
});
