import type { OpenEvent, Thread } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as ThreadPane.test.tsx: force every orMock() call
// through the mock branch and supply test-controlled fixtures for '@/lib/mock'.
// InboxView also mounts ThreadPane, so the api/mock surfaces it depends on are
// stubbed here too (none of it is exercised without a thread selected, but the
// module has to resolve without throwing).
const fixtures = vi.hoisted(() => ({
  threads: [] as Thread[],
  opens: [] as OpenEvent[],
}));

// Spy on mockThread so the prefetch tests can observe exactly which thread
// bodies get warmed (orMock is forced through the mock branch below).
const mockThreadSpy = vi.hoisted(() => vi.fn(() => null));

vi.mock('@/lib/api', () => ({
  api: {
    listThreads: vi.fn(),
    listOpens: vi.fn(),
    listAccounts: vi.fn(),
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
  mockThread: (...args: unknown[]) => mockThreadSpy(...(args as [])),
  mockAccounts: [
    {
      id: 'acc_1',
      provider: 'google',
      email: 'ada@calendium.app',
      status: 'active',
      scopes: [],
      vipSenders: [],
      signatureHtml: '',
      autoBcc: [],
      lastSyncedAt: null,
      createdAt: new Date().toISOString(),
    },
    {
      id: 'acc_2',
      provider: 'google',
      email: 'work@acme.com',
      status: 'active',
      scopes: [],
      vipSenders: [],
      signatureHtml: '',
      autoBcc: [],
      lastSyncedAt: null,
      createdAt: new Date().toISOString(),
    },
  ],
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

describe('InboxView — thread prefetching (M2.6 Task 7)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    fixtures.threads = [
      makeThread({ id: 'thr_1', subject: 'First subject' }),
      makeThread({ id: 'thr_2', subject: 'Second subject' }),
      makeThread({ id: 'thr_3', subject: 'Third subject' }),
      makeThread({ id: 'thr_4', subject: 'Fourth subject' }),
    ];
    fixtures.opens = [];
    mockThreadSpy.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  function warmedIds(): unknown[] {
    return mockThreadSpy.mock.calls.map((call: unknown[]) => call[0]);
  }

  it('prefetches the j/k neighbors (idx+1, idx+2) of the selected thread after the settle delay', async () => {
    renderInbox();
    // First advancement settles the list query (its microtask chain runs while
    // the clock moves); the second runs out the debounce window the neighbor
    // effect schedules once the list has rendered.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });

    const warmed = warmedIds();
    expect(warmed).toContain('thr_2');
    expect(warmed).toContain('thr_3');
    // idx+3 is not a j/k target — no storm past ±2.
    expect(warmed).not.toContain('thr_4');
  });

  it('a quick mouse pass does not prefetch; sustained hover prefetches that row', async () => {
    renderInbox();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });
    mockThreadSpy.mockClear();

    // React derives onMouseEnter/onMouseLeave from mouseover/mouseout.
    const row = screen.getByText('Fourth subject').closest('button')!;
    fireEvent.mouseOver(row);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(40);
    });
    fireEvent.mouseOut(row);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    expect(warmedIds()).not.toContain('thr_4');

    fireEvent.mouseOver(row);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(80);
    });
    expect(warmedIds()).toContain('thr_4');
  });
});

describe('InboxView — account switching (mod+1..9)', () => {
  beforeEach(() => {
    fixtures.threads = [makeThread()];
    fixtures.opens = [];
    window.localStorage.clear();
  });

  it('defaults to All accounts, switches with mod+2, and clears with mod+0', async () => {
    renderInbox();
    await screen.findByText('Q3 roadmap review — final pass');
    expect(screen.getByRole('button', { name: /all accounts/i })).toBeTruthy();

    fireEvent.keyDown(window, { key: '2', metaKey: true, ctrlKey: true });
    expect(await screen.findByRole('button', { name: /work@acme\.com/i })).toBeTruthy();
    expect(window.localStorage.getItem('calendium.activeAccountId')).toBe('acc_2');

    fireEvent.keyDown(window, { key: '0', metaKey: true, ctrlKey: true });
    expect(await screen.findByRole('button', { name: /all accounts/i })).toBeTruthy();
    expect(window.localStorage.getItem('calendium.activeAccountId')).toBeNull();
  });

  it('mod+5 with two accounts is a no-op', async () => {
    renderInbox();
    await screen.findByText('Q3 roadmap review — final pass');
    fireEvent.keyDown(window, { key: '5', metaKey: true, ctrlKey: true });
    expect(screen.getByRole('button', { name: /all accounts/i })).toBeTruthy();
  });

  it('ignores mod+digits while typing in an input', async () => {
    renderInbox();
    await screen.findByText('Q3 roadmap review — final pass');
    const input = document.createElement('input');
    document.body.appendChild(input);
    fireEvent.keyDown(input, { key: '2', metaKey: true, ctrlKey: true });
    expect(screen.getByRole('button', { name: /all accounts/i })).toBeTruthy();
    input.remove();
  });

  it('restores the persisted account scope', async () => {
    window.localStorage.setItem('calendium.activeAccountId', 'acc_2');
    renderInbox();
    expect(await screen.findByRole('button', { name: /work@acme\.com/i })).toBeTruthy();
  });
});
