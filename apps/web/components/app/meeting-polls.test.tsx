import type { AvailabilitySlot, Calendar, MeetingPoll } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MeetingPolls } from '@/components/app/meeting-polls';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const fetchPollsMock = vi.fn();
const createPollApiMock = vi.fn();
const confirmPollApiMock = vi.fn();
const deletePollApiMock = vi.fn();
vi.mock('@/lib/scheduling-data', () => ({
  fetchPolls: (...args: unknown[]) => fetchPollsMock(...args),
  createPollApi: (...args: unknown[]) => createPollApiMock(...args),
  confirmPollApi: (...args: unknown[]) => confirmPollApiMock(...args),
  deletePollApi: (...args: unknown[]) => deletePollApiMock(...args),
}));

const fetchAvailabilityMock = vi.fn();
const fetchCalendarsMock = vi.fn();
vi.mock('@/lib/calendar-data', () => ({
  fetchAvailability: (...args: unknown[]) => fetchAvailabilityMock(...args),
  fetchCalendars: (...args: unknown[]) => fetchCalendarsMock(...args),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

const CALENDAR: Calendar = {
  id: 'cal-1',
  accountId: 'acc1',
  name: 'Work',
  color: '#3b82f6',
  timeZone: 'UTC',
  isPrimary: true,
  isVisible: true,
  canWrite: true,
};

const SLOTS: AvailabilitySlot[] = [
  { start: '2026-08-03T10:00:00.000Z', end: '2026-08-03T10:30:00.000Z' },
  { start: '2026-08-04T14:00:00.000Z', end: '2026-08-04T14:30:00.000Z' },
  { start: '2026-08-05T09:00:00.000Z', end: '2026-08-05T09:30:00.000Z' },
];

function makePoll(overrides: Partial<MeetingPoll> = {}): MeetingPoll {
  return {
    id: 'poll-1',
    token: 'tok-abc123',
    title: 'Team sync',
    description: null,
    calendarId: CALENDAR.id,
    durationMinutes: 30,
    options: [
      { id: 'opt-1', start: SLOTS[0]!.start, end: SLOTS[0]!.end },
      { id: 'opt-2', start: SLOTS[1]!.start, end: SLOTS[1]!.end },
    ],
    status: 'open',
    winnerOptionId: null,
    eventId: null,
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

function renderComponent() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MeetingPolls />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchCalendarsMock.mockResolvedValue([CALENDAR]);
  fetchAvailabilityMock.mockResolvedValue(SLOTS);
});

describe('MeetingPolls', () => {
  it('renders the poll list with status and candidate count', async () => {
    fetchPollsMock.mockResolvedValue([makePoll()]);
    renderComponent();

    await screen.findByText('Team sync');
    expect(screen.getByText('Open')).toBeInTheDocument();
    expect(screen.getByText('2 candidate times')).toBeInTheDocument();
  });

  it('shows an empty state with no polls', async () => {
    fetchPollsMock.mockResolvedValue([]);
    renderComponent();
    await screen.findByText(/No meeting polls yet/);
  });

  it('requires at least 2 candidate slots before creating a poll', async () => {
    fetchPollsMock.mockResolvedValue([]);
    renderComponent();
    await screen.findByText(/No meeting polls yet/);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /New meeting poll/ }));

    await user.type(await screen.findByLabelText('Title'), 'Team sync');
    await user.click(screen.getByRole('combobox', { name: /^Calendar$/ }));
    await user.click(await screen.findByRole('option', { name: 'Work' }));

    const createButton = screen.getByRole('button', { name: /^Create poll$/ });
    expect(createButton).toBeDisabled();

    const slotButtons = await screen.findAllByRole('button', { name: /\d{1,2}:\d{2} (AM|PM)/ });
    expect(slotButtons.length).toBeGreaterThanOrEqual(3);

    // One slot selected — still below the 2-slot minimum.
    await user.click(slotButtons[0]!);
    expect(screen.getByText(/Pick at least 2 candidate times/)).toBeInTheDocument();
    expect(createButton).toBeDisabled();

    // A second slot clears the requirement and enables submission.
    await user.click(slotButtons[1]!);
    expect(createButton).toBeEnabled();

    createPollApiMock.mockResolvedValue(makePoll({ id: 'poll-new' }));
    await user.click(createButton);

    await waitFor(() => expect(createPollApiMock).toHaveBeenCalledOnce());
    const input = createPollApiMock.mock.calls[0]![0];
    expect(input.options).toHaveLength(2);
    expect(toastSuccess).toHaveBeenCalledWith('Meeting poll created');
  });

  it('copies the real public poll URL to the clipboard', async () => {
    fetchPollsMock.mockResolvedValue([makePoll()]);
    renderComponent();
    await screen.findByText('Team sync');

    const user = userEvent.setup();
    // userEvent.setup() installs its own Clipboard stub on navigator.clipboard
    // (for copy/paste support), replacing anything defined beforehand — so the
    // spy must be attached after setup(), not in beforeEach.
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
    await user.click(screen.getByRole('button', { name: /Copy link/ }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(expect.stringContaining('/poll/tok-abc123'));
    });
    expect(toastSuccess).toHaveBeenCalledWith('Poll link copied to clipboard');
  });

  it('confirms a winning option and shows the "Event created" badge', async () => {
    fetchPollsMock.mockResolvedValue([makePoll()]);
    confirmPollApiMock.mockResolvedValue(
      makePoll({ status: 'confirmed', winnerOptionId: 'opt-1', eventId: 'evt-1' })
    );
    renderComponent();
    await screen.findByText('Team sync');

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Confirm winner/ }));

    await screen.findByText('Confirm winning time');
    const dialog = screen.getByRole('dialog');
    const radios = screen.getAllByRole('radio');
    await user.click(radios[1]!);
    await userEvent.click(
      (await within(dialog).findByRole('button', { name: /^Confirm winner$/ }))
    );

    await waitFor(() => expect(confirmPollApiMock).toHaveBeenCalledWith('poll-1', 'opt-2'));
    await screen.findByText('Event created');
    expect(toastSuccess).toHaveBeenCalledWith('Winner confirmed - event created');
  });

  it('deletes a poll', async () => {
    fetchPollsMock.mockResolvedValue([makePoll()]);
    deletePollApiMock.mockResolvedValue(undefined);
    renderComponent();
    await screen.findByText('Team sync');

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Delete Team sync/ }));

    await waitFor(() => expect(deletePollApiMock).toHaveBeenCalledWith('poll-1'));
    expect(toastSuccess).toHaveBeenCalledWith('Poll deleted');
  });
});
