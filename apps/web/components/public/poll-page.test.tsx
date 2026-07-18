import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { PublicPoll } from '@calendium/shared';

const fetchPublicPollMock = vi.fn();
const votePublicPollMock = vi.fn();

vi.mock('@calendium/shared', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@calendium/shared')>();
  return {
    ...actual,
    fetchPublicPoll: (...args: unknown[]) => fetchPublicPollMock(...args),
    votePublicPoll: (...args: unknown[]) => votePublicPollMock(...args),
  };
});

import { ApiRequestError } from '@calendium/shared';

import { PublicPollPage } from './poll-page';

const VOTER_KEY = 'calendium.poll.voter';

function openPoll(overrides: Partial<PublicPoll> = {}): PublicPoll {
  return {
    token: 'tok123',
    title: 'Team Sync',
    description: 'Quick weekly check-in',
    organizerName: 'Ada Lovelace',
    durationMinutes: 30,
    status: 'open',
    options: [
      { id: 'opt1', start: '2026-08-03T13:00:00.000Z', end: '2026-08-03T13:30:00.000Z' },
      { id: 'opt2', start: '2026-08-04T15:00:00.000Z', end: '2026-08-04T15:30:00.000Z' },
    ],
    tallies: {
      opt1: { yes: 1, no: 0, ifNeeded: 0 },
      opt2: { yes: 0, no: 1, ifNeeded: 0 },
    },
    winnerOptionId: null,
    ...overrides,
  };
}

function renderPage(token = 'tok123') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <PublicPollPage token={token} />
    </QueryClientProvider>
  );
}

describe('PublicPollPage', () => {
  let originalTz: string | undefined;

  beforeEach(() => {
    fetchPublicPollMock.mockReset();
    votePublicPollMock.mockReset();
    window.localStorage.clear();
    originalTz = process.env.TZ;
    process.env.TZ = 'America/New_York';
    // Keep the timezone <select> small — a real IANA list is 400+ entries,
    // which balloons every failed-test DOM dump into unreadable noise and
    // slows rendering for no test value.
    vi.spyOn(Intl, 'supportedValuesOf').mockReturnValue([
      'America/New_York',
      'America/Los_Angeles',
      'UTC',
    ]);
  });

  afterEach(() => {
    process.env.TZ = originalTz;
    vi.restoreAllMocks();
  });

  it('renders poll options in the visitor timezone', async () => {
    fetchPublicPollMock.mockResolvedValue(openPoll());
    renderPage();

    // 2026-08-03T13:00:00Z is 9:00 AM in America/New_York (EDT, UTC-4).
    expect(await screen.findByText(/9:00\s?AM/i)).toBeInTheDocument();
    // 2026-08-04T15:00:00Z is 11:00 AM in America/New_York.
    expect(await screen.findByText(/11:00\s?AM/i)).toBeInTheDocument();
    expect(screen.getByText(/Times shown in America\/New_York/)).toBeInTheDocument();
  });

  it('recomputes displayed times when the visitor changes timezone', async () => {
    fetchPublicPollMock.mockResolvedValue(openPoll());
    renderPage();

    await screen.findByText(/9:00\s?AM/i);
    const select = screen.getByLabelText(/time zone/i) as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'America/Los_Angeles' } });

    // 2026-08-03T13:00:00Z is 6:00 AM in America/Los_Angeles (PDT, UTC-7).
    expect(await screen.findByText(/6:00\s?AM/i)).toBeInTheDocument();
  });

  it('submits a ballot with voter name/email and per-option choices', async () => {
    const poll = openPoll();
    fetchPublicPollMock.mockResolvedValue(poll);
    votePublicPollMock.mockResolvedValue({
      ...poll,
      tallies: {
        opt1: { yes: 2, no: 0, ifNeeded: 0 },
        opt2: { yes: 0, no: 1, ifNeeded: 1 },
      },
    });
    renderPage();

    await screen.findByText('Team Sync');

    fireEvent.change(screen.getByLabelText(/your name/i), { target: { value: 'Grace Hopper' } });
    fireEvent.change(screen.getByLabelText(/your email/i), { target: { value: 'grace@example.com' } });

    const optionCards = screen.getAllByTestId(/poll-option-/);
    expect(optionCards).toHaveLength(2);

    fireEvent.click(within(optionCards[0]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(within(optionCards[1]!).getByRole('button', { name: 'If needed' }));

    fireEvent.click(screen.getByRole('button', { name: /submit/i }));

    await waitFor(() => expect(votePublicPollMock).toHaveBeenCalledTimes(1));
    const [, token, ballot] = votePublicPollMock.mock.calls[0]!;
    expect(token).toBe('tok123');
    expect(ballot).toEqual({
      voterName: 'Grace Hopper',
      voterEmail: 'grace@example.com',
      choices: { opt1: 'yes', opt2: 'if_needed' },
    });

    // Thanks state renders tallies from the mutation RESPONSE, not an
    // optimistic guess.
    expect(await screen.findByText(/thanks/i)).toBeInTheDocument();
    // Tallies come from the mutation RESPONSE (opt1 yes bumped to 2), not an
    // optimistic local guess.
    expect(screen.getByText(/2 yes/)).toBeInTheDocument();
  });

  it('prefills name/email from localStorage for a returning voter (re-vote allowed)', async () => {
    window.localStorage.setItem(VOTER_KEY, JSON.stringify({ name: 'Ada Lovelace', email: 'ada@example.com' }));
    fetchPublicPollMock.mockResolvedValue(openPoll());
    renderPage();

    await screen.findByText('Team Sync');

    expect((screen.getByLabelText(/your name/i) as HTMLInputElement).value).toBe('Ada Lovelace');
    expect((screen.getByLabelText(/your email/i) as HTMLInputElement).value).toBe('ada@example.com');
  });

  it('shows the winning time and disables voting controls on a confirmed poll', async () => {
    fetchPublicPollMock.mockResolvedValue(
      openPoll({ status: 'confirmed', winnerOptionId: 'opt1' })
    );
    renderPage();

    await screen.findByText('Team Sync');
    expect(screen.getByText(/confirmed/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /submit/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Yes' })).not.toBeInTheDocument();
  });

  it('shows a friendly not-found state for an unknown token', async () => {
    fetchPublicPollMock.mockRejectedValue(new ApiRequestError(404, 'not_found', 'poll not found'));
    renderPage('ghost-token');

    expect(await screen.findByText(/not found/i)).toBeInTheDocument();
  });

  it('shows a friendly message and refreshes state on a 409 (poll closed mid-vote)', async () => {
    const poll = openPoll();
    fetchPublicPollMock.mockResolvedValueOnce(poll);
    votePublicPollMock.mockRejectedValue(new ApiRequestError(409, 'conflict', 'poll is not open for voting'));
    fetchPublicPollMock.mockResolvedValueOnce(openPoll({ status: 'confirmed', winnerOptionId: 'opt1' }));
    renderPage();

    await screen.findByText('Team Sync');
    fireEvent.change(screen.getByLabelText(/your name/i), { target: { value: 'Grace Hopper' } });
    fireEvent.change(screen.getByLabelText(/your email/i), { target: { value: 'grace@example.com' } });
    const optionCards = screen.getAllByTestId(/poll-option-/);
    fireEvent.click(within(optionCards[0]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(within(optionCards[1]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(screen.getByRole('button', { name: /submit/i }));

    expect(await screen.findByText(/this poll just closed/i)).toBeInTheDocument();
  });

  it('shows a friendly message on a 429 (rate limited)', async () => {
    fetchPublicPollMock.mockResolvedValue(openPoll());
    votePublicPollMock.mockRejectedValue(new ApiRequestError(429, 'rate_limited', 'too many requests, slow down'));
    renderPage();

    await screen.findByText('Team Sync');
    fireEvent.change(screen.getByLabelText(/your name/i), { target: { value: 'Grace Hopper' } });
    fireEvent.change(screen.getByLabelText(/your email/i), { target: { value: 'grace@example.com' } });
    const optionCards = screen.getAllByTestId(/poll-option-/);
    fireEvent.click(within(optionCards[0]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(within(optionCards[1]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(screen.getByRole('button', { name: /submit/i }));

    expect(await screen.findByText(/too many/i)).toBeInTheDocument();
  });

  it('shows the validation message on a 400 without fabricating a submission', async () => {
    fetchPublicPollMock.mockResolvedValue(openPoll());
    votePublicPollMock.mockRejectedValue(
      new ApiRequestError(400, 'validation_failed', 'invalid voterEmail "nope"')
    );
    renderPage();

    await screen.findByText('Team Sync');
    fireEvent.change(screen.getByLabelText(/your name/i), { target: { value: 'Grace Hopper' } });
    fireEvent.change(screen.getByLabelText(/your email/i), { target: { value: 'nope' } });
    const optionCards = screen.getAllByTestId(/poll-option-/);
    fireEvent.click(within(optionCards[0]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(within(optionCards[1]!).getByRole('button', { name: 'Yes' }));
    fireEvent.click(screen.getByRole('button', { name: /submit/i }));

    expect(await screen.findByText(/invalid voterEmail/i)).toBeInTheDocument();
    expect(screen.queryByText(/thanks/i)).not.toBeInTheDocument();
  });
});
