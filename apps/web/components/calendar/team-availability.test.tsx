import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { addDays, startOfDay } from 'date-fns';
import type { MemberAvailability, Team } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  TeamAvailabilityDialog,
  hourIsBusy,
} from '@/components/calendar/team-availability';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const listTeamsMock = vi.fn();
const teamAvailabilityMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listTeams: (...args: unknown[]) => listTeamsMock(...args),
    teamAvailability: (...args: unknown[]) => teamAvailabilityMock(...args),
  }),
}));

const TEAM: Team = {
  id: 't1',
  name: 'Crew',
  createdBy: 'u1',
  createdAt: '2026-07-01T00:00:00Z',
};

// All busy data is built relative to the machine-local "today" so assertions
// are timezone-independent (the grid itself renders in local time).
const today = startOfDay(new Date());
function localIso(hour: number, minute = 0): string {
  const d = new Date(today);
  d.setHours(hour, minute, 0, 0);
  return d.toISOString();
}

function makeRows(): MemberAvailability[] {
  return [
    // Sharing member, busy 9:00–10:30 local.
    { userId: 'alice', shared: true, busy: [{ start: localIso(9), end: localIso(10, 30) }] },
    // Member who never opted in: no data at all.
    { userId: 'bob', shared: false, busy: [] },
  ];
}

function renderDialog() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <TeamAvailabilityDialog open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listTeamsMock.mockResolvedValue([TEAM]);
  teamAvailabilityMock.mockResolvedValue(makeRows());
});

// ---------------------------------------------------------------------------
// hourIsBusy — pure overlap math on local hour cells
// ---------------------------------------------------------------------------

describe('hourIsBusy', () => {
  const busy = [{ start: localIso(9), end: localIso(10, 30) }];

  it('marks every local hour a block overlaps, and no others', () => {
    expect(hourIsBusy(busy, today, 8)).toBe(false);
    expect(hourIsBusy(busy, today, 9)).toBe(true);
    expect(hourIsBusy(busy, today, 10)).toBe(true); // partial 10:00–10:30 overlap
    expect(hourIsBusy(busy, today, 11)).toBe(false);
  });

  it('treats a block ending exactly on the hour as free for that hour', () => {
    const onTheHour = [{ start: localIso(9), end: localIso(10) }];
    expect(hourIsBusy(onTheHour, today, 10)).toBe(false);
  });

  it('is false for another day entirely', () => {
    expect(hourIsBusy(busy, addDays(today, 1), 9)).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// TeamAvailabilityDialog — overview grid wired to the API
// ---------------------------------------------------------------------------

describe('TeamAvailabilityDialog', () => {
  it('auto-selects the first team and requests one local day of availability', async () => {
    renderDialog();
    await waitFor(() => expect(teamAvailabilityMock).toHaveBeenCalled());
    const [teamId, from, to] = teamAvailabilityMock.mock.calls[0] as [string, string, string];
    expect(teamId).toBe('t1');
    expect(from).toBe(today.toISOString());
    expect(to).toBe(addDays(today, 1).toISOString());
  });

  it('renders sharing members as opaque "Busy" blocks only — never details', async () => {
    renderDialog();
    await waitFor(() => expect(screen.getByText('alice')).toBeInTheDocument());

    // 9:00–10:30 busy → exactly the 9 and 10 o'clock cells, both opaque.
    const busyCells = screen.getAllByLabelText('Busy');
    expect(busyCells).toHaveLength(2);
    for (const cell of busyCells) {
      expect(cell).toHaveAttribute('title', 'Busy');
      expect(cell.textContent).toBe(''); // nothing beyond the opaque block
    }
  });

  it('labels rows with the server-resolved name, id fallback otherwise (F2)', async () => {
    teamAvailabilityMock.mockResolvedValue([
      { userId: 'alice', name: 'Alice Adams', email: 'alice@x.com', shared: true, busy: [] },
      { userId: 'bob', email: 'bob@x.com', shared: false, busy: [] },
      { userId: 'carol', shared: false, busy: [] },
    ] satisfies MemberAvailability[]);
    renderDialog();

    await waitFor(() => expect(screen.getByText('Alice Adams')).toBeInTheDocument());
    expect(screen.getByText('bob@x.com')).toBeInTheDocument(); // email fallback
    expect(screen.getByText('carol')).toBeInTheDocument(); // honest id fallback
    expect(screen.queryByText('alice')).not.toBeInTheDocument();
  });

  it('shows non-sharing members as "Not sharing" with zero busy cells', async () => {
    teamAvailabilityMock.mockResolvedValue([
      { userId: 'bob', shared: false, busy: [] },
    ] satisfies MemberAvailability[]);
    renderDialog();

    await waitFor(() => expect(screen.getByText('bob')).toBeInTheDocument());
    expect(screen.getByText('Not sharing')).toBeInTheDocument();
    expect(screen.queryByLabelText('Busy')).not.toBeInTheDocument();
  });

  it('tells the user when they have no teams instead of fetching availability', async () => {
    listTeamsMock.mockResolvedValue([]);
    renderDialog();

    await waitFor(() =>
      expect(screen.getByText('You are not a member of any team yet.')).toBeInTheDocument()
    );
    expect(teamAvailabilityMock).not.toHaveBeenCalled();
  });
});
