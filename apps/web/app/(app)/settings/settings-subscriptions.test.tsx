import type { CalendarSubscription } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock only the API boundary (@/lib/api) so the real settings-data.ts
// wrappers run for real — mirrors settings-snippets.test.tsx.
const listSubsMock = vi.fn();
const createSubMock = vi.fn();
const updateSubMock = vi.fn();
const deleteSubMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listCalendarSubscriptions: (...args: unknown[]) => listSubsMock(...args),
    createCalendarSubscription: (...args: unknown[]) => createSubMock(...args),
    updateCalendarSubscription: (...args: unknown[]) => updateSubMock(...args),
    deleteCalendarSubscription: (...args: unknown[]) => deleteSubMock(...args),
  }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    info: (...args: unknown[]) => vi.fn()(...args),
  },
}));

import { SubscriptionsSection } from './settings-page';

const HOLIDAYS: CalendarSubscription = {
  id: 'sub1',
  url: 'https://example.com/holidays.ics',
  name: 'US Holidays',
  color: '#8b5cf6',
  isVisible: true,
  lastFetchedAt: '2026-07-19T11:00:00Z',
  lastError: null,
  createdAt: '2026-07-01T00:00:00Z',
};

const BROKEN: CalendarSubscription = {
  ...HOLIDAYS,
  id: 'sub2',
  url: 'https://example.com/broken.ics',
  name: 'Broken feed',
  lastError: 'icsfeed: feed answered status 500',
};

function renderSection() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <SubscriptionsSection />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listSubsMock.mockResolvedValue([HOLIDAYS, BROKEN]);
});

describe('SubscriptionsSection (M2.8 Task 15)', () => {
  it('lists feeds and marks a failing one honestly', async () => {
    renderSection();
    expect(await screen.findByText('US Holidays')).toBeInTheDocument();
    expect(await screen.findByText('Broken feed')).toBeInTheDocument();
    // The failing feed is flagged; stale data must look stale.
    expect(await screen.findByText('Fetch failed')).toBeInTheDocument();
  });

  it('subscribes to a feed with the chosen color', async () => {
    createSubMock.mockResolvedValue({ ...HOLIDAYS, id: 'sub3', name: 'Team cal' });
    const user = userEvent.setup();
    renderSection();

    await user.type(
      await screen.findByLabelText('ICS feed URL'),
      'https://example.com/team.ics'
    );
    await user.click(screen.getByRole('button', { name: 'Color #10b981' }));
    await user.click(screen.getByRole('button', { name: /Add feed/ }));

    await waitFor(() => expect(createSubMock).toHaveBeenCalledTimes(1));
    expect(createSubMock.mock.calls[0]![0]).toEqual({
      url: 'https://example.com/team.ics',
      name: undefined,
      color: '#10b981',
    });
    expect(toastSuccess).toHaveBeenCalledWith('Subscribed to Team cal');
  });

  it('surfaces the server 422 message inline when the feed is unfetchable', async () => {
    createSubMock.mockRejectedValue(
      new ApiRequestError(422, 'unprocessable', 'The calendar feed could not be fetched or parsed.')
    );
    const user = userEvent.setup();
    renderSection();

    await user.type(
      await screen.findByLabelText('ICS feed URL'),
      'https://example.com/broken.ics'
    );
    await user.click(screen.getByRole('button', { name: /Add feed/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The calendar feed could not be fetched or parsed.'
    );
  });

  it('toggles visibility through PATCH', async () => {
    updateSubMock.mockResolvedValue({ ...HOLIDAYS, isVisible: false });
    const user = userEvent.setup();
    renderSection();

    await user.click(
      await screen.findByRole('switch', { name: 'Show US Holidays on the calendar' })
    );
    await waitFor(() => expect(updateSubMock).toHaveBeenCalledWith('sub1', { isVisible: false }));
  });

  it('removes a feed', async () => {
    deleteSubMock.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderSection();

    await user.click(await screen.findByRole('button', { name: 'Remove feed US Holidays' }));
    await waitFor(() => expect(deleteSubMock).toHaveBeenCalledWith('sub1'));
    expect(toastSuccess).toHaveBeenCalledWith('Feed removed');
  });
});
