import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Team, ThreadShare } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ShareDialog, SHARE_THREAD_EVENT, dispatchShareThread } from '@/components/app/share-dialog';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const shareThreadMock = vi.fn();
const listThreadSharesMock = vi.fn();
const revokeThreadShareMock = vi.fn();
const listTeamsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    shareThread: shareThreadMock,
    listThreadShares: listThreadSharesMock,
    revokeThreadShare: revokeThreadShareMock,
    listTeams: listTeamsMock,
  }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const TEAMS: Team[] = [
  { id: 'team1', name: 'Design', createdBy: 'user_1', createdAt: new Date().toISOString() },
];

const EXISTING_SHARE: ThreadShare = {
  id: 'sh1',
  threadId: 'thr_1',
  createdBy: 'user_1',
  audience: 'external',
  teamId: null,
  revokedAt: null,
  expiresAt: null,
  createdAt: new Date().toISOString(),
};

function renderDialog(open = true) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onOpenChange = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <ShareDialog threadId="thr_1" open={open} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
  return { ...utils, onOpenChange };
}

beforeEach(() => {
  vi.clearAllMocks();
  listTeamsMock.mockResolvedValue([]);
  listThreadSharesMock.mockResolvedValue([]);
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('ShareDialog', () => {
  it('renders nothing (and runs no queries) while closed', () => {
    renderDialog(false);
    expect(screen.queryByText('Share conversation')).not.toBeInTheDocument();
    expect(listThreadSharesMock).not.toHaveBeenCalled();
  });

  it('creating an external share calls the client and surfaces the one-time link', async () => {
    const user = userEvent.setup();
    shareThreadMock.mockResolvedValue({
      share: { ...EXISTING_SHARE, id: 'sh2' },
      token: 'raw-token-once',
    });
    renderDialog();

    await user.click(await screen.findByRole('button', { name: 'Create link' }));

    expect(shareThreadMock).toHaveBeenCalledWith('thr_1', { audience: 'external' });
    const linkInput = await screen.findByLabelText('Share link');
    expect(linkInput).toHaveValue(`${window.location.origin}/shared/raw-token-once`);
    expect(
      screen.getByText('Copy it now — for security, this link is shown only once.')
    ).toBeInTheDocument();
  });

  it('creating a team share sends the chosen audience and team', async () => {
    const user = userEvent.setup();
    listTeamsMock.mockResolvedValue(TEAMS);
    shareThreadMock.mockResolvedValue({
      share: { ...EXISTING_SHARE, audience: 'team', teamId: 'team1' },
      token: 'raw-team-token',
    });
    renderDialog();

    await user.click(await screen.findByRole('button', { name: 'Team members only' }));
    expect(await screen.findByLabelText('Team')).toHaveValue('team1');
    await user.click(screen.getByRole('button', { name: 'Create link' }));

    expect(shareThreadMock).toHaveBeenCalledWith('thr_1', { audience: 'team', teamId: 'team1' });
    expect(await screen.findByLabelText('Share link')).toHaveValue(
      `${window.location.origin}/shared/raw-team-token`
    );
  });

  it('team audience is unavailable without a team', async () => {
    renderDialog();
    const teamButton = await screen.findByRole('button', { name: 'Team members only' });
    await waitFor(() => expect(teamButton).toBeDisabled());
    expect(screen.getByText('Join a team to create team-only links.')).toBeInTheDocument();
  });

  it('lists existing shares and revokes them through the client', async () => {
    const user = userEvent.setup();
    listThreadSharesMock.mockResolvedValue([EXISTING_SHARE]);
    revokeThreadShareMock.mockResolvedValue(undefined);
    renderDialog();

    expect(await screen.findByText('External link')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Revoke' }));

    expect(revokeThreadShareMock).toHaveBeenCalledWith('thr_1', 'sh1');
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Link revoked'));
  });

  it('marks revoked shares instead of offering another revoke', async () => {
    listThreadSharesMock.mockResolvedValue([
      { ...EXISTING_SHARE, revokedAt: new Date().toISOString() },
    ]);
    renderDialog();

    expect(await screen.findByText('Revoked')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument();
  });

  it('dispatchShareThread fires the palette event', () => {
    const handler = vi.fn();
    window.addEventListener(SHARE_THREAD_EVENT, handler);
    dispatchShareThread();
    window.removeEventListener(SHARE_THREAD_EVENT, handler);
    expect(handler).toHaveBeenCalledOnce();
  });
});
