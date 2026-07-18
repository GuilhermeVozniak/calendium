import type { OpenEvent } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks — following ask-sidebar.test.tsx's convention of mocking the data
// hook directly (opens-feed.tsx only imports `useOpensFeed` from
// '@/lib/use-mail', so overriding just that export is safe and keeps each
// scenario's loading/error/pages shape explicit per test).
// ---------------------------------------------------------------------------

const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock }),
}));

const useOpensFeedMock = vi.fn();
vi.mock('@/lib/use-mail', () => ({
  useOpensFeed: () => useOpensFeedMock(),
}));

const checkoutMutateMock = vi.fn();
vi.mock('@/components/app/paywall', () => ({
  useCheckoutMutation: () => ({ mutate: checkoutMutateMock, isPending: false }),
}));

import { OpensFeed } from '@/components/app/opens-feed';

function makeEvent(overrides: Partial<OpenEvent> = {}): OpenEvent {
  return {
    messageId: 'msg-1',
    threadId: 'thr-1',
    accountId: 'acc-1',
    subject: 'Renewal terms for FY27',
    recipients: [{ name: 'Daniel Cho', email: 'daniel.cho@northwind.com' }],
    openedAt: new Date(Date.now() - 60 * 60_000).toISOString(),
    sentAt: new Date(Date.now() - 2 * 60 * 60_000).toISOString(),
    ...overrides,
  };
}

interface QueryOverrides {
  pages?: OpenEvent[][];
  isLoading?: boolean;
  isError?: boolean;
  error?: unknown;
  hasNextPage?: boolean;
  isFetchingNextPage?: boolean;
  fetchNextPage?: () => void;
}

function mockQuery(overrides: QueryOverrides = {}) {
  const pages = overrides.pages ?? [[]];
  useOpensFeedMock.mockReturnValue({
    data: { pages: pages.map((items) => ({ items, nextCursor: null })) },
    isLoading: overrides.isLoading ?? false,
    isError: overrides.isError ?? false,
    error: overrides.error ?? null,
    hasNextPage: overrides.hasNextPage ?? false,
    isFetchingNextPage: overrides.isFetchingNextPage ?? false,
    fetchNextPage: overrides.fetchNextPage ?? vi.fn(),
  });
}

function renderPanel(open = true) {
  const onOpenChange = vi.fn();
  const utils = render(<OpensFeed open={open} onOpenChange={onOpenChange} />);
  return { ...utils, onOpenChange };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('OpensFeed', () => {
  it('renders nothing when closed', () => {
    mockQuery();
    const { container } = renderPanel(false);
    expect(container.querySelector('[data-testid="opens-feed"]')).not.toBeInTheDocument();
  });

  it('renders mocked pages newest-first', () => {
    const newest = makeEvent({ messageId: 'msg-newest', subject: 'Newest thread' });
    const oldest = makeEvent({ messageId: 'msg-oldest', subject: 'Oldest thread' });
    mockQuery({ pages: [[newest, oldest]] });
    renderPanel();

    const rows = screen.getAllByRole('button', { name: /opened/ });
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent('Newest thread');
    expect(rows[1]).toHaveTextContent('Oldest thread');
  });

  it('shows a Load more button that fetches the next cursor page, and does not refetch already-loaded pages', async () => {
    const fetchNextPage = vi.fn();
    const page1Event = makeEvent({ messageId: 'msg-page1' });
    mockQuery({ pages: [[page1Event]], hasNextPage: true, fetchNextPage });
    renderPanel();

    expect(screen.getByText(page1Event.subject, { exact: false })).toBeInTheDocument();
    const loadMore = screen.getByRole('button', { name: /Load more/ });
    await userEvent.click(loadMore);

    expect(fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it('does not show Load more once there is no next cursor', () => {
    mockQuery({ pages: [[makeEvent()]], hasNextPage: false });
    renderPanel();
    expect(screen.queryByRole('button', { name: /Load more/ })).not.toBeInTheDocument();
  });

  it('routes to /mail/{threadId} when a row is clicked', async () => {
    const event = makeEvent({ threadId: 'thr-42' });
    mockQuery({ pages: [[event]] });
    renderPanel();

    await userEvent.click(screen.getByRole('button', { name: /opened/ }));
    expect(pushMock).toHaveBeenCalledWith('/mail/thr-42');
  });

  it('shows the honest empty state when there are no opens', () => {
    mockQuery({ pages: [[]] });
    renderPanel();
    expect(
      screen.getByText('No opens yet — read statuses appear as recipients open your mail.')
    ).toBeInTheDocument();
  });

  it('shows an upgrade prompt instead of a generic error on a 402', () => {
    mockQuery({ isError: true, error: new ApiRequestError(402, 'payment_required', 'no active subscription') });
    renderPanel();

    expect(screen.getByText('Recent Opens is a paid feature')).toBeInTheDocument();
    const subscribeButton = screen.getByRole('button', { name: /Subscribe/ });
    expect(subscribeButton).toBeInTheDocument();
    expect(screen.queryByText('Could not load recent opens.')).not.toBeInTheDocument();
  });

  it('shows a generic error state for a non-402 failure', () => {
    mockQuery({ isError: true, error: new ApiRequestError(500, 'unknown', 'server error') });
    renderPanel();
    expect(screen.getByText('Could not load recent opens.')).toBeInTheDocument();
    expect(screen.queryByText('Recent Opens is a paid feature')).not.toBeInTheDocument();
  });

  it('closes via the close button', async () => {
    mockQuery();
    const { onOpenChange } = renderPanel();
    await userEvent.click(screen.getByLabelText('Close recent opens'));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
