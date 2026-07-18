import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Thread } from '@calendium/shared';

const runInstantRepliesMock = vi.fn();
const aiErrorMessageMock = vi.fn((..._args: unknown[]) => 'AI is unavailable right now. Please try again.');
vi.mock('@/lib/use-mail', () => ({
  runInstantReplies: (...args: unknown[]) => runInstantRepliesMock(...args),
  aiErrorMessage: (...args: unknown[]) => aiErrorMessageMock(...args),
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { error: (...args: unknown[]) => toastError(...args) },
}));

import { InstantReplies } from './instant-replies';

const BASE_THREAD: Thread = {
  id: 'thr_1',
  accountId: 'acc_1',
  subject: 'Test thread',
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

describe('InstantReplies', () => {
  beforeEach(() => {
    runInstantRepliesMock.mockReset();
    toastError.mockReset();
  });

  it('renders cached thread.instantReplies without calling getInstantReplies', async () => {
    const onPick = vi.fn();
    render(
      <InstantReplies
        thread={{ ...BASE_THREAD, instantReplies: ['Sounds good', 'Will do', 'Not now'] }}
        onPick={onPick}
      />
    );
    expect(await screen.findByText('Sounds good')).toBeInTheDocument();
    expect(runInstantRepliesMock).not.toHaveBeenCalled();
  });

  it('calls getInstantReplies (via runInstantReplies) when the thread has none cached', async () => {
    runInstantRepliesMock.mockResolvedValue({ replies: ['A reply'], source: 'api' });
    render(<InstantReplies thread={BASE_THREAD} onPick={() => {}} />);
    await waitFor(() => expect(runInstantRepliesMock).toHaveBeenCalledWith('thr_1'));
    expect(await screen.findByText('A reply')).toBeInTheDocument();
  });

  it('opens the composer prefilled with the reply on click', async () => {
    const onPick = vi.fn();
    render(
      <InstantReplies
        thread={{ ...BASE_THREAD, instantReplies: ['Sounds good', 'Will do', 'Not now'] }}
        onPick={onPick}
      />
    );
    await userEvent.click(await screen.findByText('Will do'));
    expect(onPick).toHaveBeenCalledWith('Will do');
  });

  it('picks a reply via the 1/2/3 keys while the chip row has focus', async () => {
    const onPick = vi.fn();
    render(
      <InstantReplies
        thread={{ ...BASE_THREAD, instantReplies: ['First', 'Second', 'Third'] }}
        onPick={onPick}
      />
    );
    // Focus lands on a chip button (native tab stop); the keydown handler on
    // the wrapping row catches '2' via bubbling.
    (await screen.findByRole('button', { name: /First/ })).focus();
    await userEvent.keyboard('2');
    expect(onPick).toHaveBeenCalledWith('Second');
  });

  it('shows a friendly error toast and renders nothing when the fetch fails', async () => {
    runInstantRepliesMock.mockRejectedValue(new Error('boom'));
    render(<InstantReplies thread={BASE_THREAD} onPick={() => {}} />);
    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
