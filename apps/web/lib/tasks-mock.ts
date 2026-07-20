import type { Task, TaskInput, TaskPatch } from '@calendium/shared';

/**
 * In-memory demo fixtures for tasks (M2.8 Task 3). Served ONLY through the
 * DEMO_MODE gate in lib/use-tasks.ts (honesty policy, lib/demo.ts) — outside
 * demo mode task data always comes from the real API and failures surface.
 */

let seq = 0;

function iso(date: Date): string {
  return date.toISOString();
}

function todayAt(hours: number, minutes = 0): Date {
  const d = new Date();
  d.setHours(hours, minutes, 0, 0);
  return d;
}

function make(overrides: Partial<Task>): Task {
  const now = iso(new Date());
  return {
    id: `demo-task-${++seq}`,
    title: 'Task',
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

function seed(): Task[] {
  return [
    make({ title: 'Prep board deck', due: iso(todayAt(17)) }),
    make({ title: 'Send follow-up to Dana', due: iso(todayAt(12)) }),
    make({ title: 'Book flights to Lisbon' }),
    make({ title: 'Review Q3 budget draft' }),
    make({
      title: 'Deep work: roadmap doc',
      scheduledStart: iso(todayAt(10)),
      scheduledEnd: iso(todayAt(10, 30)),
    }),
    make({ title: 'Renew passport', completedAt: iso(todayAt(8)) }),
  ];
}

let store: Task[] = seed();

export function mockListTasks(): Task[] {
  return store.map((t) => ({ ...t }));
}

export function mockCreateTask(input: TaskInput): Task {
  const maxPosition = store.reduce((max, t) => Math.max(max, t.position), 0);
  const task = make({
    title: input.title,
    notes: input.notes ?? null,
    due: input.due ?? null,
    allDayDue: input.allDayDue ?? false,
    scheduledStart: input.scheduledStart ?? null,
    scheduledEnd: input.scheduledEnd ?? null,
    position: input.position ?? maxPosition + 1,
  });
  store.push(task);
  return { ...task };
}

function patchStored(id: string, apply: (t: Task) => Task): Task {
  const index = store.findIndex((t) => t.id === id);
  if (index === -1) throw new Error(`No demo task ${id}`);
  const next = { ...apply(store[index]), updatedAt: iso(new Date()) };
  store[index] = next;
  return { ...next };
}

export function mockUpdateTask(id: string, patch: TaskPatch): Task {
  return patchStored(id, (t) => ({
    ...t,
    ...(patch.title !== undefined ? { title: patch.title } : null),
    ...(patch.notes !== undefined ? { notes: patch.notes } : null),
    ...(patch.due !== undefined ? { due: patch.due } : null),
    ...(patch.allDayDue !== undefined ? { allDayDue: patch.allDayDue } : null),
    ...(patch.scheduledStart !== undefined ? { scheduledStart: patch.scheduledStart } : null),
    ...(patch.scheduledEnd !== undefined ? { scheduledEnd: patch.scheduledEnd } : null),
    ...(patch.position !== undefined ? { position: patch.position } : null),
  }));
}

export function mockCompleteTask(id: string): Task {
  return patchStored(id, (t) => ({ ...t, completedAt: t.completedAt ?? iso(new Date()) }));
}

export function mockReopenTask(id: string): Task {
  return patchStored(id, (t) => ({ ...t, completedAt: null }));
}

export function mockDeleteTask(id: string): void {
  store = store.filter((t) => t.id !== id);
}

/** Test-only: restore the seeded fixtures. */
export function resetTasksMock(): void {
  seq = 0;
  store = seed();
}
