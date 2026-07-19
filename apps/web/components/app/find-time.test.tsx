import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { addDays, startOfDay } from 'date-fns';
import type { AvailabilitySlot, MemberAvailability, Team } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { FindTimeDialog, buildFindTimeText, slotBlockers } from '@/components/app/find-time';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchAvailabilityMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchAvailability: (...args: unknown[]) => fetchAvailabilityMock(...args),
}));

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

// Machine-local tomorrow keeps the slot chips' rendered times deterministic
// regardless of the runner's timezone.
const tomorrow = startOfDay(addDays(new Date(), 1));
function localIso(hour: number, minute = 0): string {
  const d = new Date(tomorrow);
  d.setHours(hour, minute, 0, 0);
  return d.toISOString();
}

const SLOT_9: AvailabilitySlot = { start: localIso(9), end: localIso(9, 30) };
const SLOT_10: AvailabilitySlot = { start: localIso(10), end: localIso(10, 30) };

// alice shares and is busy over the 9:00 slot; bob never opted in.
function makeMembers(): MemberAvailability[] {
  return [
    { userId: 'alice', shared: true, busy: [{ start: localIso(9), end: localIso(9, 30) }] },
    { userId: 'bob', shared: false, busy: [] },
  ];
}

function renderDialog(onInsert = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <FindTimeDialog open onOpenChange={vi.fn()} onInsert={onInsert} />
    </QueryClientProvider>
  );
  return onInsert;
}

async function pickTeam(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByLabelText('Team'));
  await user.click(await screen.findByText('Crew'));
  await waitFor(() => expect(teamAvailabilityMock).toHaveBeenCalled());
}

const slotButton = (name: RegExp) => screen.getByRole('button', { name });

beforeEach(() => {
  vi.clearAllMocks();
  fetchAvailabilityMock.mockResolvedValue([SLOT_9, SLOT_10]);
  listTeamsMock.mockResolvedValue([TEAM]);
  teamAvailabilityMock.mockResolvedValue(makeMembers());
});

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

describe('slotBlockers', () => {
  it('reports only selected, sharing members whose busy overlaps the slot', () => {
    const members = makeMembers();
    expect(slotBlockers(SLOT_9, members, new Set(['alice']))).toEqual(['alice']);
    expect(slotBlockers(SLOT_10, members, new Set(['alice']))).toEqual([]);
    // Deselected member no longer blocks.
    expect(slotBlockers(SLOT_9, members, new Set())).toEqual([]);
  });

  it('never counts a non-sharing member, even when selected', () => {
    const members: MemberAvailability[] = [
      // A shared=false row can never carry busy data from the server; even a
      // hostile/buggy payload must not block (server is authoritative).
      { userId: 'bob', shared: false, busy: [{ start: localIso(9), end: localIso(9, 30) }] },
    ];
    expect(slotBlockers(SLOT_9, members, new Set(['bob']))).toEqual([]);
  });

  it('treats touching intervals as non-overlapping', () => {
    const members: MemberAvailability[] = [
      { userId: 'alice', shared: true, busy: [{ start: localIso(8, 30), end: localIso(9) }] },
    ];
    expect(slotBlockers(SLOT_9, members, new Set(['alice']))).toEqual([]);
  });
});

describe('buildFindTimeText', () => {
  it('marks only conflicted slots and groups by day', () => {
    const text = buildFindTimeText([
      { slot: SLOT_9, conflicted: true },
      { slot: SLOT_10, conflicted: false },
    ]);
    expect(text).toContain('How about one of these times?');
    expect(text).toContain('9:00 AM – 9:30 AM (may conflict for a teammate)');
    expect(text).toContain('10:00 AM – 10:30 AM');
    expect(text).not.toContain('10:30 AM (may conflict');
  });
});

// ---------------------------------------------------------------------------
// FindTimeDialog — overlay + override behavior
// ---------------------------------------------------------------------------

describe('FindTimeDialog', () => {
  it('inserts selected free slots without markers when no team is overlaid', async () => {
    const user = userEvent.setup();
    const onInsert = renderDialog();

    await user.click(await screen.findByRole('button', { name: /^10:00/ }));
    await user.click(screen.getByRole('button', { name: 'Insert times' }));

    expect(onInsert).toHaveBeenCalledTimes(1);
    const text = onInsert.mock.calls[0][0] as string;
    expect(text).toContain('10:00 AM – 10:30 AM');
    expect(text).not.toContain('may conflict');
  });

  it('disables a slot when any selected member is busy over it', async () => {
    const user = userEvent.setup();
    renderDialog();
    await screen.findByRole('button', { name: /^10:00/ });
    await pickTeam(user);

    await waitFor(() => expect(slotButton(/^9:00/)).toBeDisabled());
    expect(slotButton(/^9:00/)).toHaveAccessibleName(/Busy/);
    expect(slotButton(/^10:00/)).toBeEnabled();
    // The non-sharing member is visible as such, with no busy influence.
    expect(screen.getByText('bob (not sharing)')).toBeInTheDocument();
  });

  it('re-enables the slot when the busy member is deselected', async () => {
    const user = userEvent.setup();
    renderDialog();
    await screen.findByRole('button', { name: /^10:00/ });
    await pickTeam(user);
    await waitFor(() => expect(slotButton(/^9:00/)).toBeDisabled());

    await user.click(screen.getByLabelText('Include alice'));
    await waitFor(() => expect(slotButton(/^9:00/)).toBeEnabled());
  });

  it('override path: a busy slot becomes insertable and its text is marked', async () => {
    const user = userEvent.setup();
    const onInsert = renderDialog();
    await screen.findByRole('button', { name: /^10:00/ });
    await pickTeam(user);
    await waitFor(() => expect(slotButton(/^9:00/)).toBeDisabled());

    await user.click(screen.getByLabelText('Allow conflicting slots'));
    await waitFor(() => expect(slotButton(/^9:00/)).toBeEnabled());

    await user.click(slotButton(/^9:00/));
    await user.click(screen.getByRole('button', { name: 'Insert times' }));

    const text = onInsert.mock.calls[0][0] as string;
    expect(text).toContain('9:00 AM – 9:30 AM (may conflict for a teammate)');
  });

  it('cannot insert with nothing selected', async () => {
    renderDialog();
    await screen.findByRole('button', { name: /^10:00/ });
    expect(screen.getByRole('button', { name: 'Insert times' })).toBeDisabled();
  });
});
