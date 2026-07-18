import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const runProposeEventMock = vi.fn();
const aiErrorMessageMock = vi.fn((..._args: unknown[]) => 'AI is unavailable right now. Please try again.');
vi.mock('@/lib/use-mail', () => ({
  runProposeEvent: (...args: unknown[]) => runProposeEventMock(...args),
  aiErrorMessage: (...args: unknown[]) => aiErrorMessageMock(...args),
}));

const fetchCalendarsMock = vi.fn().mockResolvedValue([]);
vi.mock('@/lib/calendar-data', () => ({
  fetchCalendars: () => fetchCalendarsMock(),
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...args: unknown[]) => toastError(...args) } }));

// Isolate ProposeEventDialog's own logic (fetch -> hand off to EventDialog as
// `defaults`) from EventDialog's own large internals.
vi.mock('@/components/app/event-dialog', () => ({
  EventDialog: ({ open, defaults }: { open: boolean; defaults: Record<string, unknown> | null }) =>
    open ? <div data-testid="event-dialog">{JSON.stringify(defaults)}</div> : null,
}));

import { ProposeEventDialog } from './propose-event-dialog';

function renderDialog(props: { threadId: string | null; open: boolean; onOpenChange: (open: boolean) => void }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <ProposeEventDialog {...props} />
    </QueryClientProvider>
  );
}

describe('ProposeEventDialog', () => {
  beforeEach(() => {
    runProposeEventMock.mockReset();
    toastError.mockReset();
  });

  it('renders nothing when closed', () => {
    renderDialog({ threadId: 'thr_1', open: false, onOpenChange: () => {} });
    expect(screen.queryByTestId('event-dialog')).not.toBeInTheDocument();
  });

  it('shows a loading state, then hands the proposal to EventDialog as defaults', async () => {
    runProposeEventMock.mockResolvedValue({
      title: 'Follow-up call',
      attendees: ['a@example.com'],
      start: '2026-08-01T10:00:00.000Z',
      end: '2026-08-01T10:30:00.000Z',
      source: 'api',
    });
    renderDialog({ threadId: 'thr_1', open: true, onOpenChange: () => {} });

    expect(screen.getByText('Proposing an event from this thread…')).toBeInTheDocument();
    expect(runProposeEventMock).toHaveBeenCalledWith('thr_1');

    const eventDialog = await screen.findByTestId('event-dialog');
    expect(eventDialog.textContent).toContain('Follow-up call');
    expect(eventDialog.textContent).toContain('a@example.com');
  });

  it('shows a friendly error toast and closes instead of fabricating a proposal', async () => {
    runProposeEventMock.mockRejectedValue(new Error('boom'));
    const onOpenChange = vi.fn();
    renderDialog({ threadId: 'thr_1', open: true, onOpenChange });

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('AI is unavailable right now. Please try again.')
    );
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(screen.queryByTestId('event-dialog')).not.toBeInTheDocument();
  });
});
