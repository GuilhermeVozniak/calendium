import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Message, Thread } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ThreadView } from '@/components/app/thread-view';
import { resetShortcutHints } from '@/lib/shortcut-hints';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const actMock = vi.fn().mockResolvedValue(true);
const snoozeMock = vi.fn().mockResolvedValue(true);
const remindMock = vi.fn().mockResolvedValue(undefined);
const markOpenedMock = vi.fn().mockResolvedValue(undefined);
const unsubscribeMock = vi.fn();

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    message: (...args: unknown[]) => toastMessage(...args),
  },
}));

vi.mock('@/lib/use-mail', () => ({
  useMailActions: () => ({
    act: actMock,
    snooze: snoozeMock,
    remind: remindMock,
    markOpened: markOpenedMock,
    unsubscribe: unsubscribeMock,
  }),
  useThreadDetail: () => ({ data: { thread: THREAD, messages: MESSAGES, source: 'demo' }, isLoading: false }),
  runAiAsk: vi.fn(),
  runAiSummarize: vi.fn(),
}));

vi.mock('@/lib/use-identity', () => ({
  useSelfEmails: () => new Set(['me@calendium.app']),
}));

vi.mock('@/lib/use-instance', () => ({
  useInstance: () => ({ data: { features: { ai: false } } }),
}));

const openComposeMock = vi.fn();
vi.mock('@/components/app/compose', () => ({
  useCompose: () => ({ openCompose: openComposeMock }),
}));

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const THREAD: Thread = {
  id: 'thr_1',
  accountId: 'acc_1',
  subject: 'Renewal terms for FY27',
  snippet: 'legal cleared the redlines',
  participants: [{ name: 'Daniel Cho', email: 'daniel@northwind.com' }],
  labelIds: ['inbox'],
  split: 'important',
  messageCount: 1,
  unread: false,
  starred: false,
  lastMessageAt: new Date().toISOString(),
  openedAt: new Date().toISOString(),
  snoozedUntil: null,
  remindAt: null,
  unsubscribeMailto: null,
  unsubscribeUrl: 'http://example.com/unsub',
  unsubscribeOneClick: false,
};

const MESSAGES: Message[] = [
  {
    id: 'thr_1_m1',
    threadId: 'thr_1',
    accountId: 'acc_1',
    from: { name: 'Daniel Cho', email: 'daniel@northwind.com' },
    to: [{ name: 'You', email: 'me@calendium.app' }],
    cc: [],
    bcc: [],
    subject: 'Renewal terms for FY27',
    bodyHtml: '<p>legal cleared the redlines</p>',
    bodyText: 'legal cleared the redlines',
    attachments: [],
    sentAt: new Date().toISOString(),
    isDraft: false,
    openedAt: null,
  },
];

function renderThreadView(props: Partial<React.ComponentProps<typeof ThreadView>> = {}) {
  const onClose = vi.fn();
  render(<ThreadView threadId={THREAD.id} onClose={onClose} {...props} />);
  return { onClose };
}

beforeEach(() => {
  vi.clearAllMocks();
  toastSuccess.mockClear();
  toastError.mockClear();
  toastMessage.mockClear();
  actMock.mockResolvedValue(true);
  snoozeMock.mockResolvedValue(true);
  unsubscribeMock.mockClear();
  resetShortcutHints();
});

describe('ThreadView — onSnooze contract (mirrors onArchive)', () => {
  it('defers to onSnooze when provided instead of opening the internal dialog', async () => {
    const user = userEvent.setup();
    const onSnooze = vi.fn();
    renderThreadView({ onSnooze });

    await user.click(screen.getByRole('button', { name: 'Snooze' }));

    expect(onSnooze).toHaveBeenCalledOnce();
    expect(snoozeMock).not.toHaveBeenCalled();
    expect(screen.queryByText('Snooze until…')).not.toBeInTheDocument();
  });

  it('falls back to the internal dialog when onSnooze is absent', async () => {
    const user = userEvent.setup();
    renderThreadView();

    await user.click(screen.getByRole('button', { name: 'Snooze' }));

    expect(await screen.findByText('Snooze until…')).toBeInTheDocument();
  });

  it('internal fallback: gates the success toast on the resolved outcome (true)', async () => {
    const user = userEvent.setup();
    const { onClose } = renderThreadView();
    snoozeMock.mockResolvedValue(true);

    await user.click(screen.getByRole('button', { name: 'Snooze' }));
    await screen.findByText('Snooze until…');
    await user.click(screen.getByText('Tonight'));

    expect(snoozeMock).toHaveBeenCalledWith(THREAD.id, expect.any(String));
    expect(onClose).toHaveBeenCalledOnce();
    await vi.waitFor(() => expect(toastSuccess).toHaveBeenCalledWith(expect.stringMatching(/^Snoozed until/)));
  });

  it('internal fallback: suppresses the success toast when the mutation resolves false', async () => {
    const user = userEvent.setup();
    renderThreadView();
    snoozeMock.mockResolvedValue(false);

    await user.click(screen.getByRole('button', { name: 'Snooze' }));
    await screen.findByText('Snooze until…');
    await user.click(screen.getByText('Tonight'));

    expect(snoozeMock).toHaveBeenCalledOnce();
    // Let the resolved (false) promise's .then() run, then assert no success toast fired.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(toastSuccess).not.toHaveBeenCalled();
  });
});

describe('ThreadView — single unsubscribe (handleUnsubscribe)', () => {
  it('one_click method: shows success toast', async () => {
    const user = userEvent.setup();

    unsubscribeMock.mockResolvedValue({ method: 'one_click' });
    renderThreadView();

    // The unsubscribe button should be visible (THREAD now has unsubscribeUrl)
    const unsubButton = await screen.findByRole('button', { name: 'Unsubscribe' });
    await user.click(unsubButton);

    expect(unsubscribeMock).toHaveBeenCalledWith(THREAD.id);
    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith('Unsubscribed — the sender has been asked to stop');
    });
  });

  it('rejection: shows error toast only', async () => {
    const user = userEvent.setup();

    unsubscribeMock.mockRejectedValue(new Error('Network error'));
    renderThreadView();

    const unsubButton = await screen.findByRole('button', { name: 'Unsubscribe' });
    await user.click(unsubButton);

    expect(unsubscribeMock).toHaveBeenCalledWith(THREAD.id);
    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith('Could not unsubscribe.');
    });
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(toastMessage).not.toHaveBeenCalled();
  });

  it('link method with url: opens url in new tab and shows message toast', async () => {
    const user = userEvent.setup();

    const windowOpenMock = vi.spyOn(window, 'open').mockReturnValue(null);
    unsubscribeMock.mockResolvedValue({
      method: 'link',
      url: 'https://newsletter.example.com/unsub?token=xyz',
    });
    renderThreadView();

    const unsubButton = await screen.findByRole('button', { name: 'Unsubscribe' });
    await user.click(unsubButton);

    expect(unsubscribeMock).toHaveBeenCalledWith(THREAD.id);
    await waitFor(() => {
      expect(windowOpenMock).toHaveBeenCalledWith(
        'https://newsletter.example.com/unsub?token=xyz',
        '_blank',
        'noopener,noreferrer'
      );
    });
    expect(toastMessage).toHaveBeenCalledWith('Opened the unsubscribe page in a new tab');
    expect(toastSuccess).not.toHaveBeenCalledWith('Unsubscribed — the sender has been asked to stop');

    windowOpenMock.mockRestore();
  });
});

describe('ThreadView — shortcut teaching', () => {
  it('teaches Archive shortcut when header Archive button is clicked', async () => {
    const user = userEvent.setup();
    renderThreadView();

    await user.click(screen.getByRole('button', { name: 'Archive' }));

    expect(toastMessage).toHaveBeenCalledWith('Tip: press E to Archive');

    // Second click should NOT re-toast (dedup)
    toastMessage.mockClear();
    await user.click(screen.getByRole('button', { name: 'Archive' }));
    expect(toastMessage).not.toHaveBeenCalled();
  });

  it('teaches Snooze shortcut when header Snooze button is clicked', async () => {
    const user = userEvent.setup();
    renderThreadView();

    await user.click(screen.getByRole('button', { name: 'Snooze' }));

    expect(toastMessage).toHaveBeenCalledWith('Tip: press H to Snooze');
  });

  it('teaches Remind shortcut when header Remind button is clicked', async () => {
    const user = userEvent.setup();
    renderThreadView();

    await user.click(screen.getByRole('button', { name: 'Set reminder' }));

    expect(toastMessage).toHaveBeenCalledWith('Tip: press ⇧H to Set follow-up reminder');
  });
});
