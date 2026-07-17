import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Message, Thread } from '@calendium/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ThreadView } from '@/components/app/thread-view';

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const actMock = vi.fn().mockResolvedValue(true);
const snoozeMock = vi.fn().mockResolvedValue(true);
const remindMock = vi.fn().mockResolvedValue(undefined);
const markOpenedMock = vi.fn().mockResolvedValue(undefined);

const toastSuccess = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: vi.fn(),
  },
}));

vi.mock('@/lib/use-mail', () => ({
  useMailActions: () => ({
    act: actMock,
    snooze: snoozeMock,
    remind: remindMock,
    markOpened: markOpenedMock,
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
  unsubscribeUrl: null,
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
  actMock.mockResolvedValue(true);
  snoozeMock.mockResolvedValue(true);
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
