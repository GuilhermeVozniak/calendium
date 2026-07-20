import { addMinutes, startOfDay } from 'date-fns';

/**
 * Drag-and-drop contract for putting tasks on the calendar grid (M2.8 Task 3).
 *
 * Kept as a standalone helper module — separate from the rail component — so
 * other drag sources can reuse the same payload + drop-position math (Task 18
 * reuses this for email-to-event drag).
 *
 * Contract: the drag source calls `setTaskDragData(e.dataTransfer, taskId)`;
 * a day column computes the drop time from the pointer's `clientY` relative
 * to the column top with `timeblockFromDrop`, which mirrors the grid's
 * existing slot-click math in time-grid.tsx (48px per hour, snapped down to
 * 30 minutes) and returns a 30-minute scheduledStart/scheduledEnd pair.
 */

/** Custom MIME type carrying the dragged task's id. */
export const TASK_DRAG_TYPE = 'application/x-calendium-task';

/** Matches HOUR_HEIGHT in components/app/calendar/time-grid.tsx. */
export const GRID_HOUR_HEIGHT = 48;

/** Default timeblock length for a dropped task. */
export const TASK_BLOCK_MINUTES = 30;

export function setTaskDragData(dt: DataTransfer, taskId: string): void {
  dt.setData(TASK_DRAG_TYPE, taskId);
  dt.effectAllowed = 'move';
}

export function hasTaskDrag(dt: DataTransfer | null): boolean {
  if (!dt) return false;
  return Array.from(dt.types ?? []).includes(TASK_DRAG_TYPE);
}

export function readTaskDragId(dt: DataTransfer | null): string | null {
  if (!dt) return null;
  const id = dt.getData(TASK_DRAG_TYPE);
  return id || null;
}

/**
 * Wall-clock drop time for a pointer at `clientY` over a day column whose
 * bounding rect starts at `rectTop`: pixels → minutes since midnight, snapped
 * down to the half hour and clamped to [00:00, 23:30] — the same math as the
 * grid's empty-slot click-to-create.
 */
export function dropTimeForDay(
  day: Date,
  clientY: number,
  rectTop: number,
  hourHeight: number = GRID_HOUR_HEIGHT
): Date {
  const minutes = ((clientY - rectTop) / hourHeight) * 60;
  const snapped = Math.max(0, Math.min(23.5 * 60, Math.floor(minutes / 30) * 30));
  return addMinutes(startOfDay(day), snapped);
}

/** The 30-minute scheduledStart/scheduledEnd pair for a task dropped at `clientY`. */
export function timeblockFromDrop(
  day: Date,
  clientY: number,
  rectTop: number,
  hourHeight: number = GRID_HOUR_HEIGHT
): { scheduledStart: string; scheduledEnd: string } {
  const start = dropTimeForDay(day, clientY, rectTop, hourHeight);
  return {
    scheduledStart: start.toISOString(),
    scheduledEnd: addMinutes(start, TASK_BLOCK_MINUTES).toISOString(),
  };
}
