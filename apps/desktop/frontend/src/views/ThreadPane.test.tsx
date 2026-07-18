import type { AttachmentHit, Calendar, Message, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as CalendarView.test.tsx: force every orMock() call
// through the mock branch and supply test-controlled fixtures for '@/lib/mock'.
const fixtures = vi.hoisted(() => ({
  thread: null as { thread: Thread; messages: Message[] } | null,
  aiEnabled: true,
  calendars: [] as Calendar[],
  attachments: [] as AttachmentHit[],
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
    searchAttachments: vi.fn(),
    getContact: vi.fn(),
    reactToMessage: vi.fn(),
    removeReaction: vi.fn(),
  },
  fetchAttachmentBlob: vi.fn(),
  orMock: async (_real: () => unknown, mock: () => unknown) => mock(),
}));

const reactMock = vi.fn();
const removeReactionMock = vi.fn();

// createMockEvent/mockEvents are the real implementations (via importActual) so
// tests can assert the demo "Create event with AI" path actually lands in the
// shared mock event store, not just that a promise resolves. mockContactSummary
// is also left real: makeThread's default participant (grace@compilers.io)
// matches the real thr_1 seed, so the contact header exercises real logic.
vi.mock('@/lib/mock', async () => {
  const actual = await vi.importActual<typeof import('@/lib/mock')>('@/lib/mock');
  return {
    ...actual,
    mockThread: () => fixtures.thread!,
    mockInstantReplies: () => fixtures.thread?.thread.instantReplies ?? [],
    mockAiAskCited: (question: string) => ({
      answer: `Based on your mailbox: ${question}`,
      model: 'demo/local-fallback',
      sources: [{ threadId: fixtures.thread!.thread.id, subject: fixtures.thread!.thread.subject, snippet: 'snippet' }],
    }),
    mockProposeEvent: () => ({
      title: 'Follow-up',
      attendees: [],
      start: new Date().toISOString(),
      end: new Date().toISOString(),
    }),
    get mockCalendars() {
      return fixtures.calendars;
    },
    mockAttachmentsForThread: () => fixtures.attachments,
    mockAttachmentBlob: () => new Blob(['%PDF-1.4'], { type: 'application/pdf' }),
    mockReactToMessage: (...args: unknown[]) => reactMock(...args),
    mockRemoveReaction: (...args: unknown[]) => removeReactionMock(...args),
  };
});

vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({ config: { features: { ai: fixtures.aiEnabled } } }),
}));

const toastMock = vi.fn();
vi.mock('@/lib/toast', () => ({
  toast: (...args: unknown[]) => toastMock(...args),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

import { mockEvents } from '@/lib/mock';

import { ThreadPane } from './ThreadPane';

function makeCalendar(overrides: Partial<Calendar> = {}): Calendar {
  return {
    id: 'cal_work',
    accountId: 'acc_google',
    name: 'Work',
    color: '#0ea5e9',
    timeZone: 'UTC',
    isPrimary: true,
    isVisible: true,
    canWrite: true,
    ...overrides,
  };
}

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
    reactions: [],
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
    fixtures.calendars = [makeCalendar()];
    fixtures.attachments = [];
    openComposeMock.mockReset();
    aiAskCitedMock.mockReset();
    toastMock.mockReset();
    reactMock.mockReset();
    removeReactionMock.mockReset();
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

  it('demo "Create event with AI" inserts the proposed event into the mock store, not just a resolved promise', async () => {
    const wideRange = {
      from: new Date(Date.now() - 365 * 24 * 60 * 60 * 1000).toISOString(),
      to: new Date(Date.now() + 365 * 24 * 60 * 60 * 1000).toISOString(),
    };
    // Sanity check: nothing named "Follow-up" exists in the store yet.
    expect(mockEvents(wideRange.from, wideRange.to).some((e) => e.title === 'Follow-up')).toBe(false);

    renderPane();
    await userEvent.click(await screen.findByLabelText('Create event with AI'));
    await screen.findByText('Follow-up');
    await userEvent.click(screen.getByText('Create event'));

    await waitFor(() => expect(toastMock).toHaveBeenCalledWith(expect.objectContaining({ title: 'Event created' })));
    expect(mockEvents(wideRange.from, wideRange.to).some((e) => e.title === 'Follow-up')).toBe(true);
  });

  it('reacting to a message calls the reaction client and renders the returned delivery state', async () => {
    reactMock.mockReturnValue({
      reaction: {
        id: 'rxn_1',
        messageId: 'msg_1',
        emoji: '👍',
        delivery: 'sent',
        createdAt: new Date().toISOString(),
      },
      draftId: 'draft_1',
    });
    renderPane();
    await userEvent.click(await screen.findByLabelText(/react to message/i));
    await userEvent.click(await screen.findByLabelText('React with 👍 and send as reply'));

    await waitFor(() => expect(reactMock).toHaveBeenCalledWith('msg_1', '👍', true));
    // The delivery badge reflects the server's actual response (delivery: 'sent'),
    // not just that "send as reply" was requested.
    await screen.findByLabelText('Delivered as a reply');
  });

  it('removes a reaction when clicking an already-reacted chip', async () => {
    fixtures.thread = {
      thread: makeThread(),
      messages: [
        makeMessage({
          reactions: [
            { id: 'rxn_1', messageId: 'msg_1', emoji: '🎉', delivery: 'local', createdAt: new Date().toISOString() },
          ],
        }),
      ],
    };
    renderPane();
    const chip = await screen.findByText('🎉');
    await userEvent.click(chip);
    await waitFor(() => expect(removeReactionMock).toHaveBeenCalledWith('msg_1', '🎉'));
  });

  it('opens a PDF attachment preview from the Attachments list', async () => {
    fixtures.attachments = [
      {
        id: 'att_1',
        filename: 'MSA-v3.pdf',
        mimeType: 'application/pdf',
        sizeBytes: 245_760,
        messageId: 'msg_1',
        threadId: 'thr_1',
        threadSubject: 'Q3 roadmap review — final pass',
        from: { name: 'Grace Hopper', email: 'grace@compilers.io' },
        sentAt: new Date().toISOString(),
      },
    ];
    renderPane();
    await userEvent.click(await screen.findByText(/MSA-v3\.pdf/));
    const dialog = await screen.findByRole('dialog');
    const iframe = await within(dialog).findByTitle('MSA-v3.pdf');
    expect((iframe as HTMLIFrameElement).src).toContain('blob:');
  });

  it('does not open a preview for a non-PDF attachment', async () => {
    fixtures.attachments = [
      {
        id: 'att_2',
        filename: 'notes.txt',
        mimeType: 'text/plain',
        sizeBytes: 512,
        messageId: 'msg_1',
        threadId: 'thr_1',
        threadSubject: 'Q3 roadmap review — final pass',
        from: { name: 'Grace Hopper', email: 'grace@compilers.io' },
        sentAt: new Date().toISOString(),
      },
    ];
    renderPane();
    await userEvent.click(await screen.findByText(/notes\.txt/));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('renders the contact summary header for a known correspondent', async () => {
    renderPane();
    await screen.findByText('Grace Hopper');
    await screen.findByText('· compilers.io');
  });
});
