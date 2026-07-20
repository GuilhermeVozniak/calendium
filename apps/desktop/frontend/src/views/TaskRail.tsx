import type { Task } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format, isSameDay } from 'date-fns';
import { Check, ChevronRight, Clock, Plus, X } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { createMockTask, mockListTasks, setMockTaskCompleted } from '@/lib/mock';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';

/**
 * Desktop mirror of the web calendar's task rail (M2.8 Task 3b): due-today +
 * open groups, an inline quick-add, a collapsed completed section, and
 * optimistic in-place check-off with rollback + toast on real failures
 * (mirroring apps/web's use-tasks.ts pattern). No drag-to-timeblock — the
 * desktop grid has no per-slot task blocks; this is the parity surface.
 */
export function TaskRail({ onClose }: { onClose?: () => void }) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [showCompleted, setShowCompleted] = useState(false);
  // Tasks checked off during this session: kept rendered in place in their
  // original group (checked + strikethrough) instead of jumping into the
  // collapsed Completed section mid-glance — mirrors the web rail.
  const [held, setHeld] = useState<ReadonlySet<string>>(() => new Set());

  const { data: tasks = [], isLoading } = useQuery({
    queryKey: ['tasks'],
    queryFn: () =>
      orMock(
        () => api.listTasks(),
        () => mockListTasks()
      ),
  });

  const today = new Date();
  const inRail = (t: Task) => t.completedAt === null || held.has(t.id);
  const active = tasks.filter(inRail);
  const dueToday = active.filter((t) => t.due !== null && isSameDay(new Date(t.due), today));
  const open = active.filter((t) => !dueToday.includes(t)).sort((a, b) => a.position - b.position);
  const completed = tasks.filter((t) => t.completedAt !== null && !held.has(t.id));

  const setCache = (updater: (prev: Task[]) => Task[]) =>
    queryClient.setQueryData<Task[]>(['tasks'], (prev) => updater(prev ?? []));

  async function toggleTask(task: Task) {
    const reopening = task.completedAt !== null;
    setHeld((prev) => {
      const next = new Set(prev);
      if (reopening) next.delete(task.id);
      else next.add(task.id);
      return next;
    });
    const previous = queryClient.getQueryData<Task[]>(['tasks']);
    // Optimistic in-place flip; the row stays in its group (checked +
    // strikethrough) instead of jumping into Completed mid-glance.
    setCache((prev) =>
      prev.map((t) =>
        t.id === task.id ? { ...t, completedAt: reopening ? null : new Date().toISOString() } : t
      )
    );
    try {
      const updated = await orMock(
        () => (reopening ? api.reopenTask(task.id) : api.completeTask(task.id)),
        () => setMockTaskCompleted(task.id, !reopening)
      );
      setCache((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
    } catch (err) {
      if (previous) queryClient.setQueryData(['tasks'], previous);
      toast({ title: 'Could not update the task', description: errorMessage(err) });
    }
  }

  async function submitQuickAdd(e: FormEvent) {
    e.preventDefault();
    const trimmed = title.trim();
    if (!trimmed) return;
    setTitle('');
    const previous = queryClient.getQueryData<Task[]>(['tasks']);
    const tempId = `temp:${Date.now()}`;
    const nowIso = new Date().toISOString();
    setCache((prev) => [
      ...prev,
      {
        id: tempId,
        title: trimmed,
        notes: null,
        due: null,
        allDayDue: false,
        scheduledStart: null,
        scheduledEnd: null,
        completedAt: null,
        source: 'local',
        sourceUrl: null,
        position: prev.reduce((max, t) => Math.max(max, t.position), 0) + 1,
        createdAt: nowIso,
        updatedAt: nowIso,
      },
    ]);
    try {
      const created = await orMock(
        () => api.createTask({ title: trimmed }),
        () => createMockTask({ title: trimmed })
      );
      setCache((prev) => prev.map((t) => (t.id === tempId ? created : t)));
    } catch (err) {
      if (previous) queryClient.setQueryData(['tasks'], previous);
      toast({ title: 'Could not create the task', description: errorMessage(err) });
    }
  }

  return (
    <aside
      aria-label="Tasks"
      data-testid="task-rail"
      className="flex w-64 shrink-0 flex-col overflow-y-auto border-l"
    >
      <div className="flex items-center justify-between px-4 pb-2 pt-4">
        <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Tasks
        </h2>
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label="Hide task rail"
            className="rounded-md p-1 text-muted-foreground hover:bg-accent"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>

      <form onSubmit={submitQuickAdd} className="px-3 pb-3">
        <div className="flex items-center gap-2 rounded-md border px-2 py-1.5 focus-within:ring-1 focus-within:ring-ring">
          <Plus className="size-3.5 shrink-0 text-muted-foreground" />
          <input
            aria-label="Add a task"
            placeholder="Add a task…"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            className="w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
        </div>
      </form>

      <div className="flex flex-1 flex-col gap-4 px-2 pb-4">
        <TaskGroup label="Due today" tasks={dueToday} onToggle={toggleTask} />
        <TaskGroup label="Open" tasks={open} onToggle={toggleTask} />

        {!isLoading && dueToday.length === 0 && open.length === 0 && (
          <p className="px-2 text-sm text-muted-foreground">No open tasks. Add one above.</p>
        )}

        {completed.length > 0 && (
          <div className="flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => setShowCompleted((v) => !v)}
              aria-expanded={showCompleted}
              className="flex items-center gap-1 rounded-md px-2 py-1 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground hover:bg-accent"
            >
              <ChevronRight
                className={cn('size-3.5 transition-transform', showCompleted && 'rotate-90')}
              />
              Completed ({completed.length})
            </button>
            {showCompleted &&
              completed.map((task) => (
                <TaskItem key={task.id} task={task} onToggle={() => void toggleTask(task)} />
              ))}
          </div>
        )}
      </div>
    </aside>
  );
}

function TaskGroup({
  label,
  tasks,
  onToggle,
}: {
  label: string;
  tasks: Task[];
  onToggle: (task: Task) => Promise<void>;
}) {
  if (tasks.length === 0) return null;
  return (
    <section aria-label={label} className="flex flex-col gap-0.5">
      <h3 className="px-2 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </h3>
      {tasks.map((task) => (
        <TaskItem key={task.id} task={task} onToggle={() => void onToggle(task)} />
      ))}
    </section>
  );
}

function TaskItem({ task, onToggle }: { task: Task; onToggle: () => void }) {
  const completed = task.completedAt !== null;
  return (
    <div
      data-testid={`task-item-${task.id}`}
      className="group flex items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
    >
      {/* biome-ignore lint/a11y/useSemanticElements: styled round check control mirroring the web rail's design; a native checkbox can't render it. */}
      <button
        type="button"
        role="checkbox"
        aria-checked={completed}
        aria-label={completed ? `Reopen "${task.title}"` : `Complete "${task.title}"`}
        onClick={onToggle}
        className={cn(
          'mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border transition-colors outline-none focus-visible:ring-1 focus-visible:ring-ring',
          completed
            ? 'border-primary bg-primary text-primary-foreground'
            : 'border-muted-foreground/50 hover:border-primary'
        )}
      >
        {completed && <Check className="size-3" />}
      </button>
      <div className="min-w-0 flex-1">
        <div
          className={cn(
            'truncate text-sm leading-tight',
            completed && 'text-muted-foreground line-through'
          )}
        >
          {task.title}
        </div>
        {task.due && (
          <div className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
            <Clock className="size-3" />
            {task.allDayDue
              ? format(new Date(task.due), 'MMM d')
              : format(new Date(task.due), 'MMM d, HH:mm')}
          </div>
        )}
      </div>
    </div>
  );
}
