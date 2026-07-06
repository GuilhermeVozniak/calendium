import type { EmailAddress, Thread } from '@calendium/shared';
import { addDays, addHours, format, isThisYear, isToday, nextMonday, nextSaturday, set } from 'date-fns';

/** Non-split mailbox views shown in the left rail. */
export type MailboxView = 'starred' | 'snoozed' | 'sent' | 'drafts';

// ---------------------------------------------------------------------------
// Time formatting (Superhuman-dense list times)
// ---------------------------------------------------------------------------

export function formatListTime(iso: string): string {
  const d = new Date(iso);
  if (isToday(d)) return format(d, 'p');
  if (isThisYear(d)) return format(d, 'MMM d');
  return format(d, 'M/d/yy');
}

export function formatFullTime(iso: string): string {
  return format(new Date(iso), 'EEE, MMM d, p');
}

export function formatOptionTime(when: Date): string {
  return format(when, 'EEE p');
}

// ---------------------------------------------------------------------------
// People
// ---------------------------------------------------------------------------

export function displayName(addr: EmailAddress): string {
  return addr.name ?? addr.email.split('@')[0] ?? addr.email;
}

export function firstName(addr: EmailAddress): string {
  return displayName(addr).split(' ')[0] ?? displayName(addr);
}

export function initials(addr: EmailAddress): string {
  const source = addr.name ?? addr.email;
  const parts = source
    .replace(/@.*$/, '')
    .split(/[\s._-]+/)
    .filter(Boolean);
  const chars = `${parts[0]?.[0] ?? ''}${parts[1]?.[0] ?? ''}`;
  return (chars || source[0] || '?').toUpperCase();
}

/** "Priya, Jordan 3" — sender line for a dense thread row. */
export function participantsLine(thread: Thread, selfEmails?: ReadonlySet<string>): string {
  const others = thread.participants.filter((p) => !selfEmails?.has(p.email.toLowerCase()));
  const shown = (others.length > 0 ? others : thread.participants).slice(0, 3);
  return shown.map(firstName).join(', ');
}

// ---------------------------------------------------------------------------
// Snooze / reminder / send-later presets
// ---------------------------------------------------------------------------

export interface TimeOption {
  id: string;
  label: string;
  when: Date;
}

function at(base: Date, hours: number): Date {
  return set(base, { hours, minutes: 0, seconds: 0, milliseconds: 0 });
}

export function snoozeOptions(now: Date = new Date()): TimeOption[] {
  const tonight = at(now, 19);
  return [
    { id: 'tonight', label: 'Tonight', when: tonight > now ? tonight : addHours(now, 3) },
    { id: 'tomorrow', label: 'Tomorrow', when: at(addDays(now, 1), 8) },
    { id: 'weekend', label: 'This weekend', when: at(nextSaturday(now), 8) },
    { id: 'nextweek', label: 'Next week', when: at(nextMonday(now), 8) },
  ];
}

export function reminderOptions(now: Date = new Date()): TimeOption[] {
  return [
    { id: 'tomorrow', label: 'Tomorrow', when: at(addDays(now, 1), 8) },
    { id: '2days', label: 'In 2 days', when: at(addDays(now, 2), 8) },
    { id: '1week', label: 'In 1 week', when: at(addDays(now, 7), 8) },
    { id: '2weeks', label: 'In 2 weeks', when: at(addDays(now, 14), 8) },
  ];
}

export function sendLaterOptions(now: Date = new Date()): TimeOption[] {
  const tonight = at(now, 19);
  return [
    { id: 'tonight', label: 'Tonight 7 PM', when: tonight > now ? tonight : addHours(now, 2) },
    { id: 'tomorrow', label: 'Tomorrow 8 AM', when: at(addDays(now, 1), 8) },
    { id: 'afternoon', label: 'Tomorrow 1 PM', when: at(addDays(now, 1), 13) },
    { id: 'monday', label: 'Monday 8 AM', when: at(nextMonday(now), 8) },
  ];
}

// ---------------------------------------------------------------------------
// Cross-component mail actions (command palette → inbox page)
// ---------------------------------------------------------------------------

export type MailCommand =
  | 'archive'
  | 'snooze'
  | 'reminder'
  | 'star'
  | 'unread'
  | 'mark-read'
  | 'search';

export const MAIL_COMMAND_EVENT = 'calendium:mail-command';

export function dispatchMailCommand(command: MailCommand): void {
  window.dispatchEvent(new CustomEvent<MailCommand>(MAIL_COMMAND_EVENT, { detail: command }));
}

export function onMailCommand(handler: (command: MailCommand) => void): () => void {
  const listener = (event: Event) => handler((event as CustomEvent<MailCommand>).detail);
  window.addEventListener(MAIL_COMMAND_EVENT, listener);
  return () => window.removeEventListener(MAIL_COMMAND_EVENT, listener);
}

// A command triggered from another route can't be delivered as a DOM event —
// the mail page hasn't mounted its listener yet. Queue it here and let the mail
// page consume it on mount (durable, unlike a fixed setTimeout).
let pendingMailCommand: MailCommand | null = null;

export function queueMailCommand(command: MailCommand): void {
  pendingMailCommand = command;
}

export function takePendingMailCommand(): MailCommand | null {
  const command = pendingMailCommand;
  pendingMailCommand = null;
  return command;
}
