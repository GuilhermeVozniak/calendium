import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Thread } from '@calendium/shared';

import { ThreadSummary } from './thread-summary';

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

describe('ThreadSummary', () => {
  it('shows a skeleton shimmer while the summary has not been generated yet', () => {
    render(<ThreadSummary thread={BASE_THREAD} />);
    expect(screen.getByTestId('thread-summary-skeleton')).toBeInTheDocument();
  });

  it('renders the summary text once the thread carries one', () => {
    render(<ThreadSummary thread={{ ...BASE_THREAD, summary: 'A concise recap of the thread.' }} />);
    expect(screen.queryByTestId('thread-summary-skeleton')).not.toBeInTheDocument();
    expect(screen.getByText('A concise recap of the thread.')).toBeInTheDocument();
  });
});
