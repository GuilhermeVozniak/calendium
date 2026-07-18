import type { ReactNode } from 'react';
import type { Message, Reaction, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const demoState = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/demo', () => ({
  get DEMO_MODE() {
    return demoState.value;
  },
}));

const reactToMessageMock = vi.fn();
const removeReactionMock = vi.fn();
const unsendDraftMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    reactToMessage: (...args: unknown[]) => reactToMessageMock(...args),
    removeReaction: (...args: unknown[]) => removeReactionMock(...args),
    unsendDraft: (...args: unknown[]) => unsendDraftMock(...args),
  }),
}));

const mockReactToMessageMock = vi.fn();
const mockRemoveReactionMock = vi.fn();
vi.mock('@/lib/mail-mock', () => ({
  mockReactToMessage: (...args: unknown[]) => mockReactToMessageMock(...args),
  mockRemoveReaction: (...args: unknown[]) => mockRemoveReactionMock(...args),
}));

const toastErrorMock = vi.fn();
const toastSuccessMock = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastErrorMock(...args),
    success: (...args: unknown[]) => toastSuccessMock(...args),
  },
}));

// Imported after the mocks above so use-mail.ts picks them up.
import { useReactToMessage, type ThreadDetailResult } from '@/lib/use-mail';

// ---------------------------------------------------------------------------
// Fixtures / helpers
// ---------------------------------------------------------------------------

const THREAD_ID = 't1';
const MESSAGE_ID = 'm1';

function makeThread(): Thread {
  return {
    id: THREAD_ID,
    accountId: 'acc1',
    subject: 'Subject',
    snippet: 'snippet',
    participants: [],
    labelIds: [],
    split: 'important',
    messageCount: 1,
    unread: false,
    starred: false,
    lastMessageAt: new Date().toISOString(),
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
  };
}

function makeMessage(reactions: Reaction[] = []): Message {
  return {
    id: MESSAGE_ID,
    threadId: THREAD_ID,
    accountId: 'acc1',
    from: { name: 'Daniel Cho', email: 'daniel@northwind.com' },
    to: [{ name: 'You', email: 'me@calendium.app' }],
    cc: [],
    bcc: [],
    subject: 'Subject',
    bodyHtml: '<p>Body</p>',
    bodyText: 'Body',
    attachments: [],
    sentAt: new Date().toISOString(),
    isDraft: false,
    openedAt: null,
    reactions,
  };
}

function seedThread(queryClient: QueryClient, message: Message) {
  queryClient.setQueryData<ThreadDetailResult>(['thread', THREAD_ID], {
    thread: makeThread(),
    messages: [message],
    source: 'api',
  });
}

function readMessage(queryClient: QueryClient): Message {
  const data = queryClient.getQueryData<ThreadDetailResult>(['thread', THREAD_ID]);
  return data!.messages[0]!;
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function renderReact(queryClient: QueryClient) {
  return renderHook(() => useReactToMessage(), { wrapper: createWrapper(queryClient) });
}

function reaction(emoji: string, delivery: 'local' | 'sent' = 'local'): Reaction {
  return { id: `rx-${emoji}`, messageId: MESSAGE_ID, emoji, delivery, createdAt: new Date().toISOString() };
}

beforeEach(() => {
  vi.clearAllMocks();
  demoState.value = false;
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('useReactToMessage honesty contract', () => {
  it('react(): reconciles the cache with the real server reaction on success (no toast when delivery is local)', async () => {
    const queryClient = new QueryClient();
    seedThread(queryClient, makeMessage());
    const serverReaction = reaction('👍', 'local');
    reactToMessageMock.mockResolvedValue({ reaction: serverReaction, draftId: null });

    const { result } = renderReact(queryClient);
    await result.current.react(THREAD_ID, MESSAGE_ID, '👍', false);

    expect(reactToMessageMock).toHaveBeenCalledWith(MESSAGE_ID, '👍', false);
    expect(readMessage(queryClient).reactions).toEqual([serverReaction]);
    expect(toastSuccessMock).not.toHaveBeenCalled();
  });

  it('react(): shows the undo-capable tiny-reply toast only when the response carries a draftId', async () => {
    const queryClient = new QueryClient();
    seedThread(queryClient, makeMessage());
    reactToMessageMock.mockResolvedValue({ reaction: reaction('👍', 'sent'), draftId: 'draft-1' });

    const { result } = renderReact(queryClient);
    await result.current.react(THREAD_ID, MESSAGE_ID, '👍', true);

    expect(toastSuccessMock).toHaveBeenCalledWith(
      'Sent a tiny reply',
      expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
    );

    // The Undo action wires to unsendDraft(draftId).
    const call = toastSuccessMock.mock.calls[0]!;
    const opts = call[1] as { action: { onClick: () => void } };
    opts.action.onClick();
    await Promise.resolve();
    expect(unsendDraftMock).toHaveBeenCalledWith('draft-1');
  });

  it('react(): a real API rejection reverts the optimistic chip and toasts', async () => {
    const queryClient = new QueryClient();
    seedThread(queryClient, makeMessage());
    reactToMessageMock.mockRejectedValue(new Error('network down'));

    const { result } = renderReact(queryClient);
    await result.current.react(THREAD_ID, MESSAGE_ID, '👍', false);

    expect(readMessage(queryClient).reactions).toEqual([]);
    expect(toastErrorMock).toHaveBeenCalledWith('Could not add the reaction.');
  });

  it('react(): demo mode applies the mock result and never reverts on the real call rejecting', async () => {
    demoState.value = true;
    const queryClient = new QueryClient();
    seedThread(queryClient, makeMessage());
    const demoReaction = reaction('🎉', 'local');
    mockReactToMessageMock.mockReturnValue({ reaction: demoReaction, draftId: null });
    reactToMessageMock.mockRejectedValue(new Error('offline'));

    const { result } = renderReact(queryClient);
    await result.current.react(THREAD_ID, MESSAGE_ID, '🎉', false);

    expect(mockReactToMessageMock).toHaveBeenCalledWith(MESSAGE_ID, '🎉', false);
    expect(readMessage(queryClient).reactions).toEqual([demoReaction]);
    expect(toastErrorMock).not.toHaveBeenCalled();
  });

  it('removeReaction(): success leaves the emoji removed', async () => {
    const queryClient = new QueryClient();
    seedThread(queryClient, makeMessage([reaction('👍')]));
    removeReactionMock.mockResolvedValue(undefined);

    const { result } = renderReact(queryClient);
    await result.current.removeReaction(THREAD_ID, MESSAGE_ID, '👍');

    expect(removeReactionMock).toHaveBeenCalledWith(MESSAGE_ID, '👍');
    expect(readMessage(queryClient).reactions).toEqual([]);
  });

  it('removeReaction(): a real API rejection restores the chip and toasts', async () => {
    const queryClient = new QueryClient();
    const existing = reaction('👍');
    seedThread(queryClient, makeMessage([existing]));
    removeReactionMock.mockRejectedValue(new Error('network down'));

    const { result } = renderReact(queryClient);
    await result.current.removeReaction(THREAD_ID, MESSAGE_ID, '👍');

    expect(readMessage(queryClient).reactions).toEqual([existing]);
    expect(toastErrorMock).toHaveBeenCalledWith('Could not remove the reaction.');
  });
});
