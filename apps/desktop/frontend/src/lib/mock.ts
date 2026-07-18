import type {
  AiClassifier,
  AiEventProposal,
  AiSource,
  Calendar,
  ClassifierInput,
  ConnectedAccount,
  Event,
  EventInput,
  InboxSplit,
  Message,
  Subscription,
  Thread,
  User,
} from '@calendium/shared';
import { addDays, addMinutes, setHours, setMinutes, startOfDay, subDays, subMinutes } from 'date-fns';

// ---------------------------------------------------------------------------
// Standalone mock data — used when no API is configured (see lib/api.ts) so
// the desktop app runs and demos end-to-end without a backend.
// ---------------------------------------------------------------------------

const now = new Date();
const iso = (d: Date) => d.toISOString();

export const mockUser: User = {
  id: 'usr_mock',
  email: 'ada@calendium.app',
  name: 'Ada Lovelace',
  avatarUrl: null,
  createdAt: iso(subDays(now, 42)),
};

export const mockAccounts: ConnectedAccount[] = [
  {
    id: 'acc_google',
    provider: 'google',
    email: 'ada@calendium.app',
    status: 'active',
    scopes: ['gmail', 'calendar'],
    vipSenders: [],
    lastSyncedAt: iso(subMinutes(now, 2)),
    createdAt: iso(subDays(now, 42)),
  },
  {
    id: 'acc_ms',
    provider: 'microsoft',
    email: 'ada@lovelace.dev',
    status: 'syncing',
    scopes: ['mail.read', 'calendars.readwrite'],
    vipSenders: [],
    lastSyncedAt: null,
    createdAt: iso(subDays(now, 3)),
  },
];

interface ThreadSeed {
  id: string;
  split: InboxSplit;
  subject: string;
  snippet: string;
  from: { name: string; email: string };
  agoMinutes: number;
  unread?: boolean;
  starred?: boolean;
  messageCount?: number;
  /** Scripted AI thread summary (see components/mail/thread-summary equivalent below). */
  summary?: string;
  /** Scripted AI instant-reply suggestions. */
  instantReplies?: string[];
}

const threadSeeds: ThreadSeed[] = [
  {
    id: 'thr_1',
    split: 'important',
    subject: 'Q3 roadmap review — final pass',
    snippet: 'I folded in the feedback from Tuesday; the calendar integration slice is now scoped to…',
    from: { name: 'Grace Hopper', email: 'grace@compilers.io' },
    agoMinutes: 18,
    unread: true,
    messageCount: 4,
    summary:
      'Grace incorporated Tuesday’s feedback into the Q3 roadmap: the calendar-integration slice is now scoped down, with the classifier work and desktop beta unchanged.',
    instantReplies: [
      'Looks great, thanks for the update.',
      'Can we discuss the calendar scope on our next call?',
      'Approved — let’s move forward.',
    ],
  },
  { id: 'thr_2', split: 'important', subject: 'Contract renewal — signature needed', snippet: 'The updated MSA is attached. One change to §4.2 (net-30 → net-45), everything else…', from: { name: 'Margaret Hamilton', email: 'margaret@apollo.dev' }, agoMinutes: 95, unread: true, starred: true, messageCount: 2 },
  { id: 'thr_3', split: 'vip', subject: 'Intro: Katherine ↔ Ada', snippet: 'Ada, meet Katherine — she led the trajectory work I mentioned. I think a 30-min…', from: { name: 'Dorothy Vaughan', email: 'dorothy@nasa.gov' }, agoMinutes: 240, messageCount: 3 },
  { id: 'thr_4', split: 'team', subject: 'Standup notes — Wednesday', snippet: 'Shipped: split-inbox classifier v2. Blocked: Graph delta tokens expiring early, needs…', from: { name: 'Alan Turing', email: 'alan@team.calendium.app' }, agoMinutes: 300, unread: true, messageCount: 1 },
  { id: 'thr_5', split: 'important', subject: 'Re: Latency budget for thread list', snippet: 'Got p95 under 80ms by mirroring bodies into Postgres and precomputing the split…', from: { name: 'Barbara Liskov', email: 'barbara@abstractions.org' }, agoMinutes: 26 * 60, starred: true, messageCount: 7 },
  { id: 'thr_6', split: 'calendar', subject: 'Invitation: Design review @ Thu 14:00', snippet: 'Grace Hopper invited you to Design review. Thursday · 14:00–15:00 · Google Meet…', from: { name: 'Google Calendar', email: 'calendar-notification@google.com' }, agoMinutes: 30 * 60, messageCount: 1 },
  { id: 'thr_7', split: 'news', subject: 'This week in systems — issue #214', snippet: 'io_uring turns eight, a deep dive on WebView2 memory, and why your p99 is lying to…', from: { name: 'Systems Weekly', email: 'digest@systemsweekly.dev' }, agoMinutes: 47 * 60, messageCount: 1 },
  { id: 'thr_8', split: 'social', subject: 'Ada, you have 3 new connection requests', snippet: 'People are noticing your work on analytical engines. See who wants to connect…', from: { name: 'LinkedIn', email: 'notifications@linkedin.com' }, agoMinutes: 50 * 60, messageCount: 1 },
  { id: 'thr_9', split: 'other', subject: 'Your receipt from Fig & Font', snippet: 'Receipt #8841 — total $23.40. Thanks for stopping by! View your order details…', from: { name: 'Fig & Font', email: 'receipts@figandfont.com' }, agoMinutes: 70 * 60, messageCount: 1 },
  { id: 'thr_10', split: 'vip', subject: 'Dinner Friday?', snippet: 'The place on Alder finally reopened — want to grab the 7:30 slot before it books…', from: { name: 'Charles Babbage', email: 'charles@difference.engine' }, agoMinutes: 8 * 60, unread: true, messageCount: 2 },
];

function seedToThread(seed: ThreadSeed): Thread {
  return {
    id: seed.id,
    accountId: 'acc_google',
    subject: seed.subject,
    snippet: seed.snippet,
    participants: [seed.from, { name: mockUser.name, email: mockUser.email }],
    labelIds: ['inbox'],
    split: seed.split,
    messageCount: seed.messageCount ?? 1,
    unread: seed.unread ?? false,
    starred: seed.starred ?? false,
    lastMessageAt: iso(subMinutes(now, seed.agoMinutes)),
    openedAt: null,
    snoozedUntil: null,
    remindAt: null,
    unsubscribeMailto: null,
    unsubscribeUrl: null,
    unsubscribeOneClick: false,
    summary: seed.summary,
    instantReplies: seed.instantReplies,
  };
}

export function mockThreads(split?: InboxSplit): Thread[] {
  const all = threadSeeds.map(seedToThread);
  return split ? all.filter((t) => t.split === split) : all;
}

export function mockThread(threadId: string): { thread: Thread; messages: Message[] } {
  const seed = threadSeeds.find((s) => s.id === threadId) ?? threadSeeds[0]!;
  const thread = seedToThread(seed);
  const count = Math.min(thread.messageCount, 3);
  const messages: Message[] = Array.from({ length: count }, (_, i) => {
    const fromMe = i % 2 === 1;
    const sender = fromMe ? { name: mockUser.name, email: mockUser.email } : seed.from;
    return {
      id: `${seed.id}_msg_${i + 1}`,
      threadId: seed.id,
      accountId: 'acc_google',
      from: sender,
      to: [fromMe ? seed.from : { name: mockUser.name, email: mockUser.email }],
      cc: [],
      bcc: [],
      subject: seed.subject,
      bodyHtml: `<p>${seed.snippet}</p><p>— ${sender.name}</p>`,
      bodyText: `${seed.snippet}\n\nHappy to walk through the details whenever suits — my calendar is up to date, grab any slot.\n\n— ${sender.name}`,
      attachments: [],
      sentAt: iso(subMinutes(now, seed.agoMinutes + (count - 1 - i) * 45)),
      isDraft: false,
      openedAt: fromMe ? iso(subMinutes(now, seed.agoMinutes)) : null,
    };
  });
  return { thread, messages };
}

export const mockCalendars: Calendar[] = [
  { id: 'cal_work', accountId: 'acc_google', name: 'Work', color: '#0ea5e9', timeZone: 'America/Sao_Paulo', isPrimary: true, isVisible: true, canWrite: true },
  { id: 'cal_personal', accountId: 'acc_google', name: 'Personal', color: '#22c55e', timeZone: 'America/Sao_Paulo', isPrimary: false, isVisible: true, canWrite: true },
];

function ev(id: string, calendarId: string, title: string, day: Date, startHour: number, startMinute: number, durationMinutes: number, extra?: Partial<Event>): Event {
  const start = setMinutes(setHours(startOfDay(day), startHour), startMinute);
  return {
    id,
    calendarId,
    title,
    description: null,
    location: null,
    start: iso(start),
    end: iso(addMinutes(start, durationMinutes)),
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [10],
    ...extra,
  };
}

// Demo-mode events created via the UI (calendar "New event" and ThreadPane's
// "Create event with AI") — kept in module state so a created event shows up
// in mockEvents/mockSearch for the rest of the session, the same pattern as
// mockClassifiers below.
let mockCreatedEvents: Event[] = [];
let nextMockEventId = 1;

/** Local fallback for POST /v1/events when no server is configured. */
export function createMockEvent(input: EventInput): Event {
  const event: Event = {
    id: `evt_local_${nextMockEventId++}`,
    calendarId: input.calendarId,
    title: input.title,
    description: input.description ?? null,
    location: input.location ?? null,
    start: input.start,
    end: input.end,
    allDay: input.allDay ?? false,
    recurrenceRule: input.recurrenceRule ?? null,
    attendees: (input.attendeeEmails ?? []).map((email) => ({
      email,
      name: null,
      response: 'needs_action',
      organizer: false,
      optional: false,
    })),
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: input.reminderMinutes ?? [10],
  };
  mockCreatedEvents = [...mockCreatedEvents, event];
  return { ...event };
}

export function mockEvents(fromIso: string, toIso: string): Event[] {
  const today = startOfDay(now);
  const events: Event[] = [
    ...mockCreatedEvents,
    ev('evt_1', 'cal_work', 'Team standup', subDays(today, 1), 9, 30, 15),
    ev('evt_2', 'cal_work', 'Design review', today, 14, 0, 60, {
      conferencing: { provider: 'meet', url: 'https://meet.google.com/abc-defg-hij' },
    }),
    ev('evt_3', 'cal_work', 'Team standup', today, 9, 30, 15),
    ev('evt_4', 'cal_personal', 'Gym', today, 18, 30, 60),
    ev('evt_5', 'cal_work', '1:1 with Grace', addDays(today, 1), 11, 0, 30),
    ev('evt_6', 'cal_work', 'Roadmap deep-dive', addDays(today, 1), 15, 0, 90),
    ev('evt_7', 'cal_work', 'Team standup', addDays(today, 2), 9, 30, 15),
    ev('evt_8', 'cal_personal', 'Dinner with Charles', addDays(today, 2), 19, 30, 120),
    ev('evt_9', 'cal_work', 'Interview — staff engineer', addDays(today, 3), 10, 0, 60),
    ev('evt_10', 'cal_personal', 'Flight to Recife', addDays(today, 5), 7, 45, 210),
    // Conference link sniffed from free-text location (vs. evt_2's structured
    // `conferencing` field) — exercises detectConference's location-fallback
    // path so the month/week Join control has both fixtures to render.
    ev('evt_11', 'cal_work', 'Vendor sync', today, 16, 0, 30, {
      location: 'Zoom: https://zoom.us/j/5551234567?pwd=abc',
    }),
    // Further out than the current week so the month view has something to
    // show beyond the 7 days the week view already covers.
    ev('evt_12', 'cal_work', 'Quarterly planning', addDays(today, 18), 13, 0, 90),
  ];
  const from = new Date(fromIso).getTime();
  const to = new Date(toIso).getTime();
  return events.filter((e) => new Date(e.start).getTime() >= from && new Date(e.start).getTime() <= to);
}

/** Substring search across mock threads + events (⌘K in demo mode). */
export function mockSearch(query: string): { threads: Thread[]; events: Event[] } {
  const q = query.toLowerCase();
  const threads = mockThreads().filter(
    (t) =>
      t.subject.toLowerCase().includes(q) ||
      t.snippet.toLowerCase().includes(q) ||
      t.participants.some(
        (p) => (p.name ?? '').toLowerCase().includes(q) || p.email.toLowerCase().includes(q)
      )
  );
  const events = mockEvents(subDays(now, 30).toISOString(), addDays(now, 30).toISOString()).filter(
    (e) => e.title.toLowerCase().includes(q)
  );
  return { threads, events };
}

// --- Billing: standalone demo of the Spotify desktop flow -------------------
// After startMockCheckout() the mock "webhook" lands ~8s later, so polling
// GET /v1/billing/subscription visibly flips none → active without a backend.

let mockCheckoutStartedAt: number | null = null;

export function startMockCheckout(): void {
  mockCheckoutStartedAt = Date.now();
}

export function mockSubscription(): Subscription {
  const paid = mockCheckoutStartedAt !== null && Date.now() - mockCheckoutStartedAt > 8_000;
  if (paid) {
    return {
      status: 'active',
      plan: 'annual',
      priceUsd: 50,
      currentPeriodEnd: iso(addDays(now, 365)),
      cancelAtPeriodEnd: false,
      trialEndsAt: null,
    };
  }
  return {
    status: 'none',
    plan: 'annual',
    priceUsd: 50,
    currentPeriodEnd: null,
    cancelAtPeriodEnd: false,
    trialEndsAt: null,
  };
}

// --- AI suite (M2.3) --------------------------------------------------------

/** Local fallback for GET .../instant-replies when no server is configured. */
export function mockInstantReplies(threadId: string): string[] {
  const seed = threadSeeds.find((s) => s.id === threadId);
  return (
    seed?.instantReplies ?? [
      'Sounds good, thanks!',
      'Let me look into this and follow up shortly.',
      'Can we push this to next week?',
    ]
  );
}

/** Local fallback for POST /v1/ai/ask (cited Q&A) when no server is configured. */
export function mockAiAskCited(
  question: string,
  threadId?: string
): { answer: string; model: string; sources: AiSource[] } {
  const seeds = threadId ? threadSeeds.filter((s) => s.id === threadId) : threadSeeds.slice(0, 2);
  const sources: AiSource[] = seeds.map((seed) => ({
    threadId: seed.id,
    subject: seed.subject,
    snippet: seed.snippet,
  }));
  const trimmed = question.trim().replace(/\?+$/, '');
  return {
    answer: trimmed
      ? `Based on your mailbox: ${trimmed} — it looks like it's on track. See the linked conversation${sources.length === 1 ? '' : 's'} below.`
      : "I couldn't find anything relevant in your mailbox for that question.",
    model: 'demo/local-fallback',
    sources,
  };
}

/** Local fallback for POST /v1/ai/event-proposal when no server is configured. */
export function mockProposeEvent(threadId: string): AiEventProposal {
  const seed = threadSeeds.find((s) => s.id === threadId);
  const start = addMinutes(startOfDay(addDays(now, 1)), 10 * 60);
  return {
    title: seed ? seed.subject.replace(/^(Re|Fwd):\s*/i, '') : 'Follow-up meeting',
    attendees: seed ? [seed.from.email] : [],
    start: iso(start),
    end: iso(addMinutes(start, 30)),
    notes: 'Proposed from thread context (demo mode).',
  };
}

let mockClassifiers: AiClassifier[] = [
  {
    id: 'clf_recruiter',
    name: 'Recruiter outreach',
    prompt: 'Cold outreach from a recruiter or staffing agency about a job opportunity.',
    targetSplit: 'other',
    labelName: 'Recruiting',
    enabled: true,
  },
];
let nextMockClassifierId = 1;

export function listMockClassifiers(): AiClassifier[] {
  return mockClassifiers.map((c) => ({ ...c }));
}

export function createMockClassifier(input: ClassifierInput): AiClassifier {
  const classifier: AiClassifier = { id: `clf_local_${nextMockClassifierId++}`, ...input };
  mockClassifiers = [classifier, ...mockClassifiers];
  return { ...classifier };
}

export function updateMockClassifier(id: string, input: ClassifierInput): AiClassifier {
  const updated: AiClassifier = { id, ...input };
  mockClassifiers = mockClassifiers.map((c) => (c.id === id ? updated : c));
  return { ...updated };
}

export function deleteMockClassifier(id: string): void {
  mockClassifiers = mockClassifiers.filter((c) => c.id !== id);
}
