// Deterministic mock data served ONLY in explicit demo mode ("Try the demo" on
// the connect screen). Outside demo mode the app never fabricates data: screens
// show honest loading / empty / error states and mutations surface failures.
import { addDays, dayKey, startOfDay } from '@/lib/format';
import {
  ApiRequestError,
  type AiComposeRequest,
  type AiComposeResponse,
  type Calendar,
  type ConnectedAccount,
  type EmailAddress,
  type Event,
  type EventTemplate,
  type InboxSplit,
  type Message,
  type Page,
  type Subscription,
  type Thread,
} from '@calendium/shared';

const NOW = Date.now();
const minutesAgo = (min: number) => new Date(NOW - min * 60_000).toISOString();
const daysFromNow = (days: number) => new Date(NOW + days * 86_400_000).toISOString();

const person = (name: string, email: string): EmailAddress => ({ name, email });

export const MOCK_ACCOUNT_ID = 'acct_mock_google';

/**
 * True when a request failure looks like "API unreachable" (network error,
 * DNS, timeout) rather than an API-level error response.
 */
export function isApiUnreachable(error: unknown): boolean {
  return !(error instanceof ApiRequestError);
}

// Explicit demo mode: set from lib/server-config when the user picks "Try the
// demo". Only in this mode does the app serve the deterministic mock data below;
// otherwise every screen talks to the real backend and tells the truth.
let demoMode = false;

/** Enables/disables explicit demo mode. Driven by the persisted server config. */
export function setDemoMode(enabled: boolean): void {
  demoMode = enabled;
}

/** True when the app is running the offline "Try the demo" experience. */
export function isDemoMode(): boolean {
  return demoMode;
}

/**
 * In demo mode, serves deterministic mock data without any backend. Outside demo
 * mode it is a pass-through to the real request, so honest loading/empty/error
 * states surface (no fabricated success).
 */
export async function withMockFallback<T>(request: () => Promise<T>, mock: () => T): Promise<T> {
  if (!demoMode) return request();
  return mock();
}

// ---------------------------------------------------------------------------
// Mail
// ---------------------------------------------------------------------------

function thread(partial: {
  id: string;
  subject: string;
  snippet: string;
  participants: EmailAddress[];
  split: InboxSplit;
  minutesAgo: number;
  messageCount?: number;
  unread?: boolean;
  starred?: boolean;
}): Thread {
  return {
    id: partial.id,
    accountId: MOCK_ACCOUNT_ID,
    subject: partial.subject,
    snippet: partial.snippet,
    participants: partial.participants,
    labelIds: [],
    split: partial.split,
    messageCount: partial.messageCount ?? 1,
    unread: partial.unread ?? false,
    starred: partial.starred ?? false,
    lastMessageAt: minutesAgo(partial.minutesAgo),
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
  };
}

export const mockThreads: Thread[] = [
  thread({
    id: 'thr_1',
    subject: 'Q3 planning — final review',
    snippet: 'I folded in the feedback from Friday. Two open questions before we lock it…',
    participants: [person('Sarah Chen', 'sarah@acme.com'), person('You', 'you@calendium.app')],
    split: 'important',
    minutesAgo: 12,
    messageCount: 4,
    unread: true,
  }),
  thread({
    id: 'thr_2',
    subject: 'Contract renewal: Acme Corp',
    snippet: 'Legal signed off on the redlines. Countersign by Thursday and we are done.',
    participants: [person('Marcus Webb', 'marcus@acme.com')],
    split: 'important',
    minutesAgo: 47,
    messageCount: 2,
    unread: true,
    starred: true,
  }),
  thread({
    id: 'thr_3',
    subject: 'Re: Onboarding flow feedback',
    snippet: 'The empty-state copy lands much better now. One nit on the progress bar…',
    participants: [person('Priya Patel', 'priya@studio.co'), person('You', 'you@calendium.app')],
    split: 'important',
    minutesAgo: 180,
    messageCount: 6,
  }),
  thread({
    id: 'thr_4',
    subject: 'Board deck v3 — comments inside',
    snippet: 'Slides 4 and 11 need the updated retention chart. Everything else is ready.',
    participants: [person('David Kim', 'david@fund.vc')],
    split: 'important',
    minutesAgo: 1560,
    messageCount: 3,
    starred: true,
  }),
  thread({
    id: 'thr_5',
    subject: 'Dinner next week?',
    snippet: 'We are in town Tue–Thu. Any of those evenings work for you two?',
    participants: [person('Alex Rivera', 'alex@rivera.me')],
    split: 'vip',
    minutesAgo: 120,
    unread: true,
  }),
  thread({
    id: 'thr_6',
    subject: 'Intro: Jordan ↔ you',
    snippet: 'Jordan is rebuilding their data platform and asked for someone who has…',
    participants: [person('Jamie Fox', 'jamie@network.io')],
    split: 'vip',
    minutesAgo: 1440,
    messageCount: 2,
  }),
  thread({
    id: 'thr_7',
    subject: 'Standup notes — Thursday',
    snippet: 'Shipped: push registration. Blocked: staging certs. Next: calendar sync…',
    participants: [person('Team Bot', 'bot@calendium.app')],
    split: 'team',
    minutesAgo: 300,
  }),
  thread({
    id: 'thr_8',
    subject: 'Design crit moved to 3pm',
    snippet: 'Room double-booked, so we grabbed the later slot. Same agenda, same link.',
    participants: [person('Mia Torres', 'mia@calendium.app')],
    split: 'team',
    minutesAgo: 65,
    unread: true,
  }),
  thread({
    id: 'thr_9',
    subject: 'Your weekly digest',
    snippet: 'How the best teams run incident reviews, and why smaller PRs win.',
    participants: [person('The Pragmatic Engineer', 'digest@pragmatic.dev')],
    split: 'news',
    minutesAgo: 480,
  }),
  thread({
    id: 'thr_10',
    subject: 'Launch week recap',
    snippet: 'The 10 most upvoted launches this week, plus a maker interview.',
    participants: [person('Product Hunt Daily', 'hello@producthunt.com')],
    split: 'news',
    minutesAgo: 1560,
  }),
  thread({
    id: 'thr_11',
    subject: 'Receipt #48812 from Linear',
    snippet: 'Your subscription payment of $96.00 was successful.',
    participants: [person('Linear', 'billing@linear.app')],
    split: 'other',
    minutesAgo: 240,
  }),
  thread({
    id: 'thr_12',
    subject: 'Your flight confirmation — GRU → SFO',
    snippet: 'Confirmation code K8LMQ2. Seat 14A. Departs 10:35 AM.',
    participants: [person('United Airlines', 'no-reply@united.com')],
    split: 'other',
    minutesAgo: 1320,
    unread: true,
    starred: true,
  }),
];

export function mockThreadPage(split: InboxSplit): Page<Thread> {
  return { items: mockThreads.filter((t) => t.split === split), nextCursor: null };
}

function messagesForThread(t: Thread): Message[] {
  const counterpart = t.participants[0] ?? person('Someone', 'someone@example.com');
  const me = person('You', 'you@calendium.app');
  const count = Math.max(1, Math.min(t.messageCount, 3));
  const lastAt = new Date(t.lastMessageAt).getTime();
  const stepMs = 45 * 60_000;
  return Array.from({ length: count }, (_, i) => {
    const fromMe = i % 2 === 1;
    const from = fromMe ? me : counterpart;
    const bodyText =
      i === count - 1
        ? `${t.snippet}\n\nBest,\n${from.name ?? from.email}`
        : `Hi,\n\nFollowing up on "${t.subject}" — thoughts below, let me know what you think.\n\nBest,\n${from.name ?? from.email}`;
    return {
      id: `${t.id}_msg_${i}`,
      threadId: t.id,
      accountId: t.accountId,
      from,
      to: [fromMe ? counterpart : me],
      cc: [],
      bcc: [],
      subject: t.subject,
      bodyHtml: `<p>${bodyText.replace(/\n/g, '<br/>')}</p>`,
      bodyText,
      attachments: [],
      sentAt: new Date(lastAt - (count - 1 - i) * stepMs).toISOString(),
      isDraft: false,
      openedAt: null,
    } satisfies Message;
  });
}

export function mockThreadDetail(threadId: string): { thread: Thread; messages: Message[] } {
  const found = mockThreads.find((t) => t.id === threadId);
  if (!found) {
    throw new ApiRequestError(404, 'not_found', 'Thread not found');
  }
  return { thread: found, messages: messagesForThread(found) };
}

// ---------------------------------------------------------------------------
// Calendar
// ---------------------------------------------------------------------------

export const mockCalendars: Calendar[] = [
  {
    id: 'cal_work',
    accountId: MOCK_ACCOUNT_ID,
    name: 'Work',
    color: '#3b82f6',
    timeZone: 'UTC',
    isPrimary: true,
    isVisible: true,
    canWrite: true,
  },
  {
    id: 'cal_personal',
    accountId: MOCK_ACCOUNT_ID,
    name: 'Personal',
    color: '#10b981',
    timeZone: 'UTC',
    isPrimary: false,
    isVisible: true,
    canWrite: true,
  },
];

// Named `MockDayTemplate` (not `EventTemplate`) to avoid colliding with the
// shared `EventTemplate` type (saved, user-facing event templates below) -
// this one is purely an internal generator shape for the day's schedule.
interface MockDayTemplate {
  title: string;
  hour: number;
  minute: number;
  durationMin: number;
  calendarId: string;
  weekdaysOnly?: boolean;
  location?: string;
  conferencing?: boolean;
}

const EVENT_TEMPLATES: MockDayTemplate[] = [
  { title: 'Team standup', hour: 9, minute: 30, durationMin: 15, calendarId: 'cal_work', weekdaysOnly: true, conferencing: true },
  { title: 'Product review', hour: 11, minute: 0, durationMin: 50, calendarId: 'cal_work', weekdaysOnly: true, conferencing: true },
  { title: 'Lunch', hour: 12, minute: 30, durationMin: 45, calendarId: 'cal_personal', location: 'Café Central' },
  { title: 'Deep work — inbox zero', hour: 14, minute: 0, durationMin: 90, calendarId: 'cal_work', weekdaysOnly: true },
  { title: 'Gym', hour: 18, minute: 15, durationMin: 60, calendarId: 'cal_personal' },
];

/** Deterministic events between two ISO instants (inclusive by day). */
export function mockEvents(fromIso: string, toIso: string): Event[] {
  const from = new Date(fromIso);
  const to = new Date(toIso);
  const events: Event[] = [];
  for (let day = startOfDay(from); day.getTime() <= to.getTime(); day = addDays(day, 1)) {
    const dow = day.getDay();
    EVENT_TEMPLATES.forEach((tpl, i) => {
      if (tpl.weekdaysOnly && (dow === 0 || dow === 6)) return;
      // Skip some personal events so days vary.
      if (tpl.calendarId === 'cal_personal' && (day.getDate() + i) % 4 === 0) return;
      const start = new Date(day);
      start.setHours(tpl.hour, tpl.minute, 0, 0);
      const end = new Date(start.getTime() + tpl.durationMin * 60_000);
      if (end.getTime() < from.getTime() || start.getTime() > to.getTime()) return;
      events.push({
        id: `evt_${dayKey(day)}_${i}`,
        calendarId: tpl.calendarId,
        title: tpl.title,
        description: null,
        location: tpl.location ?? null,
        start: start.toISOString(),
        end: end.toISOString(),
        allDay: false,
        recurrenceRule: null,
        attendees: [],
        conferencing: tpl.conferencing
          ? { provider: 'meet', url: 'https://meet.google.com/mock-link' }
          : null,
        status: 'confirmed',
        visibility: 'default',
        reminderMinutes: [10],
      });
    });
  }
  return events.sort((a, b) => a.start.localeCompare(b.start));
}

/**
 * Saved event templates ("1:1", "Focus block") for the create-event sheet's
 * template picker (Task 20, mirroring Task 17's web templates). Demo-mode
 * only - real templates come from `api.listEventTemplates()`.
 */
export const mockEventTemplates: EventTemplate[] = [
  {
    id: 'tpl_1_1',
    name: '1:1',
    title: '1:1',
    description: '',
    location: '',
    durationMinutes: 30,
    allDay: false,
    calendarId: 'cal_work',
    attendeeEmails: [],
    addConferencing: true,
    reminderMinutes: [10],
    recurrenceRule: null,
    usageCount: 12,
  },
  {
    id: 'tpl_focus',
    name: 'Focus block',
    title: 'Focus block',
    description: 'Heads-down work, no meetings.',
    location: '',
    durationMinutes: 90,
    allDay: false,
    calendarId: 'cal_work',
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [],
    recurrenceRule: null,
    usageCount: 8,
  },
  {
    id: 'tpl_coffee',
    name: 'Coffee chat',
    title: 'Coffee chat',
    description: '',
    location: 'Cafe Central',
    durationMinutes: 30,
    allDay: false,
    calendarId: 'cal_personal',
    attendeeEmails: [],
    addConferencing: false,
    reminderMinutes: [10],
    recurrenceRule: null,
    usageCount: 3,
  },
];

// ---------------------------------------------------------------------------
// Accounts, billing, AI
// ---------------------------------------------------------------------------

export const mockAccounts: ConnectedAccount[] = [
  {
    id: MOCK_ACCOUNT_ID,
    provider: 'google',
    email: 'you@gmail.com',
    status: 'active',
    scopes: ['gmail.modify', 'calendar'],
    vipSenders: [],
    lastSyncedAt: minutesAgo(4),
    createdAt: daysFromNow(-30),
  },
];

export const mockSubscription: Subscription = {
  status: 'trialing',
  plan: 'annual',
  priceUsd: 50,
  currentPeriodEnd: null,
  cancelAtPeriodEnd: false,
  trialEndsAt: daysFromNow(9),
};

export function mockAiCompose(req: AiComposeRequest): AiComposeResponse {
  const text =
    req.action === 'summarize'
      ? 'Summary: the sender needs a decision on the two open items before Thursday; everything else is already agreed.'
      : `Hi,\n\nThanks for the note — ${req.prompt.trim().replace(/\.$/, '')}.\n\nHappy to move this forward; does Thursday afternoon work on your end?\n\nBest,\nSent with Calendium`;
  return { text, model: 'mock/offline' };
}
