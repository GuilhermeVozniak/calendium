import type { Calendar, ConnectedAccount, Event } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  ConflictWarning,
  type ConflictWarningProps,
} from '@/components/app/calendar/conflict-warning';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const useSelfEmailsMock = vi.fn();
vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => useSelfEmailsMock(),
}));

const fetchBusyEventsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchBusyEvents: (...args: unknown[]) => fetchBusyEventsMock(...args),
}));

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const CAL_WORK: Calendar = {
  id: 'cal-work',
  accountId: 'acc-work',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

// The blocking calendar is on a *different, hidden* account/calendar than the
// one the event being edited lives on — this is the cross-account case.
const CAL_HIDDEN_PERSONAL: Calendar = {
  id: 'cal-personal-hidden',
  accountId: 'acc-personal',
  name: 'Personal',
  color: '#22c55e',
  timeZone: 'UTC',
  isPrimary: false,
  isVisible: false,
  canWrite: true,
};

const ACCOUNTS: ConnectedAccount[] = [
  {
    id: 'acc-work',
    provider: 'google',
    email: 'work@acme.com',
    status: 'active',
    scopes: [],
    vipSenders: [],
    signatureHtml: '',
    autoBcc: [],
    lastSyncedAt: null,
    createdAt: '2026-01-01T00:00:00.000Z',
  },
  {
    id: 'acc-personal',
    provider: 'google',
    email: 'me@gmail.com',
    status: 'active',
    scopes: [],
    vipSenders: [],
    signatureHtml: '',
    autoBcc: [],
    lastSyncedAt: null,
    createdAt: '2026-01-01T00:00:00.000Z',
  },
];

function standupEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-standup',
    calendarId: 'cal-personal-hidden',
    title: 'Standup',
    description: null,
    location: null,
    start: '2026-07-20T09:30:00.000Z',
    end: '2026-07-20T09:45:00.000Z',
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

function renderWarning(props: Partial<ConflictWarningProps> = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ConflictWarning
        calendars={[CAL_WORK, CAL_HIDDEN_PERSONAL]}
        start={new Date('2026-07-20T09:30:00.000Z')}
        end={new Date('2026-07-20T10:00:00.000Z')}
        allDay={false}
        {...props}
      />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  useSelfEmailsMock.mockReturnValue(new Set<string>());
  fetchAccountsMock.mockResolvedValue(ACCOUNTS);
  fetchBusyEventsMock.mockResolvedValue([]);
});

describe('ConflictWarning', () => {
  it('renders nothing while there is no overlapping busy event', async () => {
    fetchBusyEventsMock.mockResolvedValue([]);
    renderWarning();

    await waitFor(() => expect(fetchBusyEventsMock).toHaveBeenCalled());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('warns with the conflicting event title, time, and its account email when a hidden other-account calendar overlaps', async () => {
    fetchBusyEventsMock.mockResolvedValue([standupEvent()]);
    renderWarning();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Standup');
    expect(alert).toHaveTextContent('me@gmail.com');
  });

  it('clears the warning once the candidate time no longer overlaps', async () => {
    fetchBusyEventsMock.mockResolvedValue([standupEvent()]);
    const { rerender } = renderWarning();
    await screen.findByRole('alert');

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    rerender(
      <QueryClientProvider client={queryClient}>
        <ConflictWarning
          calendars={[CAL_WORK, CAL_HIDDEN_PERSONAL]}
          start={new Date('2026-07-20T11:00:00.000Z')}
          end={new Date('2026-07-20T11:30:00.000Z')}
          allDay={false}
        />
      </QueryClientProvider>
    );

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });

  it('never conflicts with itself when ignoreEventId matches the busy event', async () => {
    fetchBusyEventsMock.mockResolvedValue([standupEvent({ id: 'evt-being-edited' })]);
    renderWarning({ ignoreEventId: 'evt-being-edited' });

    await waitFor(() => expect(fetchBusyEventsMock).toHaveBeenCalled());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('renders nothing for all-day candidates', async () => {
    fetchBusyEventsMock.mockResolvedValue([standupEvent()]);
    renderWarning({ allDay: true });

    expect(fetchBusyEventsMock).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('renders nothing while start/end are not yet known', () => {
    renderWarning({ start: null, end: null });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('caps the list at 3 conflicts and notes how many more there are', async () => {
    fetchBusyEventsMock.mockResolvedValue([
      standupEvent({ id: 'evt-1', title: 'One' }),
      standupEvent({ id: 'evt-2', title: 'Two' }),
      standupEvent({ id: 'evt-3', title: 'Three' }),
      standupEvent({ id: 'evt-4', title: 'Four' }),
    ]);
    renderWarning();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('One');
    expect(alert).toHaveTextContent('Two');
    expect(alert).toHaveTextContent('Three');
    expect(alert).not.toHaveTextContent('Four');
    expect(alert).toHaveTextContent('+1 more');
  });
});
