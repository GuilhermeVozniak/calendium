'use client';

import * as React from 'react';
import { format, isSameDay } from 'date-fns';
import { Check, ChevronRight, Clock, Plus, X } from 'lucide-react';

import type { Task } from '@calendium/shared';

import { hasTaskDrag, readTaskDragId, setTaskDragData } from '@/lib/task-drag';
import { useTasks } from '@/lib/use-tasks';
import { cn } from '@/lib/utils';

/**
 * Task rail beside the calendar grid (M2.8 Task 3): due-today + unscheduled
 * groups, an inline quick-add, and a collapsed completed section. Items are
 * drag sources for the grid's drag-to-timeblock (lib/task-drag.ts), and the
 * rail itself is a drop target that clears a dropped task's timeblock.
 *
 * Check-off is optimistic and stays in place: a just-completed task keeps its
 * spot in its group (strikethrough, checked) for the rest of the session
 * instead of jumping into the collapsed Completed section mid-glance; the
 * checkbox doubles as the undo affordance (clicking again reopens).
 */
export interface TaskRailProps {
  onClose?: () => void;
  className?: string;
}

export function TaskRail({ onClose, className }: TaskRailProps) {
  const { tasks, completed, isLoading, createTask, updateTask, complete, reopen } = useTasks();
  const [title, setTitle] = React.useState('');
  const [showCompleted, setShowCompleted] = React.useState(false);
  // Tasks checked off during this session: kept rendered in place in their
  // original group (with completed styling) rather than moved to Completed.
  const [held, setHeld] = React.useState<ReadonlySet<string>>(() => new Set());

  const today = new Date();
  const isDueToday = (t: Task) => t.due !== null && isSameDay(new Date(t.due), today);
  const inRail = (t: Task) => t.completedAt === null || held.has(t.id);

  const dueToday = tasks.filter((t) => inRail(t) && isDueToday(t));
  const unscheduled = tasks
    .filter((t) => inRail(t) && !t.scheduledStart && !isDueToday(t))
    .sort((a, b) => a.position - b.position);
  const completedCollapsed = completed.filter((t) => !held.has(t.id));

  const toggle = React.useCallback(
    (task: Task) => {
      if (task.completedAt !== null) {
        setHeld((prev) => {
          const next = new Set(prev);
          next.delete(task.id);
          return next;
        });
        void reopen(task.id);
      } else {
        setHeld((prev) => new Set(prev).add(task.id));
        void complete(task.id);
      }
    },
    [complete, reopen]
  );

  const submitQuickAdd = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = title.trim();
    if (!trimmed) return;
    setTitle('');
    void createTask({ title: trimmed });
  };

  return (
    <aside
      aria-label="Tasks"
      data-testid="task-rail"
      onDragOver={(e) => {
        if (hasTaskDrag(e.dataTransfer)) {
          e.preventDefault();
          e.dataTransfer.dropEffect = 'move';
        }
      }}
      onDrop={(e) => {
        const taskId = readTaskDragId(e.dataTransfer);
        if (!taskId) return;
        e.preventDefault();
        // Dragging a grid task block back to the rail clears its timeblock.
        void updateTask(taskId, { scheduledStart: null, scheduledEnd: null });
      }}
      className={cn('flex w-64 shrink-0 flex-col overflow-y-auto border-l', className)}
    >
      <div className="flex items-center justify-between px-4 pt-4 pb-2">
        <h2 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
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
        <div className="flex items-center gap-2 rounded-md border px-2 py-1.5 focus-within:ring-2 focus-within:ring-ring">
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
        <TaskGroup label="Due today" tasks={dueToday} onToggle={toggle} />
        <TaskGroup label="Unscheduled" tasks={unscheduled} onToggle={toggle} />

        {!isLoading && dueToday.length === 0 && unscheduled.length === 0 && (
          <p className="px-2 text-sm text-muted-foreground">
            No open tasks. Add one above, or drag one off the grid.
          </p>
        )}

        {completedCollapsed.length > 0 && (
          <div className="flex flex-col gap-0.5">
            <button
              type="button"
              onClick={() => setShowCompleted((v) => !v)}
              aria-expanded={showCompleted}
              className="flex items-center gap-1 rounded-md px-2 py-1 text-left text-xs font-semibold tracking-wide text-muted-foreground uppercase hover:bg-accent"
            >
              <ChevronRight
                className={cn('size-3.5 transition-transform', showCompleted && 'rotate-90')}
              />
              Completed ({completedCollapsed.length})
            </button>
            {showCompleted &&
              completedCollapsed.map((task) => (
                <TaskItem key={task.id} task={task} onToggle={() => toggle(task)} />
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
  onToggle: (task: Task) => void;
}) {
  if (tasks.length === 0) return null;
  return (
    <section aria-label={label} className="flex flex-col gap-0.5">
      <h3 className="px-2 pb-1 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
        {label}
      </h3>
      {tasks.map((task) => (
        <TaskItem key={task.id} task={task} onToggle={() => onToggle(task)} />
      ))}
    </section>
  );
}

export function TaskItem({ task, onToggle }: { task: Task; onToggle: () => void }) {
  const completed = task.completedAt !== null;
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: drag source only — the row's interactive controls are real buttons inside it.
    <div
      data-testid={`task-item-${task.id}`}
      draggable={!completed}
      onDragStart={(e) => setTaskDragData(e.dataTransfer, task.id)}
      className="group flex cursor-grab items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-accent"
    >
      {/* biome-ignore lint/a11y/useSemanticElements: styled round check control; a native checkbox can't render this design and the surrounding row must stay a draggable div. */}
      <button
        type="button"
        role="checkbox"
        aria-checked={completed}
        aria-label={completed ? `Reopen "${task.title}"` : `Complete "${task.title}"`}
        onClick={onToggle}
        className={cn(
          'mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring',
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
              : format(new Date(task.due), 'MMM d, h:mm a')}
          </div>
        )}
      </div>
    </div>
  );
}
