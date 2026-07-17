import type { EmailAddress, InboxSplit, Thread } from '@calendium/shared';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  MAIL_COMMAND_EVENT,
  dispatchMailCommand,
  displayName,
  firstName,
  formatFullTime,
  formatListTime,
  formatOptionTime,
  initials,
  onMailCommand,
  participantsLine,
  queueMailCommand,
  reminderOptions,
  sendLaterOptions,
  snoozeOptions,
  takePendingMailCommand,
  zeroCutoffOptions,
  type MailCommand,
} from '@/lib/mail-utils';

function addr(email: string, name: string | null = null): EmailAddress {
  return { email, name };
}

function makeThread(participants: EmailAddress[], overrides: Partial<Thread> = {}): Thread {
  return {
    id: 't1',
    accountId: 'a1',
    subject: 'Subject',
    snippet: 'snippet',
    participants,
    labelIds: [],
    split: 'other' as InboxSplit,
    messageCount: 1,
    unread: false,
    starred: false,
    lastMessageAt: new Date().toISOString(),
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
    ...overrides,
  };
}

describe('formatListTime', () => {
  const REAL_NOW = new Date(2024, 5, 15, 12, 0, 0);

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(REAL_NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('formats a time-of-day for a message from today', () => {
    const iso = new Date(2024, 5, 15, 9, 5, 0).toISOString();
    expect(formatListTime(iso)).toMatch(/9:05\s*AM/i);
  });

  it('formats "MMM d" for a message earlier this year', () => {
    const iso = new Date(2024, 2, 3, 9, 0, 0).toISOString();
    expect(formatListTime(iso)).toBe('Mar 3');
  });

  it('formats "M/d/yy" for a message from a previous year', () => {
    const iso = new Date(2022, 10, 20, 9, 0, 0).toISOString();
    expect(formatListTime(iso)).toBe('11/20/22');
  });
});

describe('formatFullTime', () => {
  it('formats the full weekday, date, and time', () => {
    const iso = new Date(2024, 5, 15, 9, 5, 0).toISOString();
    expect(formatFullTime(iso)).toMatch(/^Sat, Jun 15, 9:05\s*AM$/i);
  });
});

describe('formatOptionTime', () => {
  it('formats a weekday and time-of-day', () => {
    const when = new Date(2024, 5, 15, 19, 0, 0);
    expect(formatOptionTime(when)).toMatch(/^Sat 7:00\s*PM$/i);
  });
});

describe('displayName / firstName / initials', () => {
  it('prefers the display name when present', () => {
    expect(displayName(addr('ana@example.com', 'Ana Silva'))).toBe('Ana Silva');
  });

  it('falls back to the local part of the email when no name', () => {
    expect(displayName(addr('jordan@example.com'))).toBe('jordan');
  });

  it('returns the first token of the display name', () => {
    expect(firstName(addr('ana@example.com', 'Ana Silva'))).toBe('Ana');
  });

  it('returns the whole local part as firstName when there is no space', () => {
    expect(firstName(addr('jordan@example.com'))).toBe('jordan');
  });

  it('builds initials from a two-word name', () => {
    expect(initials(addr('ana@example.com', 'Ana Silva'))).toBe('AS');
  });

  it('builds initials from an email local-part with separators', () => {
    expect(initials(addr('jordan.lee@example.com'))).toBe('JL');
  });

  it('falls back to a single character when nothing else is available', () => {
    expect(initials(addr('j@example.com'))).toBe('J');
  });
});

describe('participantsLine', () => {
  it('excludes self addresses and lists first names of others', () => {
    const thread = makeThread([
      addr('me@example.com', 'Me'),
      addr('priya@example.com', 'Priya Patel'),
      addr('jordan@example.com', 'Jordan Lee'),
    ]);
    const selfEmails = new Set(['me@example.com']);
    expect(participantsLine(thread, selfEmails)).toBe('Priya, Jordan');
  });

  it('caps the list at three participants', () => {
    const thread = makeThread([
      addr('a@example.com', 'Ada Lovelace'),
      addr('b@example.com', 'Bob Ross'),
      addr('c@example.com', 'Cara Dune'),
      addr('d@example.com', 'Dana Scully'),
    ]);
    expect(participantsLine(thread)).toBe('Ada, Bob, Cara');
  });

  it('falls back to all participants when everyone is a self address', () => {
    const thread = makeThread([addr('me@example.com', 'Me')]);
    const selfEmails = new Set(['me@example.com']);
    expect(participantsLine(thread, selfEmails)).toBe('Me');
  });

  it('matches self emails case-insensitively', () => {
    const thread = makeThread([
      addr('Me@Example.com', 'Me'),
      addr('priya@example.com', 'Priya Patel'),
    ]);
    const selfEmails = new Set(['me@example.com']);
    expect(participantsLine(thread, selfEmails)).toBe('Priya');
  });
});

describe('snoozeOptions / reminderOptions / sendLaterOptions', () => {
  const now = new Date(2024, 5, 12, 10, 0, 0); // Wednesday, June 12 2024, 10:00

  it('snoozeOptions offers tonight (still ahead), tomorrow, weekend, and next week', () => {
    const options = snoozeOptions(now);
    expect(options.map((o) => o.id)).toEqual(['tonight', 'tomorrow', 'weekend', 'nextweek']);
    const tonight = options[0]!;
    expect(tonight.when.getHours()).toBe(19);
    expect(tonight.when.getDate()).toBe(now.getDate());
    const tomorrow = options[1]!;
    expect(tomorrow.when.getDate()).toBe(now.getDate() + 1);
    expect(tomorrow.when.getHours()).toBe(8);
  });

  it('snoozeOptions falls back to +3h when 7pm has already passed today', () => {
    const lateNow = new Date(2024, 5, 12, 20, 0, 0);
    const options = snoozeOptions(lateNow);
    const tonight = options[0]!;
    expect(tonight.when.getTime()).toBe(lateNow.getTime() + 3 * 60 * 60 * 1000);
  });

  it('reminderOptions offers tomorrow, 2 days, 1 week, and 2 weeks at 8am', () => {
    const options = reminderOptions(now);
    expect(options.map((o) => o.id)).toEqual(['tomorrow', '2days', '1week', '2weeks']);
    for (const option of options) {
      expect(option.when.getHours()).toBe(8);
    }
    expect(options[2]!.when.getDate()).toBe(now.getDate() + 7);
  });

  it('sendLaterOptions offers tonight, tomorrow morning, tomorrow afternoon, and monday', () => {
    const options = sendLaterOptions(now);
    expect(options.map((o) => o.id)).toEqual(['tonight', 'tomorrow', 'afternoon', 'monday']);
    expect(options[0]!.when.getHours()).toBe(19);
    expect(options[2]!.when.getHours()).toBe(13);
  });

  it('sendLaterOptions falls back to +2h when 7pm has already passed today', () => {
    const lateNow = new Date(2024, 5, 12, 20, 0, 0);
    const options = sendLaterOptions(lateNow);
    expect(options[0]!.when.getTime()).toBe(lateNow.getTime() + 2 * 60 * 60 * 1000);
  });

  it('defaults to the current time when no now is passed', () => {
    expect(() => snoozeOptions()).not.toThrow();
    expect(() => reminderOptions()).not.toThrow();
    expect(() => sendLaterOptions()).not.toThrow();
  });
});

describe('zeroCutoffOptions', () => {
  it('returns week/two-weeks/month/quarter cutoffs in the past', () => {
    const now = new Date('2026-07-17T12:00:00Z');
    const options = zeroCutoffOptions(now);
    expect(options.map((o) => o.id)).toEqual(['week', 'two-weeks', 'month', 'quarter']);
    for (const option of options) {
      expect(option.when.getTime()).toBeLessThan(now.getTime());
    }
    expect(options[0]!.when.toISOString()).toBe('2026-07-10T12:00:00.000Z');
  });
});

describe('mail command bus', () => {
  afterEach(() => {
    takePendingMailCommand();
  });

  it('dispatches and receives a command via the DOM event', () => {
    const handler = vi.fn();
    const unsubscribe = onMailCommand(handler);
    dispatchMailCommand('archive');
    expect(handler).toHaveBeenCalledWith('archive');
    unsubscribe();
  });

  it('stops receiving commands after unsubscribing', () => {
    const handler = vi.fn();
    const unsubscribe = onMailCommand(handler);
    unsubscribe();
    dispatchMailCommand('star');
    expect(handler).not.toHaveBeenCalled();
  });

  it('fires the event under the expected event name', () => {
    const handler = vi.fn();
    window.addEventListener(MAIL_COMMAND_EVENT, handler as EventListener);
    dispatchMailCommand('search');
    expect(handler).toHaveBeenCalledTimes(1);
    window.removeEventListener(MAIL_COMMAND_EVENT, handler as EventListener);
  });

  it('queues a command and hands it back exactly once', () => {
    expect(takePendingMailCommand()).toBeNull();
    queueMailCommand('snooze');
    expect(takePendingMailCommand()).toBe('snooze' satisfies MailCommand);
    expect(takePendingMailCommand()).toBeNull();
  });

  it('overwrites a previously queued command', () => {
    queueMailCommand('archive');
    queueMailCommand('mark-read');
    expect(takePendingMailCommand()).toBe('mark-read');
  });
});
