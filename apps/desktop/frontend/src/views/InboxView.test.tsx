import type { OpenEvent, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as ThreadPane.test.tsx: force every orMock() call
// through the mock branch and supply test-controlled fixtures for '@/lib/mock'.
// InboxView also mounts ThreadPane, so the api/mock surfaces it depends on are
// stubbed here too (none of it is exercised without a thread selected, but the
// module has to resolve without throwing).
const fixtures = vi.hoisted(() => ({
  threads: [] as Thread[],
  opens: [] as OpenEvent[],
}));

vi.mock('@/lib/api', () => ({
  api: {
    listThreads: vi.fn(),
    listOpens: vi.fn(),
    markThreadOpened: vi.fn(),
    actOnThread: vi.fn(),
    snoozeThread: vi.fn(),
    unsnoozeThread: vi.fn(),
    bulkThreadAction: vi.fn(),
    getThread: vi.fn(),
    getInstantReplies: vi.fn(),
    aiAskCited: vi.fn(),
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

vi.mock('@/lib/mock', () => ({
  mockThreads: () => fixtures.threads,
  mockOpens: () => fixtures.opens,
  mockThread: () => null,
  mockInstantReplies: () => [],
  mockAiAskCited: () => ({ answer: '', model: 'demo', sources: [] }),
  mockProposeEvent: () => ({ title: '', attendees: [], start: new Date().toISOString(), end: new Date().toISOString() }),
  mockCalendars: [],
  mockAttachmentsForThread: () => [],
  mockAttachmentBlob: () => new Blob(),
  mockContactSummary: () => null,
  mockReactToMessage: vi.fn(),
  mockRemoveReaction: vi.fn(),
  mockUser: {
    id: 'usr_me',
    email: 'me@calendium.app',
    name: 'Me',
    avatarUrl: null,
    createdAt: new Date().toISOString(),
  },
}));

vi.mock('@/lib/server-config', () => ({
  isDemoMode: () => false,
  useServerConfig: () => ({ config: { features: { ai: false } } }),
}));

vi.mock('@/lib/toast', () => ({
  toast: vi.fn(),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

import { InboxView } from './InboxView';

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

function makeOpen(overrides: Partial<OpenEvent> = {}): OpenEvent {
  return {
    messageId: 'msg_1',
    threadId: 'thr_2',
    accountId: 'acc_1',
    subject: 'Re: Contract renewal — signature needed',
    recipients: [{ name: 'Margaret Hamilton', email: 'margaret@apollo.dev' }],
    openedAt: new Date().toISOString(),
    sentAt: new Date().toISOString(),
    ...overrides,
  };
}

function renderInbox() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <InboxView split="important" />
    </QueryClientProvider>
  );
}

describe('InboxView — Opens feed toggle pane', () => {
  beforeEach(() => {
    fixtures.threads = [makeThread()];
    fixtures.opens = [];
  });

  it('shows the inbox thread list by default', async () => {
    renderInbox();
    await screen.findByText('Q3 roadmap review — final pass');
    expect(screen.queryByText('Recent opens')).toBeNull();
  });

  it('renders the mock Opens feed page when toggled on', async () => {
    fixtures.opens = [
      makeOpen({ subject: 'Re: Contract renewal — signature needed' }),
      makeOpen({ messageId: 'msg_2', subject: 'Re: Dinner Friday?', recipients: [{ name: 'Charles Babbage', email: 'charles@difference.engine' }] }),
    ];
    renderInbox();
    await screen.findByText('Q3 roadmap review — final pass');

    await userEvent.click(screen.getByRole('button', { name: /opens/i }));

    await screen.findByText('Recent opens');
    await screen.findByText('Re: Contract renewal — signature needed');
    await screen.findByText('Re: Dinner Friday?');
    await screen.findByText('Opened by Margaret Hamilton');
  });

  it('shows an empty state when there are no opens yet', async () => {
    renderInbox();
    await userEvent.click(screen.getByRole('button', { name: /opens/i }));
    await screen.findByText('No opens yet');
  });

  it('toggles back to the inbox list', async () => {
    renderInbox();
    await userEvent.click(screen.getByRole('button', { name: /opens/i }));
    await screen.findByText('Recent opens');

    await userEvent.click(screen.getByRole('button', { name: /back to inbox/i }));
    await screen.findByText('Q3 roadmap review — final pass');
    expect(screen.queryByText('Recent opens')).toBeNull();
  });
});
