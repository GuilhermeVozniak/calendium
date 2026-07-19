import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Comment, Team } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { CommentsPanel } from '@/components/app/comments-panel';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const getMeMock = vi.fn();
const listTeamsMock = vi.fn();
const listCommentsMock = vi.fn();
const addCommentMock = vi.fn();
const deleteCommentMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    getMe: getMeMock,
    listTeams: listTeamsMock,
    listComments: listCommentsMock,
    addComment: addCommentMock,
    deleteComment: deleteCommentMock,
  }),
}));

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const TEAMS: Team[] = [
  { id: 'team1', name: 'Design', createdBy: 'user_1', createdAt: new Date().toISOString() },
];

const COMMENTS: Comment[] = [
  {
    id: 'c1',
    threadId: 'thr_1',
    teamId: 'team1',
    authorId: 'user_1',
    body: 'I can take this one',
    mentions: [],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  },
  {
    id: 'c2',
    threadId: 'thr_1',
    teamId: 'team1',
    authorId: 'user_2',
    body: 'Thanks! Loop in @ada@example.com',
    mentions: ['user_3'],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  },
];

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <CommentsPanel threadId="thr_1" onClose={onClose} />
    </QueryClientProvider>
  );
  return { ...utils, onClose };
}

beforeEach(() => {
  vi.clearAllMocks();
  getMeMock.mockResolvedValue({ id: 'user_1', email: 'me@calendium.app' });
  listTeamsMock.mockResolvedValue(TEAMS);
  listCommentsMock.mockResolvedValue({ comments: COMMENTS });
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('CommentsPanel', () => {
  it('lists comments for the thread + team, labeling your own', async () => {
    renderPanel();

    expect(await screen.findByText('I can take this one')).toBeInTheDocument();
    expect(screen.getByText('Thanks! Loop in @ada@example.com')).toBeInTheDocument();
    expect(screen.getByText('You')).toBeInTheDocument();
    expect(listCommentsMock).toHaveBeenCalledWith('thr_1', 'team1');
  });

  it('posts a comment with the selected team and clears the box', async () => {
    const user = userEvent.setup();
    addCommentMock.mockResolvedValue(COMMENTS[0]);
    renderPanel();

    await screen.findByText('I can take this one');
    const box = screen.getByLabelText('Comment');
    await user.type(box, 'Looks good to me');
    await user.click(screen.getByRole('button', { name: 'Comment' }));

    expect(addCommentMock).toHaveBeenCalledWith('thr_1', {
      teamId: 'team1',
      body: 'Looks good to me',
    });
    await waitFor(() => expect(box).toHaveValue(''));
  });

  it('offers delete only on your own comments and calls the client', async () => {
    const user = userEvent.setup();
    deleteCommentMock.mockResolvedValue(undefined);
    renderPanel();

    await screen.findByText('I can take this one');
    const deleteButtons = screen.getAllByRole('button', { name: 'Delete comment' });
    expect(deleteButtons).toHaveLength(1);

    await user.click(deleteButtons[0]!);
    expect(deleteCommentMock).toHaveBeenCalledWith('c1');
  });

  it('explains the share prerequisite on a 404 instead of an error (no oracle)', async () => {
    listCommentsMock.mockRejectedValue(new ApiRequestError(404, 'not_found', 'not found'));
    renderPanel();

    expect(
      await screen.findByText('Comments open up once this conversation is shared with the team.')
    ).toBeInTheDocument();
  });

  it('closes via the header button', async () => {
    const user = userEvent.setup();
    const { onClose } = renderPanel();

    await user.click(await screen.findByRole('button', { name: 'Close comments' }));
    expect(onClose).toHaveBeenCalledOnce();
  });
});
