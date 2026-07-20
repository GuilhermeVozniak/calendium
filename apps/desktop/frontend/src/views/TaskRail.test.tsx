import type { Task } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TaskRail } from './TaskRail';

// Same isolation approach as CalendarView.test.tsx: '@/lib/api' is replaced
// outright so the jsdom default (isDemoMode() false → real api branch) runs
// against test-controlled fns; orMock is a pass-through to the real branch.
const fixtures = vi.hoisted(() => ({ tasks: [] as Task[] }));

vi.mock('@/lib/api', () => ({
  api: {
    listTasks: vi.fn(async () => fixtures.tasks),
    completeTask: vi.fn(),
    reopenTask: vi.fn(),
    createTask: vi.fn(),
  },
  orMock: async (real: () => unknown) => real(),
}));

const toastMock = vi.fn();
vi.mock('@/lib/toast', () => ({
  toast: (...args: unknown[]) => toastMock(...args),
  errorMessage: () => 'error',
}));

function makeTask(overrides: Partial<Task> = {}): Task {
  const now = new Date().toISOString();
  return {
    id: 't1',
    title: 'Prep board deck',
    notes: null,
    due: null,
    allDayDue: false,
    scheduledStart: null,
    scheduledEnd: null,
    completedAt: null,
    source: 'local',
    sourceUrl: null,
    position: 1,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function renderRail() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <TaskRail />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  fixtures.tasks = [];
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('TaskRail', () => {
  it('renders due-today and open groups plus the collapsed completed section', async () => {
    fixtures.tasks = [
      makeTask({ id: 't-due', title: 'Send follow-up to Dana', due: new Date().toISOString() }),
      makeTask({ id: 't-open', title: 'Book flights to Lisbon', position: 2 }),
      makeTask({
        id: 't-done',
        title: 'Renew passport',
        position: 3,
        completedAt: new Date().toISOString(),
      }),
    ];
    renderRail();

    expect(await screen.findByText('Send follow-up to Dana')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Due today' })).toBeTruthy();
    expect(screen.getByText('Book flights to Lisbon')).toBeTruthy();
    expect(screen.getByText('Completed (1)')).toBeTruthy();
    // Collapsed by default — the completed row itself is hidden.
    expect(screen.queryByText('Renew passport')).toBeNull();
  });

  it('checks a task off optimistically before the request settles', async () => {
    fixtures.tasks = [makeTask()];
    const { api } = await import('@/lib/api');
    // Never settles: aria-checked below can only come from the optimistic
    // cache patch, not the server response.
    (api.completeTask as ReturnType<typeof vi.fn>).mockReturnValue(new Promise(() => {}));
    renderRail();

    const checkbox = await screen.findByRole('checkbox', { name: 'Complete "Prep board deck"' });
    fireEvent.click(checkbox);

    expect(api.completeTask).toHaveBeenCalledWith('t1');
    const reopen = await screen.findByRole('checkbox', { name: 'Reopen "Prep board deck"' });
    expect(reopen.getAttribute('aria-checked')).toBe('true');
  });

  it('rolls the flip back and toasts when the request fails', async () => {
    fixtures.tasks = [makeTask()];
    const { api } = await import('@/lib/api');
    (api.completeTask as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('boom'));
    renderRail();

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Complete "Prep board deck"' }));

    await waitFor(() => expect(toastMock).toHaveBeenCalled());
    const checkbox = screen.getByRole('checkbox', { name: 'Complete "Prep board deck"' });
    expect(checkbox.getAttribute('aria-checked')).toBe('false');
  });

  it('quick-add submits on Enter, calls createTask, and renders the new task', async () => {
    const { api } = await import('@/lib/api');
    (api.createTask as ReturnType<typeof vi.fn>).mockImplementation(async (input: unknown) =>
      makeTask({ id: 't-new', title: (input as { title: string }).title, position: 9 })
    );
    renderRail();

    const input = await screen.findByLabelText('Add a task');
    fireEvent.change(input, { target: { value: 'Write launch notes' } });
    fireEvent.submit(input.closest('form') as HTMLFormElement);

    expect(api.createTask).toHaveBeenCalledWith({ title: 'Write launch notes' });
    expect(await screen.findByText('Write launch notes')).toBeTruthy();
    expect((input as HTMLInputElement).value).toBe('');
  });
});
