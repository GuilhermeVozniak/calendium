import { act, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SharedThreadView as SharedThreadViewData } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { SharedThreadView } from '@/components/share/shared-thread-view';
import type { CollabEvent, CollabStreamOptions } from '@/lib/collab-stream';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const getSharedThreadMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({ getSharedThread: getSharedThreadMock }),
}));

let streamHandler: ((ev: CollabEvent) => void) | null = null;
const closeMock = vi.fn();
const openCollabStreamMock = vi.fn(
  (onEvent: (ev: CollabEvent) => void, _options?: CollabStreamOptions) => {
    streamHandler = onEvent;
    return closeMock;
  }
);
vi.mock('@/lib/collab-stream', () => ({
  openCollabStream: (onEvent: (ev: CollabEvent) => void, options?: CollabStreamOptions) =>
    openCollabStreamMock(onEvent, options),
}));

vi.mock('@/lib/demo', () => ({ DEMO_MODE: false }));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const VIEW: SharedThreadViewData = {
  subject: 'Renewal terms for FY27',
  audience: 'external',
  updatedAt: new Date().toISOString(),
  messages: [
    {
      id: 'm1',
      threadId: 't1',
      accountId: 'acc1',
      from: { name: 'Daniel Cho', email: 'daniel@northwind.com' },
      to: [{ name: 'Ada Park', email: 'ada@calendium.app' }],
      cc: [],
      bcc: [],
      subject: 'Renewal terms for FY27',
      bodyHtml: '<p>legal cleared the redlines</p>',
      bodyText: 'legal cleared the redlines',
      attachments: [],
      sentAt: new Date().toISOString(),
      isDraft: false,
      openedAt: null,
      reactions: [],
    },
  ],
};

function renderView(token = 'tok123') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <SharedThreadView token={token} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  streamHandler = null;
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('SharedThreadView', () => {
  it('renders the subject and message list read-only — no compose or triage affordances', async () => {
    getSharedThreadMock.mockResolvedValue(VIEW);
    renderView();

    expect(await screen.findByText('Renewal terms for FY27')).toBeInTheDocument();
    expect(screen.getByText('legal cleared the redlines')).toBeInTheDocument();
    expect(screen.getByText('Daniel Cho')).toBeInTheDocument();

    for (const name of [/reply/i, /forward/i, /archive/i, /snooze/i, /star/i, /compose/i, /share/i]) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument();
    }
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('shows the team badge on team-audience shares', async () => {
    getSharedThreadMock.mockResolvedValue({ ...VIEW, audience: 'team' });
    renderView();

    expect(await screen.findByText('Team')).toBeInTheDocument();
  });

  it('renders the inactive-link state on 404 (revoked, expired, and unknown are indistinguishable)', async () => {
    getSharedThreadMock.mockRejectedValue(new ApiRequestError(404, 'not_found', 'not found'));
    renderView();

    expect(await screen.findByText('This link is no longer active')).toBeInTheDocument();
    expect(screen.queryByText('Renewal terms for FY27')).not.toBeInTheDocument();
  });

  it('renders a generic failure state on non-404 errors', async () => {
    getSharedThreadMock.mockRejectedValue(new ApiRequestError(500, 'internal', 'boom'));
    renderView();

    expect(await screen.findByText('Something went wrong')).toBeInTheDocument();
    expect(screen.queryByText('This link is no longer active')).not.toBeInTheDocument();
  });

  it('opens the token-scoped SSE stream and refetches on share.updated (no polling)', async () => {
    getSharedThreadMock.mockResolvedValue(VIEW);
    renderView('tok 123');

    await screen.findByText('Renewal terms for FY27');
    expect(getSharedThreadMock).toHaveBeenCalledTimes(1);
    expect(openCollabStreamMock).toHaveBeenCalledWith(
      expect.any(Function),
      expect.objectContaining({ path: '/v1/shared/threads/tok%20123/stream' })
    );

    act(() => {
      streamHandler?.({ topic: 'share:sh1', type: 'share.updated', payload: null });
    });

    await waitFor(() => expect(getSharedThreadMock).toHaveBeenCalledTimes(2));
  });

  it('ignores unrelated stream events and closes the stream on unmount', async () => {
    getSharedThreadMock.mockResolvedValue(VIEW);
    const { unmount } = renderView();

    await screen.findByText('Renewal terms for FY27');
    act(() => {
      streamHandler?.({ topic: 'team:team1', type: 'comment.created', payload: null });
    });
    expect(getSharedThreadMock).toHaveBeenCalledTimes(1);

    unmount();
    expect(closeMock).toHaveBeenCalled();
  });
});
