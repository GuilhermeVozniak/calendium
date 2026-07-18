import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  FindATimeGrid,
  ProposalsList,
  ProposeTimeForm,
} from '@/components/app/find-a-time';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const getFreeBusyMock = vi.fn();
const proposeTimeMock = vi.fn();
const listProposalsMock = vi.fn();
const acceptProposalMock = vi.fn();
const declineProposalMock = vi.fn();

vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    getFreeBusy: (...args: unknown[]) => getFreeBusyMock(...args),
    proposeTime: (...args: unknown[]) => proposeTimeMock(...args),
    listProposals: (...args: unknown[]) => listProposalsMock(...args),
    acceptProposal: (...args: unknown[]) => acceptProposalMock(...args),
    declineProposal: (...args: unknown[]) => declineProposalMock(...args),
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

const DAY = new Date(2026, 6, 20, 0, 0, 0, 0);

// ---------------------------------------------------------------------------
// FindATimeGrid
// ---------------------------------------------------------------------------

describe('FindATimeGrid', () => {
  it('shades a cell busy when the mocked free/busy response covers that slot', async () => {
    getFreeBusyMock.mockResolvedValue({
      'a@example.com': [
        {
          start: new Date(2026, 6, 20, 9, 0).toISOString(),
          end: new Date(2026, 6, 20, 9, 30).toISOString(),
        },
      ],
    });
    render(
      <FindATimeGrid
        attendeeEmails={['a@example.com']}
        durationMinutes={30}
        initialDate={DAY}
        onPick={vi.fn()}
      />
    );

    await waitFor(() => expect(getFreeBusyMock).toHaveBeenCalledTimes(1));

    const busyCell = await screen.findByTestId(
      `cell-a@example.com-${new Date(2026, 6, 20, 9, 0).toISOString()}`
    );
    expect(busyCell).toHaveAttribute('data-busy', 'true');

    const freeCell = screen.getByTestId(
      `cell-a@example.com-${new Date(2026, 6, 20, 10, 0).toISOString()}`
    );
    expect(freeCell).toHaveAttribute('data-busy', 'false');
  });

  it('invokes onPick with a duration-sized range when a column header is clicked', async () => {
    getFreeBusyMock.mockResolvedValue({ 'a@example.com': [] });
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <FindATimeGrid
        attendeeEmails={['a@example.com']}
        durationMinutes={45}
        initialDate={DAY}
        onPick={onPick}
      />
    );

    await waitFor(() => expect(getFreeBusyMock).toHaveBeenCalledTimes(1));
    const columnButton = await screen.findByLabelText(/Pick 9:00 AM/i);
    await user.click(columnButton);

    expect(onPick).toHaveBeenCalledTimes(1);
    const [start, end] = onPick.mock.calls[0]!;
    expect(start).toBeInstanceOf(Date);
    expect(end).toBeInstanceOf(Date);
    expect((end as Date).getTime() - (start as Date).getTime()).toBe(45 * 60_000);
  });

  it('labels an attendee the provider could not resolve as availability unknown, never free', async () => {
    // Response omits b@example.com entirely — the provider couldn't resolve it.
    getFreeBusyMock.mockResolvedValue({ 'a@example.com': [] });
    render(
      <FindATimeGrid
        attendeeEmails={['a@example.com', 'b@example.com']}
        durationMinutes={30}
        initialDate={DAY}
        onPick={vi.fn()}
      />
    );

    await waitFor(() => expect(getFreeBusyMock).toHaveBeenCalledTimes(1));
    const row = await screen.findByText('b@example.com');
    expect(row.parentElement).toHaveTextContent('availability unknown');
    // The resolved attendee's row must not carry the same disclaimer.
    const resolvedRow = screen.getByText('a@example.com');
    expect(resolvedRow.parentElement).not.toHaveTextContent('availability unknown');
  });

  it('labels the "You" row as availability unknown too (its own schedule is never queried here)', async () => {
    getFreeBusyMock.mockResolvedValue({ 'a@example.com': [] });
    render(
      <FindATimeGrid
        attendeeEmails={['a@example.com']}
        durationMinutes={30}
        initialDate={DAY}
        onPick={vi.fn()}
      />
    );
    await waitFor(() => expect(getFreeBusyMock).toHaveBeenCalledTimes(1));
    const youRow = await screen.findByText('You');
    expect(youRow.parentElement).toHaveTextContent('availability unknown');
  });

  it('never blocks manual picking on a free/busy fetch failure', async () => {
    getFreeBusyMock.mockRejectedValue(new Error('provider down'));
    const onPick = vi.fn();
    const user = userEvent.setup();
    render(
      <FindATimeGrid
        attendeeEmails={['a@example.com']}
        durationMinutes={30}
        initialDate={DAY}
        onPick={onPick}
      />
    );

    await waitFor(() => expect(getFreeBusyMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole('alert')).toHaveTextContent(/could not load availability/i);
    const columnButton = await screen.findByLabelText(/Pick 9:00 AM/i);
    await user.click(columnButton);
    expect(onPick).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// ProposeTimeForm
// ---------------------------------------------------------------------------

describe('ProposeTimeForm', () => {
  const initialStart = new Date(2026, 6, 20, 9, 0);
  const initialEnd = new Date(2026, 6, 20, 9, 30);

  it('submits proposeTime with the entered start/end and note, then calls onProposed', async () => {
    proposeTimeMock.mockResolvedValue({ id: 'prop-1' });
    const onProposed = vi.fn();
    const user = userEvent.setup();
    render(
      <ProposeTimeForm
        eventId="ev-1"
        initialStart={initialStart}
        initialEnd={initialEnd}
        onProposed={onProposed}
      />
    );

    await user.type(screen.getByLabelText('Proposal note'), 'Works better for me');
    await user.click(screen.getByRole('button', { name: /propose time/i }));

    await waitFor(() => expect(proposeTimeMock).toHaveBeenCalledTimes(1));
    const [eventId, input] = proposeTimeMock.mock.calls[0]!;
    expect(eventId).toBe('ev-1');
    expect(input.start).toBe(initialStart.toISOString());
    expect(input.end).toBe(initialEnd.toISOString());
    expect(input.note).toBe('Works better for me');
    expect(onProposed).toHaveBeenCalledTimes(1);
  });

  it('shows an error and does not call onProposed when the API call fails', async () => {
    proposeTimeMock.mockRejectedValue(new Error('network down'));
    const onProposed = vi.fn();
    const user = userEvent.setup();
    render(
      <ProposeTimeForm
        eventId="ev-1"
        initialStart={initialStart}
        initialEnd={initialEnd}
        onProposed={onProposed}
      />
    );
    await user.click(screen.getByRole('button', { name: /propose time/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/could not propose/i);
    expect(onProposed).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// ProposalsList
// ---------------------------------------------------------------------------

describe('ProposalsList', () => {
  const PENDING = {
    id: 'prop-1',
    eventId: 'ev-1',
    proposerEmail: 'guest@example.com',
    proposerName: 'Guest',
    start: new Date(2026, 6, 20, 14, 0).toISOString(),
    end: new Date(2026, 6, 20, 14, 30).toISOString(),
    note: 'Prefer afternoon',
    status: 'pending' as const,
    createdAt: new Date(2026, 6, 19).toISOString(),
  };

  it('lists pending proposals and calls acceptProposal + onAccepted on Accept', async () => {
    listProposalsMock.mockResolvedValue([PENDING]);
    acceptProposalMock.mockResolvedValue({ id: 'ev-1' });
    const onAccepted = vi.fn();
    const user = userEvent.setup();
    render(<ProposalsList eventId="ev-1" onAccepted={onAccepted} />);

    expect(await screen.findByText('Guest')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /accept/i }));

    await waitFor(() => expect(acceptProposalMock).toHaveBeenCalledWith('ev-1', 'prop-1'));
    expect(onAccepted).toHaveBeenCalledTimes(1);
  });

  it('calls declineProposal and refetches on Decline', async () => {
    listProposalsMock.mockResolvedValueOnce([PENDING]).mockResolvedValueOnce([]);
    declineProposalMock.mockResolvedValue(undefined);
    const user = userEvent.setup();
    render(<ProposalsList eventId="ev-1" onAccepted={vi.fn()} />);

    expect(await screen.findByText('Guest')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /decline/i }));

    await waitFor(() => expect(declineProposalMock).toHaveBeenCalledWith('ev-1', 'prop-1'));
    await waitFor(() => expect(listProposalsMock).toHaveBeenCalledTimes(2));
  });

  it('on a 409 (already resolved) accept failure, refreshes state instead of claiming success', async () => {
    listProposalsMock.mockResolvedValueOnce([PENDING]).mockResolvedValueOnce([]);
    acceptProposalMock.mockRejectedValue(
      Object.assign(new Error('Conflict'), { status: 409 })
    );
    const onAccepted = vi.fn();
    const user = userEvent.setup();
    render(<ProposalsList eventId="ev-1" onAccepted={onAccepted} />);

    expect(await screen.findByText('Guest')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /accept/i }));

    await waitFor(() => expect(acceptProposalMock).toHaveBeenCalledTimes(1));
    expect(onAccepted).not.toHaveBeenCalled();
    expect(await screen.findByRole('alert')).toHaveTextContent(/already resolved/i);
    await waitFor(() => expect(listProposalsMock).toHaveBeenCalledTimes(2));
  });

  it('renders nothing when there are no pending proposals', async () => {
    listProposalsMock.mockResolvedValue([]);
    const { container } = render(<ProposalsList eventId="ev-1" onAccepted={vi.fn()} />);
    await waitFor(() => expect(listProposalsMock).toHaveBeenCalledTimes(1));
    expect(container.textContent).toBe('');
  });
});
