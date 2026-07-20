'use client';

import * as React from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import { toast } from 'sonner';

import type { Task, TaskInput, TaskPatch } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import {
  mockCompleteTask,
  mockCreateTask,
  mockDeleteTask,
  mockListTasks,
  mockReopenTask,
  mockUpdateTask,
} from '@/lib/tasks-mock';

/**
 * TanStack Query hooks for tasks (M2.8 Task 3). Every mutation is optimistic
 * with snapshot rollback + toast on a real failure, mirroring the
 * thread-action pattern in use-mail.ts: the cache is patched synchronously
 * BEFORE the request goes out, so check-off and drag-to-timeblock render
 * immediately (sub-100ms), and a failed request restores the snapshot.
 * In demo mode (lib/demo.ts) failures fall back to the in-memory fixture
 * store instead of rolling back.
 */

export const TASKS_QUERY_KEY = ['tasks'] as const;

async function fetchTasks(): Promise<Task[]> {
  try {
    return await getApiClient().listTasks();
  } catch (err) {
    if (DEMO_MODE) return mockListTasks();
    throw err;
  }
}

function applyPatch(task: Task, patch: TaskPatch): Task {
  return {
    ...task,
    ...(patch.title !== undefined ? { title: patch.title } : null),
    ...(patch.notes !== undefined ? { notes: patch.notes } : null),
    ...(patch.due !== undefined ? { due: patch.due } : null),
    ...(patch.allDayDue !== undefined ? { allDayDue: patch.allDayDue } : null),
    ...(patch.scheduledStart !== undefined ? { scheduledStart: patch.scheduledStart } : null),
    ...(patch.scheduledEnd !== undefined ? { scheduledEnd: patch.scheduledEnd } : null),
    ...(patch.position !== undefined ? { position: patch.position } : null),
    updatedAt: new Date().toISOString(),
  };
}

let tempSeq = 0;

export interface UseTasksResult {
  /** Every task, as fetched (all groups derive from this). */
  tasks: Task[];
  /** Active tasks with no timeblock, rail order (position asc). */
  unscheduled: Task[];
  /** Active timeblocked tasks, filtered to `range` when one is given. */
  scheduled: Task[];
  /** Active tasks with a deadline, keyed by local yyyy-MM-dd of `due`. */
  dueByDay: Map<string, Task[]>;
  /** Checked-off tasks, most recent first. */
  completed: Task[];
  isLoading: boolean;
  createTask: (input: TaskInput) => Promise<boolean>;
  updateTask: (taskId: string, patch: TaskPatch) => Promise<boolean>;
  complete: (taskId: string) => Promise<boolean>;
  reopen: (taskId: string) => Promise<boolean>;
  remove: (taskId: string) => Promise<boolean>;
}

export function useTasks(range?: { from: Date; to: Date }): UseTasksResult {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: TASKS_QUERY_KEY, queryFn: fetchTasks });
  const tasks = React.useMemo(() => query.data ?? [], [query.data]);

  const rangeFromMs = range?.from.getTime();
  const rangeToMs = range?.to.getTime();

  const groups = React.useMemo(() => {
    const active = tasks.filter((t) => t.completedAt === null);
    const unscheduled = active
      .filter((t) => !t.scheduledStart)
      .sort((a, b) => a.position - b.position);
    const scheduled = active
      .filter((t) => {
        if (!t.scheduledStart || !t.scheduledEnd) return false;
        if (rangeFromMs === undefined || rangeToMs === undefined) return true;
        return (
          new Date(t.scheduledStart).getTime() < rangeToMs &&
          new Date(t.scheduledEnd).getTime() > rangeFromMs
        );
      })
      .sort(
        (a, b) =>
          new Date(a.scheduledStart as string).getTime() -
          new Date(b.scheduledStart as string).getTime()
      );
    const dueByDay = new Map<string, Task[]>();
    for (const t of active) {
      if (!t.due) continue;
      const key = format(new Date(t.due), 'yyyy-MM-dd');
      const list = dueByDay.get(key);
      if (list) list.push(t);
      else dueByDay.set(key, [t]);
    }
    const completed = tasks
      .filter((t) => t.completedAt !== null)
      .sort(
        (a, b) =>
          new Date(b.completedAt as string).getTime() -
          new Date(a.completedAt as string).getTime()
      );
    return { unscheduled, scheduled, dueByDay, completed };
  }, [tasks, rangeFromMs, rangeToMs]);

  const setCache = React.useCallback(
    (updater: (prev: Task[] | undefined) => Task[] | undefined) => {
      queryClient.setQueryData<Task[] | undefined>(TASKS_QUERY_KEY, updater);
    },
    [queryClient]
  );

  const patchCache = React.useCallback(
    (taskId: string, patch: (t: Task) => Task) => {
      setCache((prev) => prev?.map((t) => (t.id === taskId ? patch(t) : t)));
    },
    [setCache]
  );

  /**
   * The optimistic skeleton (use-mail.ts thread-action pattern): snapshot →
   * patch the cache synchronously → fire the request → reconcile on success,
   * demo-fallback or rollback+toast on failure.
   */
  const run = React.useCallback(
    async <T,>(
      optimistic: () => void,
      request: () => Promise<T>,
      demoApply: () => void,
      errorMessage: string,
      reconcile?: (result: T) => void
    ): Promise<boolean> => {
      const previous = queryClient.getQueryData<Task[]>(TASKS_QUERY_KEY);
      optimistic();
      try {
        const result = await request();
        reconcile?.(result);
        return true;
      } catch {
        if (DEMO_MODE) {
          try {
            demoApply();
            queryClient.setQueryData(TASKS_QUERY_KEY, mockListTasks());
            return true;
          } catch {
            // Fixture miss — fall through to rollback.
          }
        }
        if (previous) queryClient.setQueryData(TASKS_QUERY_KEY, previous);
        toast.error(errorMessage);
        return false;
      }
    },
    [queryClient]
  );

  const createTask = React.useCallback(
    (input: TaskInput): Promise<boolean> => {
      const tempId = `temp:${++tempSeq}`;
      const now = new Date().toISOString();
      const current = queryClient.getQueryData<Task[]>(TASKS_QUERY_KEY) ?? [];
      const temp: Task = {
        id: tempId,
        title: input.title,
        notes: input.notes ?? null,
        due: input.due ?? null,
        allDayDue: input.allDayDue ?? false,
        scheduledStart: input.scheduledStart ?? null,
        scheduledEnd: input.scheduledEnd ?? null,
        completedAt: null,
        source: 'local',
        sourceUrl: null,
        position: input.position ?? current.reduce((max, t) => Math.max(max, t.position), 0) + 1,
        createdAt: now,
        updatedAt: now,
      };
      return run(
        () => setCache((prev) => [...(prev ?? []), temp]),
        () => getApiClient().createTask(input),
        () => mockCreateTask(input),
        'Could not create the task.',
        (created) => setCache((prev) => prev?.map((t) => (t.id === tempId ? created : t)))
      );
    },
    [queryClient, run, setCache]
  );

  const updateTask = React.useCallback(
    (taskId: string, patch: TaskPatch): Promise<boolean> =>
      run(
        () => patchCache(taskId, (t) => applyPatch(t, patch)),
        () => getApiClient().updateTask(taskId, patch),
        () => mockUpdateTask(taskId, patch),
        'Could not update the task.',
        (updated) => patchCache(taskId, () => updated)
      ),
    [run, patchCache]
  );

  const complete = React.useCallback(
    (taskId: string): Promise<boolean> =>
      run(
        () =>
          patchCache(taskId, (t) => ({
            ...t,
            completedAt: t.completedAt ?? new Date().toISOString(),
          })),
        () => getApiClient().completeTask(taskId),
        () => mockCompleteTask(taskId),
        'Could not complete the task.',
        (updated) => patchCache(taskId, () => updated)
      ),
    [run, patchCache]
  );

  const reopen = React.useCallback(
    (taskId: string): Promise<boolean> =>
      run(
        () => patchCache(taskId, (t) => ({ ...t, completedAt: null })),
        () => getApiClient().reopenTask(taskId),
        () => mockReopenTask(taskId),
        'Could not reopen the task.',
        (updated) => patchCache(taskId, () => updated)
      ),
    [run, patchCache]
  );

  const remove = React.useCallback(
    (taskId: string): Promise<boolean> =>
      run(
        () => setCache((prev) => prev?.filter((t) => t.id !== taskId)),
        () => getApiClient().deleteTask(taskId),
        () => mockDeleteTask(taskId),
        'Could not delete the task.'
      ),
    [run, setCache]
  );

  return {
    tasks,
    unscheduled: groups.unscheduled,
    scheduled: groups.scheduled,
    dueByDay: groups.dueByDay,
    completed: groups.completed,
    isLoading: query.isLoading,
    createTask,
    updateTask,
    complete,
    reopen,
    remove,
  };
}
