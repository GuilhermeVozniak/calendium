import { describe, expect, it, vi } from 'vitest';

import {
  TASK_BLOCK_MINUTES,
  TASK_DRAG_TYPE,
  dropTimeForDay,
  hasTaskDrag,
  readTaskDragId,
  setTaskDragData,
  timeblockFromDrop,
} from '@/lib/task-drag';

/** Minimal DataTransfer stub — jsdom has no real constructor. */
function makeDataTransfer(data: Record<string, string> = {}) {
  const store: Record<string, string> = { ...data };
  return {
    setData: vi.fn((type: string, value: string) => {
      store[type] = value;
    }),
    getData: vi.fn((type: string) => store[type] ?? ''),
    get types() {
      return Object.keys(store);
    },
    effectAllowed: '',
    dropEffect: '',
  } as unknown as DataTransfer;
}

describe('task drag payload', () => {
  it('round-trips the task id through the custom MIME type', () => {
    const dt = makeDataTransfer();
    setTaskDragData(dt, 'task-1');
    expect(dt.setData).toHaveBeenCalledWith(TASK_DRAG_TYPE, 'task-1');
    expect(readTaskDragId(dt)).toBe('task-1');
    expect(hasTaskDrag(dt)).toBe(true);
  });

  it('returns null / false when there is no task payload', () => {
    expect(readTaskDragId(null)).toBeNull();
    expect(readTaskDragId(makeDataTransfer())).toBeNull();
    expect(hasTaskDrag(null)).toBe(false);
    expect(hasTaskDrag(makeDataTransfer({ 'text/plain': 'x' }))).toBe(false);
  });
});

describe('drop time math (48px per hour, 30-minute snap)', () => {
  const day = new Date(2026, 6, 22); // Wed Jul 22 2026, local midnight

  it('snaps the pointer down to the previous 30-minute slot', () => {
    // 500px = 625 clock minutes (10:25) → snaps to 10:00.
    const t = dropTimeForDay(day, 500, 0);
    expect(t.getHours()).toBe(10);
    expect(t.getMinutes()).toBe(0);
  });

  it('subtracts the column top before converting to minutes', () => {
    // 48px into the column = one hour into the day.
    const t = dropTimeForDay(day, 148, 100);
    expect(t.getHours()).toBe(1);
    expect(t.getMinutes()).toBe(0);
  });

  it('clamps to the top and bottom of the day', () => {
    const early = dropTimeForDay(day, -50, 0);
    expect(early.getHours()).toBe(0);
    expect(early.getMinutes()).toBe(0);
    const late = dropTimeForDay(day, 48 * 25, 0);
    expect(late.getHours()).toBe(23);
    expect(late.getMinutes()).toBe(30);
  });

  it('timeblockFromDrop returns a 30-minute block starting at the snapped slot', () => {
    // 480px = 600 minutes = exactly 10:00.
    const block = timeblockFromDrop(day, 480, 0);
    const start = new Date(block.scheduledStart);
    expect(start.getHours()).toBe(10);
    expect(start.getMinutes()).toBe(0);
    expect(new Date(block.scheduledEnd).getTime() - start.getTime()).toBe(
      TASK_BLOCK_MINUTES * 60 * 1000
    );
  });
});
