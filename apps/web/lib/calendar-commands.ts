'use client';

import * as React from 'react';

import type { EventInput } from '@calendium/shared';

import type { CalendarView } from '@/lib/calendar-views';

/**
 * Cross-component calendar actions (command palette → calendar page), mirroring
 * mail-utils.ts's MailCommand bus: dispatchCalendarCommand delivers a command
 * to a mounted listener as a DOM event; queueCalendarCommand durably stashes
 * one for a page that hasn't mounted yet (e.g. the palette acting on the
 * calendar from another route).
 */
export type CalendarCommand =
  | { type: 'today' }
  | { type: 'step'; dir: 1 | -1 }
  | { type: 'view'; view: CalendarView }
  | { type: 'new-event'; defaults?: Partial<EventInput> }
  | { type: 'new-from-template'; templateId: string }
  | { type: 'share-availability' }
  | { type: 'toggle-set'; setId: string }
  | { type: 'time-travel' }
  // Task rail (M2.8 Task 3b): palette / global-shortcut parity for the rail.
  | { type: 'new-task' }
  | { type: 'toggle-task-rail' };

export const CALENDAR_COMMAND_EVENT = 'calendium:calendar-command';

export function dispatchCalendarCommand(command: CalendarCommand): void {
  window.dispatchEvent(
    new CustomEvent<CalendarCommand>(CALENDAR_COMMAND_EVENT, { detail: command })
  );
}

export function onCalendarCommand(handler: (command: CalendarCommand) => void): () => void {
  const listener = (event: Event) => handler((event as CustomEvent<CalendarCommand>).detail);
  window.addEventListener(CALENDAR_COMMAND_EVENT, listener);
  return () => window.removeEventListener(CALENDAR_COMMAND_EVENT, listener);
}

// A command triggered from another route can't be delivered as a DOM event —
// the calendar page hasn't mounted its listener yet. Queue it here and let the
// calendar page consume it on mount (durable, unlike a fixed setTimeout).
let pendingCalendarCommand: CalendarCommand | null = null;

export function queueCalendarCommand(command: CalendarCommand): void {
  pendingCalendarCommand = command;
}

export function takePendingCalendarCommand(): CalendarCommand | null {
  const command = pendingCalendarCommand;
  pendingCalendarCommand = null;
  return command;
}

/**
 * Mounts a single handler for calendar commands: drains any command queued
 * before mount (exactly once), then subscribes to live dispatches for the
 * lifetime of the component. The handler is read through a ref so callers can
 * pass a fresh closure on every render without tearing down and resubscribing
 * the listener (mirrors lib/shortcuts.ts's useShortcuts pattern).
 */
export function useCalendarCommands(handler: (command: CalendarCommand) => void): void {
  const ref = React.useRef(handler);
  ref.current = handler;

  React.useEffect(() => {
    const pending = takePendingCalendarCommand();
    if (pending) ref.current(pending);
    return onCalendarCommand((command) => ref.current(command));
  }, []);
}
