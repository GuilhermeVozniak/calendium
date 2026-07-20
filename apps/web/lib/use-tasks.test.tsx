import type { Task } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type * as React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listTasksMock = vi.fn();
const createTaskMock = vi.fn();
const updateTaskMock = vi.fn();
const completeTaskMock = vi.fn();
const reopenTaskMock = vi.fn();
const deleteTaskMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listTasks: (...args: unknown[]) => listTasksMock(...args),
    createTask: (...args: unknown[]) => createTaskMock(...args),
    updateTask: (...args: unknown[]) => updateTaskMock(...args),
    completeTask: (...args: unknown[]) => completeTaskMock(...args),
    reopenTask: (...args: unknown[]) => reopenTaskMock(...args),
    deleteTask: (...args: unknown[]) => deleteTaskMock(...args),
  }),
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: { error: (...args: unknown[]) => toastError(...args), success: vi.fn() },
}));

import { TASKS_QUERY_KEY, useTasks } from '@/lib/use-tasks';

let seq = 0;
function makeTask(overrides: Partial<Task> = {}): Task {
  const now = new Date().toISOString();
  return {
    id: `task-${++seq}`,
    title: 'A task',
    notes: null,
    due: null,
    allDayDue: false,
    scheduledStart: null,
    scheduledEnd: null,
    completedAt: null,
    source: 'local',
    sourceUrl: null,
    position: seq,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function setup(tasks: Task[], range?: { from: Date; to: Date }) {
  listTasksMock.mockResolvedValue(tasks);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const hook = renderHook(() => useTasks(range), { wrapper });
  return { client, hook };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('useTasks — grouping', () => {
  it('splits tasks into unscheduled / scheduled / dueByDay / completed', async () => {
    const today = new Date();
    const due = new Date(today.getFullYear(), today.getMonth(), today.getDate(), 17, 0);
    const schedStart = new Date(today.getFullYear(), today.getMonth(), today.getDate(), 10, 0);
    const tasks = [
      makeTask({ id: 'un1', title: 'Unscheduled one' }),
      makeTask({ id: 'due1', title: 'Due today', due: due.toISOString() }),
      makeTask({
        id: 'sch1',
        title: 'Scheduled',
        scheduledStart: schedStart.toISOString(),
        scheduledEnd: new Date(schedStart.getTime() + 30 * 60 * 1000).toISOString(),
      }),
      makeTask({ id: 'done1', title: 'Done', completedAt: new Date().toISOString() }),
    ];
    const { hook } = setup(tasks);
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(4));

    const { unscheduled, scheduled, dueByDay, completed } = hook.result.current;
    expect(unscheduled.map((t) => t.id)).toEqual(['un1', 'due1']);
    expect(scheduled.map((t) => t.id)).toEqual(['sch1']);
    const dayKey = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;
    expect(dueByDay.get(dayKey)?.map((t) => t.id)).toEqual(['due1']);
    expect(completed.map((t) => t.id)).toEqual(['done1']);
  });

  it('filters scheduled tasks to the given range', async () => {
    const inRange = new Date(2026, 6, 22, 10, 0);
    const outOfRange = new Date(2026, 7, 22, 10, 0);
    const tasks = [
      makeTask({
        id: 'in',
        scheduledStart: inRange.toISOString(),
        scheduledEnd: new Date(inRange.getTime() + 30 * 60 * 1000).toISOString(),
      }),
      makeTask({
        id: 'out',
        scheduledStart: outOfRange.toISOString(),
        scheduledEnd: new Date(outOfRange.getTime() + 30 * 60 * 1000).toISOString(),
      }),
    ];
    const { hook } = setup(tasks, {
      from: new Date(2026, 6, 20),
      to: new Date(2026, 6, 27),
    });
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(2));
    expect(hook.result.current.scheduled.map((t) => t.id)).toEqual(['in']);
  });
});

describe('useTasks — optimistic check-off', () => {
  it('marks the task completed in cache before the request resolves', async () => {
    const { client, hook } = setup([makeTask({ id: 't1' })]);
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(1));

    completeTaskMock.mockReturnValue(new Promise(() => {})); // never resolves
    act(() => {
      void hook.result.current.complete('t1');
    });

    const cached = client.getQueryData<Task[]>(TASKS_QUERY_KEY);
    expect(cached?.[0]?.completedAt).not.toBeNull();
    expect(completeTaskMock).toHaveBeenCalledWith('t1');
  });

  it('rolls back the cache and toasts when the request fails', async () => {
    const { client, hook } = setup([makeTask({ id: 't1' })]);
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(1));

    completeTaskMock.mockRejectedValue(new Error('boom'));
    let ok: boolean | undefined;
    await act(async () => {
      ok = await hook.result.current.complete('t1');
    });

    expect(ok).toBe(false);
    expect(client.getQueryData<Task[]>(TASKS_QUERY_KEY)?.[0]?.completedAt).toBeNull();
    expect(toastError).toHaveBeenCalled();
  });

  it('reopen clears completedAt optimistically', async () => {
    const { client, hook } = setup([
      makeTask({ id: 't1', completedAt: new Date().toISOString() }),
    ]);
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(1));

    reopenTaskMock.mockReturnValue(new Promise(() => {}));
    act(() => {
      void hook.result.current.reopen('t1');
    });
    expect(client.getQueryData<Task[]>(TASKS_QUERY_KEY)?.[0]?.completedAt).toBeNull();
    expect(reopenTaskMock).toHaveBeenCalledWith('t1');
  });
});

describe('useTasks — createTask', () => {
  it('inserts optimistically and reconciles with the server task', async () => {
    const { client, hook } = setup([]);
    await waitFor(() => expect(listTasksMock).toHaveBeenCalled());

    let resolve!: (t: Task) => void;
    createTaskMock.mockReturnValue(
      new Promise<Task>((r) => {
        resolve = r;
      })
    );
    act(() => {
      void hook.result.current.createTask({ title: 'New' });
    });
    expect(
      client.getQueryData<Task[]>(TASKS_QUERY_KEY)?.some((t) => t.title === 'New')
    ).toBe(true);

    const server = makeTask({ id: 'server-1', title: 'New' });
    await act(async () => {
      resolve(server);
    });
    await waitFor(() =>
      expect(
        client.getQueryData<Task[]>(TASKS_QUERY_KEY)?.some((t) => t.id === 'server-1')
      ).toBe(true)
    );
    expect(
      client.getQueryData<Task[]>(TASKS_QUERY_KEY)?.some((t) => t.id.startsWith('temp:'))
    ).toBe(false);
  });

  it('removes the optimistic task again when the request fails', async () => {
    const { client, hook } = setup([]);
    await waitFor(() => expect(listTasksMock).toHaveBeenCalled());

    createTaskMock.mockRejectedValue(new Error('boom'));
    await act(async () => {
      await hook.result.current.createTask({ title: 'New' });
    });
    expect(client.getQueryData<Task[]>(TASKS_QUERY_KEY)).toHaveLength(0);
    expect(toastError).toHaveBeenCalled();
  });
});

describe('useTasks — updateTask (drag-to-timeblock)', () => {
  it('applies the schedule patch optimistically and calls the API', async () => {
    const { client, hook } = setup([makeTask({ id: 't1' })]);
    await waitFor(() => expect(hook.result.current.tasks).toHaveLength(1));

    const patch = {
      scheduledStart: '2026-07-22T10:00:00.000Z',
      scheduledEnd: '2026-07-22T10:30:00.000Z',
    };
    updateTaskMock.mockReturnValue(new Promise(() => {}));
    act(() => {
      void hook.result.current.updateTask('t1', patch);
    });
    const cached = client.getQueryData<Task[]>(TASKS_QUERY_KEY);
    expect(cached?.[0]?.scheduledStart).toBe(patch.scheduledStart);
    expect(cached?.[0]?.scheduledEnd).toBe(patch.scheduledEnd);
    expect(updateTaskMock).toHaveBeenCalledWith('t1', patch);
  });
});
