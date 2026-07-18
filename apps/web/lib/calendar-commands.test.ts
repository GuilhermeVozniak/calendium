import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  CALENDAR_COMMAND_EVENT,
  dispatchCalendarCommand,
  onCalendarCommand,
  queueCalendarCommand,
  takePendingCalendarCommand,
  useCalendarCommands,
  type CalendarCommand,
} from '@/lib/calendar-commands';

describe('calendar command bus', () => {
  afterEach(() => {
    takePendingCalendarCommand();
  });

  it('dispatches and receives a command via the DOM event', () => {
    const handler = vi.fn();
    const unsubscribe = onCalendarCommand(handler);
    const command: CalendarCommand = { type: 'today' };
    dispatchCalendarCommand(command);
    expect(handler).toHaveBeenCalledWith(command);
    unsubscribe();
  });

  it('stops receiving commands after unsubscribing', () => {
    const handler = vi.fn();
    const unsubscribe = onCalendarCommand(handler);
    unsubscribe();
    dispatchCalendarCommand({ type: 'today' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('fires the event under the expected event name', () => {
    const handler = vi.fn();
    window.addEventListener(CALENDAR_COMMAND_EVENT, handler as EventListener);
    dispatchCalendarCommand({ type: 'step', dir: 1 });
    expect(handler).toHaveBeenCalledTimes(1);
    window.removeEventListener(CALENDAR_COMMAND_EVENT, handler as EventListener);
  });

  it('queues a command and hands it back exactly once', () => {
    expect(takePendingCalendarCommand()).toBeNull();
    queueCalendarCommand({ type: 'view', view: 'month' });
    expect(takePendingCalendarCommand()).toEqual({ type: 'view', view: 'month' });
    expect(takePendingCalendarCommand()).toBeNull();
  });

  it('overwrites a previously queued command', () => {
    queueCalendarCommand({ type: 'today' });
    queueCalendarCommand({ type: 'share-availability' });
    expect(takePendingCalendarCommand()).toEqual({ type: 'share-availability' });
  });
});

describe('useCalendarCommands', () => {
  afterEach(() => {
    takePendingCalendarCommand();
  });

  it('reaches a mounted handler when a command is dispatched live', () => {
    const handler = vi.fn();
    renderHook(() => useCalendarCommands(handler));
    dispatchCalendarCommand({ type: 'today' });
    expect(handler).toHaveBeenCalledWith({ type: 'today' });
  });

  it('drains a command queued before the handler mounted, exactly once', () => {
    queueCalendarCommand({ type: 'view', view: 'day' });
    const handler = vi.fn();
    renderHook(() => useCalendarCommands(handler));
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith({ type: 'view', view: 'day' });
    expect(takePendingCalendarCommand()).toBeNull();
  });

  it('does not redeliver the drained command on rerender', () => {
    queueCalendarCommand({ type: 'today' });
    const handler = vi.fn();
    const { rerender } = renderHook(() => useCalendarCommands(handler));
    rerender();
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('stops receiving commands after unmount', () => {
    const handler = vi.fn();
    const { unmount } = renderHook(() => useCalendarCommands(handler));
    unmount();
    dispatchCalendarCommand({ type: 'today' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('picks up an updated handler reference without remounting', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ handler }) => useCalendarCommands(handler), {
      initialProps: { handler: first },
    });
    rerender({ handler: second });
    dispatchCalendarCommand({ type: 'today' });
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});
