import { nextAfterRemoval, type Thread, type UnsubscribeResult } from '@calendium/shared';

export { nextAfterRemoval };

export interface SnoozePreset {
  label: string;
  until: Date;
}

/** Canonical snooze presets shared by inbox swipe/long-press and the thread reader. */
export function snoozePresets(now: Date = new Date()): SnoozePreset[] {
  const laterToday = new Date(now.getTime() + 3 * 60 * 60 * 1000);
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(8, 0, 0, 0);
  const nextWeek = new Date(now);
  nextWeek.setDate(nextWeek.getDate() + 7);
  nextWeek.setHours(8, 0, 0, 0);
  return [
    { label: 'Later today', until: laterToday },
    { label: 'Tomorrow 8 AM', until: tomorrow },
    { label: 'Next week', until: nextWeek },
  ];
}

/** Get Me To Zero cutoffs for the mobile action sheet. */
export function zeroCutoffs(now: Date = new Date()): { id: string; label: string; iso: string }[] {
  const day = 24 * 3_600_000;
  return [
    { id: 'week', label: 'Older than 1 week', iso: new Date(now.getTime() - 7 * day).toISOString() },
    { id: 'two-weeks', label: 'Older than 2 weeks', iso: new Date(now.getTime() - 14 * day).toISOString() },
    { id: 'month', label: 'Older than 1 month', iso: new Date(now.getTime() - 30 * day).toISOString() },
    { id: 'quarter', label: 'Older than 3 months', iso: new Date(now.getTime() - 90 * day).toISOString() },
  ];
}

export function canUnsubscribe(thread: Pick<Thread, 'unsubscribeMailto' | 'unsubscribeUrl'>): boolean {
  return Boolean(thread.unsubscribeMailto || thread.unsubscribeUrl);
}

/** Human copy for an unsubscribe result, shared by the confirmation toast. */
export function unsubscribeMessage(res: UnsubscribeResult): string {
  return res.method === 'link'
    ? 'Opening the unsubscribe page…'
    : 'Unsubscribed — the sender has been asked to stop.';
}
