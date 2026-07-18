import type { Message, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as CalendarView.test.tsx: force every orMock() call
// through the mock branch and supply test-controlled fixtures for '@/lib/mock'.
const fixtures = vi.hoisted(() => ({
  thread: null as { thread: Thread; messages: Message[] } | null,
  aiEnabled: true,
}));

const openComposeMock = vi.fn();
vi.mock('@/lib/compose', () => ({
  openCompose: (...args: unknown[]) => openComposeMock(...args),
}));

const aiAskCitedMock = vi.fn();
vi.mock('@/lib/api', () => ({
  api: {
    getThread: vi.fn(),
    getInstantReplies: vi.fn(),
    aiAskCited: (...args: unknown[]) => aiAskCitedMock(...args),
    proposeEvent: vi.fn(),
    listCalendars: vi.fn(),
    createEvent: vi.fn(),
  },
  orMock: async (_real: () => unknown, mock: () => unknown) => mock(),
}));

vi.mock('@/lib/mock', () => ({
  mockThread: () => fixtures.thread!,
  mockInstantReplies: () => fixtures.thread?.thread.instantReplies ?? [],
  mockAiAskCited: (question: string) => ({
    answer: `Based on your mailbox: ${question}`,
    model: 'demo/local-fallback',
    sources: [{ threadId: fixtures.thread!.thread.id, subject: fixtures.thread!.thread.subject, snippet: 'snippet' }],
  }),
  mockProposeEvent: () => ({ title: 'Follow-up', attendees: [], start: new Date().toISOString(), end: new Date().toISOString() }),
  mockCalendars: [],
}));

vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({ config: { features: { ai: fixtures.aiEnabled } } }),
}));

const toastMock = vi.fn();
vi.mock('@/lib/toast', () => ({
  toast: (...args: unknown[]) => toastMock(...args),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

import { ThreadPane } from './ThreadPane';

function makeThread(overrides: Partial<Thread> = {}): Thread {
  return {
    id: 'thr_1',
    accountId: 'acc_1',
    subject: 'Q3 roadmap review — final pass',
    snippet: 'snippet',
    participants: [{ name: 'Grace Hopper', email: 'grace@compilers.io' }],
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
    ...overrides,
  };
}

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'msg_1',
    threadId: 'thr_1',
    accountId: 'acc_1',
    from: { name: 'Grace Hopper', email: 'grace@compilers.io' },
    to: [{ name: 'Me', email: 'me@calendium.app' }],
    cc: [],
    bcc: [],
    subject: 'Q3 roadmap review — final pass',
    bodyHtml: '<p>Body</p>',
    bodyText: 'Body',
    attachments: [],
    sentAt: new Date().toISOString(),
    isDraft: false,
    openedAt: null,
    ...overrides,
  };
}

function renderPane(threadId = 'thr_1') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ThreadPane threadId={threadId} onAction={() => {}} />
    </QueryClientProvider>
  );
}

describe('ThreadPane — AI suite', () => {
  beforeEach(() => {
    fixtures.aiEnabled = true;
    fixtures.thread = { thread: makeThread(), messages: [makeMessage()] };
    openComposeMock.mockReset();
    aiAskCitedMock.mockReset();
    toastMock.mockReset();
  });

  it('shows a skeleton shimmer while the thread has no summary yet', async () => {
    renderPane();
    // findBy* throws if the node never appears, so resolving is sufficient.
    await screen.findByTestId('thread-summary-skeleton');
  });

  it('renders the AI summary once the thread carries one', async () => {
    fixtures.thread = { thread: makeThread({ summary: 'Grace incorporated feedback into the roadmap.' }), messages: [makeMessage()] };
    renderPane();
    await screen.findByText('Grace incorporated feedback into the roadmap.');
  });

  it('opens the composer prefilled when an instant-reply chip is clicked', async () => {
    fixtures.thread = {
      thread: makeThread({ instantReplies: ['Looks great, thanks!'] }),
      messages: [makeMessage()],
    };
    renderPane();
    const chip = await screen.findByText('Looks great, thanks!');
    await userEvent.click(chip);
    expect(openComposeMock).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'reply', body: 'Looks great, thanks!' })
    );
  });

  it('hides every AI affordance when the server has no AI', async () => {
    fixtures.aiEnabled = false;
    renderPane();
    await waitFor(() => expect(screen.getByText('Q3 roadmap review — final pass')).not.toBeNull());
    expect(screen.queryByLabelText('Ask AI')).toBeNull();
    expect(screen.queryByLabelText('Create event with AI')).toBeNull();
    expect(screen.queryByTestId('thread-summary-skeleton')).toBeNull();
  });

  it('Ask AI posts the question to aiAskCited and renders the answer', async () => {
    renderPane();
    await userEvent.click(await screen.findByLabelText('Ask AI'));
    const input = await screen.findByPlaceholderText("What's the status of this?");
    await userEvent.type(input, 'What is the status?');
    await userEvent.click(screen.getByLabelText('Ask'));

    await waitFor(() => expect(screen.getByText('Based on your mailbox: What is the status?')).not.toBeNull());
  });
});
