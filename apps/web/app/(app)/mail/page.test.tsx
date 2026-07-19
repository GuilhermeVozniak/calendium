import * as React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Thread } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import MailPage from './page';
import { dispatchMailCommand } from '@/lib/mail-utils';

// ---------------------------------------------------------------------------
// Reactive test doubles
// ---------------------------------------------------------------------------
// The page reads its open-thread / split / search state from
// next/navigation's useSearchParams and mutates it via router.replace, and
// reads its thread list from lib/use-mail's useThreadList. Both need to be
// genuinely reactive (a re-render when the underlying value changes) for the
// auto-advance / toast-gating assertions below to observe anything, so both
// mocks are tiny external stores wired through useState + a subscriber set —
// mirroring what next/navigation and TanStack Query actually do.

let searchQs = '';
const searchListeners = new Set<() => void>();
function setSearchQs(qs: string) {
  searchQs = qs;
  for (const l of searchListeners) l();
}
function useSearchParamsImpl() {
  const [, force] = React.useState(0);
  React.useEffect(() => {
    const cb = () => force((n) => n + 1);
    searchListeners.add(cb);
    return () => {
      searchListeners.delete(cb);
    };
  }, []);
  return new URLSearchParams(searchQs);
}
const replaceMock = vi.fn((url: string) => {
  const qIndex = url.indexOf('?');
  setSearchQs(qIndex >= 0 ? url.slice(qIndex + 1) : '');
});
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: (...args: unknown[]) => replaceMock(...(args as [string])) }),
  useSearchParams: () => useSearchParamsImpl(),
}));

let currentThreads: Thread[] = [];
const threadsListeners = new Set<() => void>();
function setCurrentThreads(next: Thread[]) {
  currentThreads = next;
  for (const l of threadsListeners) l();
}
function useThreadListImpl() {
  const [, force] = React.useState(0);
  React.useEffect(() => {
    const cb = () => force((n) => n + 1);
    threadsListeners.add(cb);
    return () => {
      threadsListeners.delete(cb);
    };
  }, []);
  return {
    data: { items: currentThreads, source: 'api' },
    isLoading: false,
    isError: false,
    fetchNextPage: vi.fn(),
    hasNextPage: false,
    isFetchingNextPage: false,
  };
}

/** Controls whether the mocked act/snooze mutations resolve true or false. */
let mutationResolvesTo = true;

const actMock = vi.fn((id: string, action: string) => {
  const removes = action === 'archive' || action === 'trash' || action === 'spam';
  if (!removes) return Promise.resolve(mutationResolvesTo);
  const before = currentThreads;
  setCurrentThreads(currentThreads.filter((t) => t.id !== id));
  return Promise.resolve(mutationResolvesTo).then((ok) => {
    if (!ok) setCurrentThreads(before); // mirrors runOptimistic's cache revert on failure
    return ok;
  });
});
const snoozeMock = vi.fn((id: string) => {
  const before = currentThreads;
  setCurrentThreads(currentThreads.filter((t) => t.id !== id));
  return Promise.resolve(mutationResolvesTo).then((ok) => {
    if (!ok) setCurrentThreads(before);
    return ok;
  });
});
const remindMock = vi.fn().mockResolvedValue(undefined);
const undoLastMock = vi.fn().mockResolvedValue(false);
const bulkActMock = vi.fn().mockResolvedValue({ failedCount: 0 });
const setLabelMock = vi.fn().mockResolvedValue(true);
const markOpenedMock = vi.fn().mockResolvedValue(undefined);
const unsubscribeMock = vi.fn().mockResolvedValue({ method: 'one_click' });

vi.mock('@/lib/use-mail', () => ({
  useThreadList: (...args: unknown[]) => useThreadListImpl(...(args as [])),
  useThreadDetail: (threadId: string | null) => {
    const thread = currentThreads.find((t) => t.id === threadId) ?? null;
    return { data: thread ? { thread, messages: [], source: 'demo' } : null, isLoading: false };
  },
  useMailActions: () => ({
    act: actMock,
    snooze: snoozeMock,
    remind: remindMock,
    undoLast: undoLastMock,
    bulkAct: bulkActMock,
    setLabel: setLabelMock,
    markOpened: markOpenedMock,
    unsubscribe: unsubscribeMock,
  }),
  useReactToMessage: () => ({ react: vi.fn(), removeReaction: vi.fn() }),
  useLabels: () => ({ data: { labels: [{ id: 'lbl1', accountId: 'acc_1', name: 'Updates', kind: 'user', color: null }] }, isLoading: false }),
  useDrafts: () => ({ data: { drafts: [] }, isLoading: false, isError: false }),
  useDraftActions: () => ({ remove: vi.fn() }),
  runAiAsk: vi.fn(),
  runAiSummarize: vi.fn(),
}));

vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => new Set<string>(),
}));

// Prefetch hooks need a QueryClientProvider and fire debounced network warms —
// both irrelevant to what this file asserts (covered by use-prefetch.test.tsx).
vi.mock('@/lib/use-prefetch', () => ({
  useThreadHoverPrefetch: () => ({ onHoverStart: () => {}, onHoverEnd: () => {} }),
  usePrefetchNeighbors: () => {},
  useNextPagePrefetch: () => {},
}));

vi.mock('@/lib/use-instance', () => ({
  useInstance: () => ({ data: { features: { ai: false } } }),
}));

vi.mock('@/lib/prefs-data', () => ({
  usePrefs: () => ({ data: { prefs: { splitOrder: [] } }, isLoading: false }),
  useUpdatePrefs: () => vi.fn().mockResolvedValue(undefined),
}));

const openComposeMock = vi.fn();
vi.mock('@/components/app/compose', () => ({
  useCompose: () => ({ openCompose: openComposeMock }),
  htmlToText: (html: string) => html,
}));

// CalendarPeek owns its own data-fetching (React Query) and internal UI —
// covered by calendar-peek.test.tsx. Here we only care that the mail page
// wires `open`/`onOpenChange` correctly, so stub it down to a marker that
// exposes the open state and lets a click flip it (mirroring the real
// component's close button) without pulling in QueryClientProvider/EventDialog.
const readStoredCalendarPeekOpenMock = vi.fn(() => false);
vi.mock('@/components/app/calendar-peek', () => ({
  readStoredCalendarPeekOpen: () => readStoredCalendarPeekOpenMock(),
  CalendarPeek: ({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) =>
    open ? (
      <div data-testid="calendar-peek-stub">
        <button type="button" onClick={() => onOpenChange(false)}>
          Close calendar peek
        </button>
      </div>
    ) : null,
}));

// OpensFeed owns its own data-fetching (useInfiniteQuery) — covered by
// opens-feed.test.tsx. Here we only care that the mail page wires
// open/onOpenChange correctly, same reasoning as the CalendarPeek stub above.
vi.mock('@/components/app/opens-feed', () => ({
  OpensFeed: ({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) =>
    open ? (
      <div data-testid="opens-feed-stub">
        <button type="button" onClick={() => onOpenChange(false)}>
          Close opens feed
        </button>
      </div>
    ) : null,
}));

// ProposeEventDialog owns its own data-fetching (React Query + EventDialog's
// internals) — covered by propose-event-dialog.test.tsx. Here we only care
// that the mail page wires threadId/open/onOpenChange correctly.
vi.mock('@/components/mail/propose-event-dialog', () => ({
  ProposeEventDialog: ({ open }: { open: boolean }) =>
    open ? <div data-testid="propose-event-dialog-stub" /> : null,
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastWarning = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    warning: (...args: unknown[]) => toastWarning(...args),
    message: (...args: unknown[]) => toastMessage(...args),
  },
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

function makeThread(id: string, subject: string, hoursAgo: number): Thread {
  return {
    id,
    accountId: 'acc_1',
    subject,
    snippet: subject,
    participants: [{ name: 'Someone', email: `${id}@example.com` }],
    labelIds: ['inbox'],
    split: 'important',
    messageCount: 1,
    unread: false,
    starred: false,
    lastMessageAt: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
    openedAt: new Date().toISOString(),
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
  };
}

const T1 = makeThread('t1', 'First conversation', 1);
const T2 = makeThread('t2', 'Second conversation', 2);
const T3 = makeThread('t3', 'Third conversation (last)', 3);

beforeEach(() => {
  vi.clearAllMocks();
  mutationResolvesTo = true;
  searchQs = '';
  setCurrentThreads([T1, T2, T3]);
});

describe('Mail page — auto-advance edge positions (finding 3)', () => {
  it('archiving the LAST thread in the list advances focus to the previous one', async () => {
    const user = userEvent.setup();
    render(<MailPage />);

    await user.click(await screen.findByRole('button', { name: /Third conversation \(last\)/ }));
    await screen.findByRole('heading', { name: 'Third conversation (last)' });

    await user.click(screen.getByRole('button', { name: 'Archive' }));

    // T3 leaves the list…
    expect(screen.queryByRole('button', { name: /Third conversation \(last\)/ })).not.toBeInTheDocument();
    // …and focus/URL advance to T2, the one above it (no thread below it).
    await screen.findByRole('heading', { name: 'Second conversation' });
    expect(new URLSearchParams(searchQs).get('t')).toBe('t2');
  });

  it('archiving the ONLY thread closes the pane and clears selection to the empty state', async () => {
    setCurrentThreads([T1]);
    const user = userEvent.setup();
    render(<MailPage />);

    await user.click(await screen.findByRole('button', { name: /First conversation/ }));
    await screen.findByRole('heading', { name: 'First conversation' });

    await user.click(screen.getByRole('button', { name: 'Archive' }));

    // Pane closes (ThreadView unmounts)…
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'First conversation' })).not.toBeInTheDocument()
    );
    // …selection clears to the empty state, and the URL drops `t`.
    expect(await screen.findByText("You're at Inbox Zero")).toBeInTheDocument();
    expect(new URLSearchParams(searchQs).get('t')).toBeNull();
  });
});

describe('Mail page — trash (#) coverage (finding 4)', () => {
  it('# trashes the focused thread, advances, and shows Deleted with Undo on success', async () => {
    const user = userEvent.setup();
    render(<MailPage />);

    const row = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row);
    await user.keyboard('#');

    expect(actMock).toHaveBeenCalledWith('t1', 'trash');
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Deleted', expect.objectContaining({
      action: expect.objectContaining({ label: 'Undo' }),
    })));
    expect(screen.queryByRole('button', { name: /First conversation/ })).not.toBeInTheDocument();
  });
});

describe('Mail page — success toasts gated on mutation outcome (finding 2)', () => {
  it('archive: shows "Archived" only when the mutation resolves true', async () => {
    const user = userEvent.setup();
    render(<MailPage />);

    const row = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row);
    await user.keyboard('e');

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Archived', expect.anything()));
  });

  it('archive: suppresses the toast when the mutation resolves false', async () => {
    mutationResolvesTo = false;
    const user = userEvent.setup();
    render(<MailPage />);

    const row = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row);
    await user.keyboard('e');

    expect(actMock).toHaveBeenCalledWith('t1', 'archive');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalledWith('Archived', expect.anything());
  });

  it('trash: suppresses the "Deleted" toast when the mutation resolves false', async () => {
    mutationResolvesTo = false;
    const user = userEvent.setup();
    render(<MailPage />);

    const row = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row);
    await user.keyboard('#');

    expect(actMock).toHaveBeenCalledWith('t1', 'trash');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalledWith('Deleted', expect.anything());
  });

  it('snooze (page dialog, via ThreadView onSnooze): shows the toast only when the mutation resolves true', async () => {
    const user = userEvent.setup();
    render(<MailPage />);

    await user.click(await screen.findByRole('button', { name: /First conversation/ }));
    await screen.findByRole('heading', { name: 'First conversation' });
    await user.click(screen.getByRole('button', { name: 'Snooze' }));

    const dialog = await screen.findByText('Snooze until…');
    await user.click(within(dialog.closest('[role="dialog"]') as HTMLElement).getByText('Tonight'));

    expect(snoozeMock).toHaveBeenCalledWith('t1', expect.any(String));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(expect.stringMatching(/^Snoozed until/), expect.anything())
    );
  });

  it('snooze (page dialog): suppresses the toast when the mutation resolves false', async () => {
    const user = userEvent.setup();
    render(<MailPage />);

    await user.click(await screen.findByRole('button', { name: /First conversation/ }));
    await screen.findByRole('heading', { name: 'First conversation' });
    await user.click(screen.getByRole('button', { name: 'Snooze' }));

    const dialog = await screen.findByText('Snooze until…');
    mutationResolvesTo = false;
    await user.click(within(dialog.closest('[role="dialog"]') as HTMLElement).getByText('Tonight'));

    expect(snoozeMock).toHaveBeenCalledWith('t1', expect.any(String));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalledWith(expect.stringMatching(/^Snoozed until/), expect.anything());
  });
});

describe('Mail page — bulk unsubscribe partitions by method (finding 1)', () => {
  async function selectFirstTwo(user: ReturnType<typeof userEvent.setup>) {
    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('{Shift>}j{/Shift}');
    await screen.findByText('2 selected');
  }

  it('one_click/mailto results are archived and counted as unsubscribed', async () => {
    const T1u = { ...T1, unsubscribeUrl: 'http://example.com/unsub' };
    const T2u = { ...T2, unsubscribeUrl: 'http://example.com/unsub' };
    setCurrentThreads([T1u, T2u, T3]);
    unsubscribeMock.mockImplementationOnce(() => Promise.resolve({ method: 'one_click' }));
    unsubscribeMock.mockImplementationOnce(() => Promise.resolve({ method: 'mailto' }));

    const user = userEvent.setup();
    render(<MailPage />);
    await selectFirstTwo(user);

    await user.click(screen.getByRole('button', { name: 'Unsubscribe' }));

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringContaining('Unsubscribed from 2 senders'),
        expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
      )
    );
    expect(bulkActMock).toHaveBeenCalledWith(['t1', 't2'], 'archive');
    expect(toastMessage).not.toHaveBeenCalled();
  });

  it('a fulfilled link-method result is NOT archived, NOT counted as unsubscribed, and is surfaced distinctly', async () => {
    // A resolved {method:'link', url} did nothing server-side — unlike the
    // single-thread path (thread-view.tsx), bulk never opens the url, so it
    // must not be treated as a completed unsubscribe.
    const T1u = { ...T1, unsubscribeUrl: 'http://example.com/unsub' };
    const T2u = { ...T2, unsubscribeUrl: 'http://example.com/unsub' };
    setCurrentThreads([T1u, T2u, T3]);
    unsubscribeMock.mockImplementationOnce(() =>
      Promise.resolve({ method: 'link', url: 'http://example.com/unsub' })
    );
    unsubscribeMock.mockImplementationOnce(() => Promise.resolve({ method: 'one_click' }));

    const user = userEvent.setup();
    render(<MailPage />);
    await selectFirstTwo(user);

    await user.click(screen.getByRole('button', { name: 'Unsubscribe' }));

    // Only the one_click id (t2) is archived and counted as unsubscribed.
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringContaining('Unsubscribed from 1 sender'),
        expect.anything()
      )
    );
    expect(bulkActMock).toHaveBeenCalledWith(['t2'], 'archive');
    expect(bulkActMock).not.toHaveBeenCalledWith(expect.arrayContaining(['t1']), 'archive');
    expect(toastSuccess).not.toHaveBeenCalledWith(
      expect.stringContaining('Unsubscribed from 2'),
      expect.anything()
    );

    // The link-method sender is surfaced distinctly (manual follow-up needed),
    // never folded into the success count.
    expect(toastMessage).toHaveBeenCalledWith(expect.stringContaining('1 sender'));
  });

  it('all link-method results: nothing archived, nothing counted as unsubscribed', async () => {
    const T1u = { ...T1, unsubscribeUrl: 'http://example.com/unsub' };
    const T2u = { ...T2, unsubscribeUrl: 'http://example.com/unsub' };
    setCurrentThreads([T1u, T2u, T3]);
    unsubscribeMock.mockImplementation(() =>
      Promise.resolve({ method: 'link', url: 'http://example.com/unsub' })
    );

    const user = userEvent.setup();
    render(<MailPage />);
    await selectFirstTwo(user);

    await user.click(screen.getByRole('button', { name: 'Unsubscribe' }));

    await waitFor(() => expect(toastMessage).toHaveBeenCalledWith(expect.stringContaining('2 sender')));
    expect(bulkActMock).not.toHaveBeenCalled();
    expect(toastSuccess).not.toHaveBeenCalledWith(
      expect.stringContaining('Unsubscribed'),
      expect.anything()
    );
  });

  it('rejected results are counted as failed, distinct from link-method', async () => {
    const T1u = { ...T1, unsubscribeUrl: 'http://example.com/unsub' };
    const T2u = { ...T2, unsubscribeUrl: 'http://example.com/unsub' };
    setCurrentThreads([T1u, T2u, T3]);
    unsubscribeMock.mockImplementationOnce(() => Promise.reject(new Error('failed')));
    unsubscribeMock.mockImplementationOnce(() => Promise.resolve({ method: 'one_click' }));

    const user = userEvent.setup();
    render(<MailPage />);
    await selectFirstTwo(user);

    await user.click(screen.getByRole('button', { name: 'Unsubscribe' }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(expect.stringContaining("Couldn't unsubscribe from 1"))
    );
    expect(bulkActMock).toHaveBeenCalledWith(['t2'], 'archive');
  });
});

describe('Mail page — bulk/label mutations gate their success toast on resolution (finding 4)', () => {
  it('bulk archive: no success toast until bulkAct resolves', async () => {
    let resolveBulk: ((value: { failedCount: number }) => void) | undefined;
    bulkActMock.mockImplementationOnce(
      () => new Promise((resolve) => { resolveBulk = resolve; })
    );

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('e');

    expect(bulkActMock).toHaveBeenCalledWith(['t1'], 'archive');
    expect(toastSuccess).not.toHaveBeenCalled();

    resolveBulk?.({ failedCount: 0 });
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringContaining('Archived 1 conversation'),
        expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
      )
    );
  });

  it('bulk archive: partial failure reflects the succeeded count only', async () => {
    bulkActMock.mockResolvedValueOnce({ failedCount: 1 });

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('{Shift>}j{/Shift}');
    await screen.findByText('2 selected');
    await user.keyboard('e');

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringContaining('Archived 1 conversation'),
        expect.anything()
      )
    );
    expect(toastSuccess).not.toHaveBeenCalledWith(expect.stringContaining('Archived 2'), expect.anything());
  });

  it('bulk archive: total failure shows no success toast', async () => {
    bulkActMock.mockResolvedValueOnce({ failedCount: 1 });

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('e');

    await waitFor(() => expect(bulkActMock).toHaveBeenCalledWith(['t1'], 'archive'));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  it('bulk mark read: reflects the succeeded count only', async () => {
    bulkActMock.mockResolvedValueOnce({ failedCount: 1 });

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('{Shift>}j{/Shift}');
    await screen.findByText('2 selected');
    await user.keyboard('{Shift>}i{/Shift}');

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Marked 1 read'));
  });

  it('bulk star: reflects the succeeded count only', async () => {
    bulkActMock.mockResolvedValueOnce({ failedCount: 1 });

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('{Shift>}j{/Shift}');
    await screen.findByText('2 selected');
    await user.keyboard('s');

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(expect.stringContaining('Starred 1 conversation'))
    );
  });

  it('bulk label: reflects the succeeded count only', async () => {
    bulkActMock.mockResolvedValueOnce({ failedCount: 1 });

    const user = userEvent.setup();
    render(<MailPage />);

    const row1 = await screen.findByRole('button', { name: /First conversation/ });
    await user.hover(row1);
    await user.keyboard('x');
    await user.keyboard('{Shift>}j{/Shift}');
    await screen.findByText('2 selected');
    await user.keyboard('l');

    await screen.findByPlaceholderText('Label as…');
    await user.click(screen.getByText('Updates'));

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(expect.stringContaining('Labeled 1 conversation'))
    );
  });

  it('single-thread label: no success toast when setLabel resolves false', async () => {
    setLabelMock.mockResolvedValueOnce(false);

    const user = userEvent.setup();
    render(<MailPage />);

    await user.click(await screen.findByRole('button', { name: /First conversation/ }));
    await screen.findByRole('heading', { name: 'First conversation' });
    await user.keyboard('l');

    await screen.findByPlaceholderText('Label as…');
    await user.click(screen.getByText('Updates'));

    await waitFor(() => expect(setLabelMock).toHaveBeenCalledWith('t1', 'lbl1', true));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalledWith(expect.stringContaining('Labeled'));
  });
});

/** mod+shift+k, platform-agnostic — set both ctrlKey and metaKey so the
 *  assertion holds regardless of the jsdom-reported platform (mirrors
 *  lib/shortcuts.test.ts's own "mod" convention). */
function pressTogglePeek() {
  fireEvent.keyDown(document.body, { key: 'k', shiftKey: true, ctrlKey: true, metaKey: true });
}

describe('Mail page — calendar peek toggle', () => {
  it('mod+shift+k opens the calendar peek panel', async () => {
    render(<MailPage />);
    await screen.findByRole('button', { name: /First conversation/ });

    expect(screen.queryByTestId('calendar-peek-stub')).not.toBeInTheDocument();
    pressTogglePeek();
    expect(await screen.findByTestId('calendar-peek-stub')).toBeInTheDocument();

    // Pressing it again closes the panel — a plain toggle, not a one-way open.
    pressTogglePeek();
    await waitFor(() => expect(screen.queryByTestId('calendar-peek-stub')).not.toBeInTheDocument());
  });

  it('bare k (previous conversation) does not open the peek — mod distinguishes the bindings', async () => {
    const user = userEvent.setup();
    render(<MailPage />);
    const row = await screen.findByRole('button', { name: /Second conversation/ });
    await user.hover(row);

    await user.keyboard('k');
    expect(screen.queryByTestId('calendar-peek-stub')).not.toBeInTheDocument();
  });

  it('the "Toggle calendar peek" MailCommand opens the panel (palette bridge)', async () => {
    render(<MailPage />);
    await screen.findByRole('button', { name: /First conversation/ });

    expect(screen.queryByTestId('calendar-peek-stub')).not.toBeInTheDocument();
    dispatchMailCommand('toggle-calendar-peek');
    expect(await screen.findByTestId('calendar-peek-stub')).toBeInTheDocument();
  });

  it('hydrates the initial open state from localStorage on mount', async () => {
    readStoredCalendarPeekOpenMock.mockReturnValueOnce(true);
    render(<MailPage />);
    expect(await screen.findByTestId('calendar-peek-stub')).toBeInTheDocument();
  });

  it("the panel's own close control flips peekOpen back through the page", async () => {
    const user = userEvent.setup();
    render(<MailPage />);
    await screen.findByRole('button', { name: /First conversation/ });

    pressTogglePeek();
    await user.click(await screen.findByRole('button', { name: 'Close calendar peek' }));
    await waitFor(() => expect(screen.queryByTestId('calendar-peek-stub')).not.toBeInTheDocument());
  });
});
