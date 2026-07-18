import type {
  AiComposeRequest,
  AiComposeResponse,
  AiEventProposal,
  AiSource,
  EmailAddress,
  InboxSplit,
  Label,
  Message,
  OpenEvent,
  Page,
  Thread,
  ThreadAction,
  UnsubscribeResult,
} from '@calendium/shared';

import type { MailboxView } from '@/lib/mail-utils';

/**
 * Offline demo dataset — used whenever the Calendium API is unreachable so the
 * whole mail client stays explorable. Mutations (archive, star, snooze…) are
 * applied to this in-memory store, so demo state survives refetches within a
 * session.
 */

export const MOCK_ME: EmailAddress = { name: 'You', email: 'me@calendium.app' };
const ACCOUNT_ID = 'acc_demo';

const p = (name: string, email: string): EmailAddress => ({ name, email });

const priya = p('Priya Sharma', 'priya@calendium.app');
const daniel = p('Daniel Cho', 'daniel.cho@northwind.com');
const tom = p('Tom Bakker', 'tom@bakker.vc');
const maya = p('Maya Lindqvist', 'maya@sequoia.com');
const laura = p('Laura Kim', 'laura@calendium.app');
const ravi = p('Ravi Patel', 'ravi@calendium.app');
const aws = p('AWS Billing', 'no-reply@billing.aws.amazon.com');
const sofia = p('Sofia Almeida', 'sofia.almeida@gmail.com');
const elena = p('Elena Ruiz', 'elena@calendium.app');
const patrick = p('Patrick McKenzie', 'patrick@kalzumeus.com');
const lucas = p('Lucas Ferreira', 'lucas.ferreira@gmail.com');
const meg = p('Meg Foster', 'meg@calendium.app');
const jordan = p('Jordan Lee', 'jordan@calendium.app');
const ana = p('Ana Duarte', 'ana@calendium.app');
const chen = p('Chen Wei', 'chen@calendium.app');
const gcal = p('Google Calendar', 'calendar-notification@google.com');
const batch = p('The Batch', 'thebatch@deeplearning.ai');
const stratechery = p('Stratechery', 'email@stratechery.com');
const changelog = p('Changelog News', 'news@changelog.com');
const producthunt = p('Product Hunt Daily', 'hello@producthunt.com');
const linear = p('Linear', 'receipts@linear.app');
const ups = p('UPS', 'mcinfo@ups.com');
const github = p('GitHub', 'noreply@github.com');

const NOW = Date.now();
const hoursAgo = (h: number) => new Date(NOW - h * 3_600_000).toISOString();

interface MsgSpec {
  from: EmailAddress;
  hoursAgo: number;
  text: string;
  /** Hours-ago the recipient opened it (read receipts on your sent mail). */
  openedHoursAgo?: number;
  to?: EmailAddress[];
  cc?: EmailAddress[];
}

interface ThreadSpec {
  id: string;
  split: InboxSplit;
  subject: string;
  unread?: boolean;
  starred?: boolean;
  participants: EmailAddress[];
  msgs: MsgSpec[];
  unsubscribe?: { mailto?: string; url?: string; oneClick?: boolean };
  /** Pre-baked AI thread summary — scripted so Playwright runs keyless. */
  summary?: string;
  /** Pre-baked AI instant-reply suggestions — same reason. */
  instantReplies?: string[];
}

function toHtml(text: string): string {
  return text
    .split(/\n{2,}/)
    .map((para) => `<p>${para.replaceAll('\n', '<br/>')}</p>`)
    .join('');
}

function firstLine(text: string): string {
  const clean = text
    .split('\n')
    .filter((l) => !l.startsWith('>') && !/^On .+ wrote:$/.test(l.trim()))
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim();
  return clean.length > 110 ? `${clean.slice(0, 110)}…` : clean;
}

interface ThreadRecord {
  thread: Thread;
  messages: Message[];
  archived: boolean;
  trashed: boolean;
}

function build(spec: ThreadSpec): ThreadRecord {
  const sorted = [...spec.msgs].sort((a, b) => b.hoursAgo - a.hoursAgo);
  const messages: Message[] = sorted.map((m, i) => ({
    id: `${spec.id}_m${i + 1}`,
    threadId: spec.id,
    accountId: ACCOUNT_ID,
    from: m.from,
    to: m.to ?? (m.from.email === MOCK_ME.email ? spec.participants : [MOCK_ME]),
    cc: m.cc ?? [],
    bcc: [],
    subject: spec.subject,
    bodyHtml: toHtml(m.text),
    bodyText: m.text,
    attachments: [],
    sentAt: hoursAgo(m.hoursAgo),
    isDraft: false,
    openedAt: m.openedHoursAgo !== undefined ? hoursAgo(m.openedHoursAgo) : null,
    reactions: [],
  }));
  const last = messages[messages.length - 1]!;
  return {
    archived: false,
    trashed: false,
    messages,
    thread: {
      id: spec.id,
      accountId: ACCOUNT_ID,
      subject: spec.subject,
      snippet: firstLine(last.bodyText),
      participants: spec.participants,
      labelIds: ['inbox'],
      split: spec.split,
      messageCount: messages.length,
      unread: spec.unread ?? false,
      starred: spec.starred ?? false,
      lastMessageAt: last.sentAt,
      openedAt: spec.unread ? null : hoursAgo(0),
      snoozedUntil: null,
      remindAt: null,
      unsubscribeMailto: spec.unsubscribe?.mailto ?? null,
      unsubscribeUrl: spec.unsubscribe?.url ?? null,
      unsubscribeOneClick: spec.unsubscribe?.oneClick ?? false,
      summary: spec.summary,
      instantReplies: spec.instantReplies,
    },
  };
}

const SPECS: ThreadSpec[] = [
  // --- Important -----------------------------------------------------------
  {
    id: 'thr_01',
    split: 'important',
    subject: 'Postmortem: checkout latency spike',
    unread: true,
    participants: [priya, chen],
    summary:
      'Checkout p99 latency spiked to 2.4s from a synchronous Redis call in the rate limiter; a load-shedding fallback and an async-by-default limiter are done, and the incident is now marked resolved.',
    instantReplies: [
      'Thanks for the update — looks resolved.',
      'Great work resolving this so fast.',
      'Can we schedule a follow-up review next week?',
    ],
    msgs: [
      {
        from: priya,
        hoursAgo: 26,
        text: 'Heads up — p99 on checkout jumped from 180ms to 2.4s between 14:05 and 14:40 UTC. Root cause looks like the new rate limiter defaulting to synchronous Redis calls.\n\nDraft postmortem is in Notion. Can you review the action items before tomorrow?',
      },
      {
        from: MOCK_ME,
        hoursAgo: 20,
        openedHoursAgo: 18,
        text: 'Reviewed. Two asks: add a load-shedding fallback when Redis is degraded, and let’s make the limiter async-by-default.\n\nOn Tue, Jul 1, Priya Sharma wrote:\n> Heads up — p99 on checkout jumped from 180ms to 2.4s between 14:05 and 14:40 UTC.\n> Draft postmortem is in Notion.',
      },
      {
        from: priya,
        hoursAgo: 2,
        text: 'Both added, and Chen is picking up the async limiter this sprint. Marking the incident resolved — final doc attached to the Notion page.\n\nOn Wed, Jul 2, You wrote:\n> Two asks: add a load-shedding fallback when Redis is degraded,\n> and let’s make the limiter async-by-default.',
      },
    ],
  },
  {
    id: 'thr_02',
    split: 'important',
    subject: 'Renewal terms for FY27 — need your sign-off',
    unread: true,
    starred: true,
    participants: [daniel],
    msgs: [
      {
        from: daniel,
        hoursAgo: 5,
        text: 'Hi — legal cleared the redlines on our side. The only open item is the 12% uplift cap in section 4.2; we’d like it at 8%.\n\nIf you can sign off by Thursday we’ll get this executed before the quarter closes. Summary of changes attached.',
      },
    ],
  },
  {
    id: 'thr_03',
    split: 'important',
    subject: 'Intro: Maya (Sequoia) <> Calendium',
    participants: [tom, maya],
    msgs: [
      {
        from: tom,
        hoursAgo: 30,
        text: 'Maya — meet the team behind Calendium, the fastest email + calendar client I’ve used since Superhuman. They’re raising later this year.\n\nI’ll let you two take it from here.',
        to: [maya],
        cc: [MOCK_ME],
      },
      {
        from: MOCK_ME,
        hoursAgo: 8,
        openedHoursAgo: 3,
        text: 'Thanks Tom (to bcc). Maya — great to meet you. Happy to walk you through the product and our retention numbers; how does Thursday afternoon look?\n\nOn Mon, Jun 30, Tom Bakker wrote:\n> Maya — meet the team behind Calendium, the fastest email + calendar client\n> I’ve used since Superhuman.',
        to: [maya],
      },
    ],
  },
  {
    id: 'thr_04',
    split: 'important',
    subject: 'Offer letter — Senior Product Designer (Camila R.)',
    starred: true,
    participants: [laura],
    msgs: [
      {
        from: laura,
        hoursAgo: 26,
        text: 'Offer draft is ready: L5, $185k base, 0.35% over 4 years, start date Aug 3. Comp band checks out against our last two design hires.\n\nNeed your approval before I send it tonight — she has a competing offer expiring Friday.',
      },
    ],
  },
  {
    id: 'thr_05',
    split: 'important',
    subject: 'Q3 planning doc — comments by Friday',
    unread: true,
    participants: [ravi],
    msgs: [
      {
        from: ravi,
        hoursAgo: 31,
        text: 'Q3 planning doc is up. Big bets: calendar sharing links, split-inbox AI pass, and the desktop app beta.\n\nPlease leave comments by Friday EOD — we lock scope at Monday’s leads sync.',
      },
    ],
  },
  {
    id: 'thr_06',
    split: 'important',
    subject: 'Your AWS bill increased 42% month-over-month',
    participants: [aws],
    msgs: [
      {
        from: aws,
        hoursAgo: 50,
        text: 'Your estimated charges for June are $8,412.90, a 42% increase from May.\n\nTop contributors: Amazon RDS ($3,102), NAT Gateway data processing ($1,286), CloudWatch Logs ingestion ($904). View the Cost Explorer report for details.',
      },
    ],
  },

  // --- VIP -------------------------------------------------------------------
  {
    id: 'thr_07',
    split: 'vip',
    subject: 'Dinner Saturday?',
    unread: true,
    participants: [sofia],
    msgs: [
      {
        from: sofia,
        hoursAgo: 1,
        text: 'That new Peruvian place on Augusta finally has openings — I grabbed 8pm Saturday before they disappeared.\n\nAlso your mom called, she says you never answer your phone. Love you.',
      },
    ],
  },
  {
    id: 'thr_08',
    split: 'vip',
    subject: 'Board deck v3 — final pass tonight',
    unread: true,
    starred: true,
    participants: [elena],
    msgs: [
      {
        from: elena,
        hoursAgo: 7,
        text: 'v3 is in the drive folder. I rewrote the retention slide around the 68% M6 number and moved hiring to the appendix.\n\nCan you do a final pass on slides 4–9 tonight? Board call is 9am sharp.',
      },
    ],
  },
  {
    id: 'thr_09',
    split: 'vip',
    subject: 'Re: advice on pricing page',
    participants: [patrick],
    msgs: [
      {
        from: MOCK_ME,
        hoursAgo: 49,
        openedHoursAgo: 40,
        text: 'Patrick — we’re debating a single $50/yr plan vs. tiering. Any strong opinions from your Stripe days?',
        to: [patrick],
      },
      {
        from: patrick,
        hoursAgo: 22,
        text: 'Single plan, no question — at your stage pricing complexity is a tax on every conversation. Anchor the annual price against “two coffees a month” and spend your energy on the trial-to-paid moment instead.\n\nOn Fri, Jun 27, You wrote:\n> we’re debating a single $50/yr plan vs. tiering.\n> Any strong opinions from your Stripe days?',
      },
    ],
  },
  {
    id: 'thr_10',
    split: 'vip',
    subject: 'Ski trip dates — lock them in',
    participants: [lucas],
    msgs: [
      {
        from: lucas,
        hoursAgo: 70,
        text: 'Cabin is available Feb 12–16 or Feb 19–23. The second window is cheaper but the first has better snow history.\n\nNeed headcount by Sunday to put the deposit down. You in?',
      },
    ],
  },

  // --- Team --------------------------------------------------------------
  {
    id: 'thr_11',
    split: 'team',
    subject: 'Standup notes — Wednesday',
    participants: [meg],
    msgs: [
      {
        from: meg,
        hoursAgo: 4,
        text: 'Shipped: draft autosave, snooze wake-ups on worker. In progress: thread virtualization, Graph delta sync. Blocked: APNs cert renewal (waiting on Apple).\n\nFull notes in #standup.',
      },
    ],
  },
  {
    id: 'thr_12',
    split: 'team',
    subject: 'PR #482: split-inbox classifier — review requested',
    unread: true,
    participants: [jordan],
    msgs: [
      {
        from: jordan,
        hoursAgo: 6,
        text: 'This moves classification to ingest time and adds the header heuristics we discussed (list-unsubscribe, sender domain, invite detection). +412 −180 across mailsync.\n\nAccuracy on the labeled set went from 81% → 93%. Would love your eyes on the tie-breaking logic in classifier.go.',
      },
    ],
  },
  {
    id: 'thr_13',
    split: 'team',
    subject: 'Design crit Thursday: thread view',
    participants: [ana, meg, jordan],
    msgs: [
      {
        from: ana,
        hoursAgo: 53,
        text: 'Posting three directions for the thread view in Figma: classic stacked cards, flat Superhuman-style stream, and a hybrid with sticky sender rail.\n\nCrit is Thursday 11am — come with opinions.',
      },
      {
        from: jordan,
        hoursAgo: 48,
        text: 'Early vote: the flat stream. The cards feel heavy once a thread passes 5 messages.\n\nOn Mon, Jun 30, Ana Duarte wrote:\n> Posting three directions for the thread view in Figma.',
      },
      {
        from: MOCK_ME,
        hoursAgo: 30,
        openedHoursAgo: 26,
        text: 'Agree with Jordan, flat stream — but keep the collapsed quoted text affordance from the hybrid. Quoting noise is the #1 complaint in user interviews.\n\nOn Mon, Jun 30, Jordan Lee wrote:\n> Early vote: the flat stream.',
      },
      {
        from: ana,
        hoursAgo: 24,
        text: 'Done — merged the two into direction D, updated in Figma. See you Thursday.\n\nOn Tue, Jul 1, You wrote:\n> keep the collapsed quoted text affordance from the hybrid.',
      },
    ],
  },
  {
    id: 'thr_14',
    split: 'team',
    subject: 'Retro doc + action items',
    participants: [meg],
    msgs: [
      {
        from: meg,
        hoursAgo: 47,
        text: 'Retro highlights: release train worked, incident comms didn’t. Three action items assigned — you own “define sev1 escalation path” (due July 11).\n\nDoc: notion.so/calendium/retro-june',
      },
    ],
  },
  {
    id: 'thr_15',
    split: 'team',
    subject: 'On-call handoff: 3 open alerts',
    participants: [chen],
    msgs: [
      {
        from: chen,
        hoursAgo: 72,
        text: 'Handing off on-call. Open: Gmail sync lag on 2 large accounts (watching), flaky APNs timeouts (retry queue holding), disk at 78% on db-replica-2 (expand scheduled Monday).\n\nRunbook links in the pager notes. Quiet week otherwise.',
      },
    ],
  },

  // --- Calendar ------------------------------------------------------------
  {
    id: 'thr_16',
    split: 'calendar',
    subject: 'Invitation: Product review @ Thu, Jul 9 2pm',
    unread: true,
    participants: [gcal, ana],
    msgs: [
      {
        from: gcal,
        hoursAgo: 9,
        text: 'Ana Duarte has invited you to Product review.\n\nThursday, July 9 · 2:00–3:00pm — Google Meet. Agenda: thread view direction D, calendar sharing links, mobile beta feedback.',
      },
    ],
  },
  {
    id: 'thr_17',
    split: 'calendar',
    subject: 'Updated: 1:1 with Elena moved to 4pm',
    participants: [gcal, elena],
    msgs: [
      {
        from: gcal,
        hoursAgo: 28,
        text: 'This event was updated. 1:1 Elena / You now starts Wednesday, July 8 at 4:00pm (was 2:30pm). Location unchanged — Elena’s office / Meet.',
      },
    ],
  },
  {
    id: 'thr_18',
    split: 'calendar',
    subject: 'Accepted: Roadmap sync — Jordan Lee',
    participants: [gcal, jordan],
    msgs: [
      {
        from: gcal,
        hoursAgo: 52,
        text: 'Jordan Lee has accepted this invitation: Roadmap sync, Friday, July 10 · 10:00–10:45am.\n\n4 yes, 1 awaiting: Ravi Patel.',
      },
    ],
  },

  // --- News ---------------------------------------------------------------
  {
    id: 'thr_19',
    split: 'news',
    subject: 'The Batch: Agents that plan before they act',
    participants: [batch],
    unsubscribe: {
      mailto: 'mailto:unsub@deeplearning.ai',
      url: 'https://deeplearning.ai/the-batch/unsubscribe',
      oneClick: true,
    },
    msgs: [
      {
        from: batch,
        hoursAgo: 10,
        text: 'This week: planning-first agent architectures outperform ReAct-style loops on long-horizon tasks; a new benchmark for tool-use reliability; and why context caching is quietly reshaping inference costs.\n\nRead time: 7 minutes.',
      },
    ],
  },
  {
    id: 'thr_20',
    split: 'news',
    subject: 'Stratechery: Aggregation theory and the AI browser',
    unread: true,
    participants: [stratechery],
    unsubscribe: {
      mailto: 'mailto:unsub@stratechery.com',
      url: 'https://stratechery.com/unsubscribe',
      oneClick: true,
    },
    msgs: [
      {
        from: stratechery,
        hoursAgo: 33,
        text: 'The browser wars are back, but the moat has moved: it’s no longer distribution, it’s the agent’s memory of you. Today’s article looks at what aggregation theory predicts for AI-native browsers — and who is best positioned.',
      },
    ],
  },
  {
    id: 'thr_21',
    split: 'news',
    subject: 'Changelog News #578 — local-first strikes back',
    participants: [changelog],
    unsubscribe: { url: 'https://changelog.com/news/unsubscribe' },
    msgs: [
      {
        from: changelog,
        hoursAgo: 55,
        text: 'In this issue: CRDTs land in two mainstream ORMs, Bun 2.0 RC ships a built-in test runner overhaul, and a spicy take on why your sync engine is your product.\n\nPlus 12 more links worth your time.',
      },
    ],
  },
  {
    id: 'thr_22',
    split: 'news',
    subject: 'Product Hunt Daily: today’s top launches',
    participants: [producthunt],
    unsubscribe: { mailto: 'mailto:unsub@producthunt.com' },
    msgs: [
      {
        from: producthunt,
        hoursAgo: 79,
        text: '#1 Tempo — AI meeting notes that write your follow-ups. #2 Driftless — focus timer that blocks Slack. #3 Papertrail — receipts inbox for finance teams.\n\nSee all 20 launches →',
      },
    ],
  },

  // --- Other ---------------------------------------------------------------
  {
    id: 'thr_23',
    split: 'other',
    subject: 'Your receipt from Linear ($96.00)',
    participants: [linear],
    msgs: [
      {
        from: linear,
        hoursAgo: 12,
        text: 'Thanks for your payment. Linear Standard · 12 seats · July 2026 — $96.00 charged to Visa ••4242.\n\nInvoice INV-38291 is attached.',
      },
    ],
  },
  {
    id: 'thr_24',
    split: 'other',
    subject: 'Package shipped: arriving Friday by 9pm',
    participants: [ups],
    msgs: [
      {
        from: ups,
        hoursAgo: 36,
        text: 'Your package from B&H Photo is on the way. Tracking 1Z 999 AA1 01 2345 6784.\n\nScheduled delivery: Friday, July 10 by 9:00pm. No signature required.',
      },
    ],
  },
  {
    id: 'thr_25',
    split: 'other',
    subject: '[GitHub] Dependabot alert: lodash < 4.17.21 in calendium/api',
    participants: [github],
    msgs: [
      {
        from: github,
        hoursAgo: 60,
        text: 'A high-severity vulnerability (prototype pollution, CVE-2021-23337) was found in a transitive dependency of calendium/api.\n\nDependabot opened PR #103 to bump lodash to 4.17.21. Review and merge when ready.',
      },
    ],
  },
];

const store = new Map<string, ThreadRecord>(SPECS.map((spec) => [spec.id, build(spec)]));

function sentByMe(record: ThreadRecord): boolean {
  return record.messages.some((m) => m.from.email === MOCK_ME.email);
}

function matchesQuery(record: ThreadRecord, q: string): boolean {
  const needle = q.toLowerCase();
  const { thread } = record;
  return (
    thread.subject.toLowerCase().includes(needle) ||
    thread.snippet.toLowerCase().includes(needle) ||
    thread.participants.some(
      (part) =>
        part.email.toLowerCase().includes(needle) ||
        (part.name ?? '').toLowerCase().includes(needle)
    )
  );
}

export function getMockThreads(params: {
  split?: InboxSplit;
  view?: MailboxView;
  q?: string;
}): Page<Thread> {
  const records = [...store.values()];
  let filtered: ThreadRecord[];

  if (params.q) {
    filtered = records.filter((r) => !r.trashed && matchesQuery(r, params.q!));
  } else if (params.view === 'starred') {
    filtered = records.filter((r) => !r.trashed && r.thread.starred);
  } else if (params.view === 'snoozed') {
    filtered = records.filter((r) => !r.trashed && r.thread.snoozedUntil !== null);
  } else if (params.view === 'sent') {
    filtered = records.filter((r) => !r.trashed && sentByMe(r));
  } else if (params.view === 'drafts') {
    filtered = [];
  } else {
    const split = params.split ?? 'important';
    filtered = records.filter(
      (r) =>
        !r.trashed &&
        !r.archived &&
        r.thread.split === split &&
        (r.thread.snoozedUntil === null || new Date(r.thread.snoozedUntil).getTime() <= Date.now())
    );
  }

  const items = filtered
    .map((r) => ({ ...r.thread }))
    .sort((a, b) => b.lastMessageAt.localeCompare(a.lastMessageAt));
  return { items, nextCursor: null };
}

export function getMockThread(threadId: string): { thread: Thread; messages: Message[] } | null {
  const record = store.get(threadId);
  if (!record) return null;
  return { thread: { ...record.thread }, messages: record.messages.map((m) => ({ ...m })) };
}

// ---------------------------------------------------------------------------
// Recent Opens (M2.5, task 15)
// ---------------------------------------------------------------------------

/** Every sent message with a real openedAt (read receipt), newest first. */
function computeMockOpens(): OpenEvent[] {
  const events: OpenEvent[] = [];
  for (const record of store.values()) {
    for (const message of record.messages) {
      if (message.from.email === MOCK_ME.email && message.openedAt) {
        events.push({
          messageId: message.id,
          threadId: message.threadId,
          accountId: message.accountId,
          subject: record.thread.subject,
          recipients: message.to,
          openedAt: message.openedAt,
          sentAt: message.sentAt,
        });
      }
    }
  }
  return events.sort((a, b) => b.openedAt.localeCompare(a.openedAt));
}

/**
 * Local fallback for GET /v1/mail/opens when the API is unreachable. Honors
 * the same cursor contract as the real endpoint: `cursor` is the last
 * messageId the caller already has, and the response's `nextCursor` is null
 * once there is nothing left to page through.
 */
export function getMockOpens(params: { cursor?: string; limit?: number }): Page<OpenEvent> {
  const all = computeMockOpens();
  const limit = params.limit ?? 25;
  const startIndex = params.cursor
    ? Math.max(0, all.findIndex((e) => e.messageId === params.cursor) + 1)
    : 0;
  const items = all.slice(startIndex, startIndex + limit);
  const hasMore = startIndex + items.length < all.length;
  const nextCursor = hasMore ? (items[items.length - 1]?.messageId ?? null) : null;
  return { items, nextCursor };
}

export function applyMockAction(threadId: string, action: ThreadAction): void {
  const record = store.get(threadId);
  if (!record) return;
  switch (action) {
    case 'archive':
      record.archived = true;
      break;
    case 'move_to_inbox':
      record.archived = false;
      record.trashed = false;
      break;
    case 'trash':
    case 'spam':
      record.trashed = true;
      break;
    case 'star':
      record.thread.starred = true;
      break;
    case 'unstar':
      record.thread.starred = false;
      break;
    case 'read':
      record.thread.unread = false;
      break;
    case 'unread':
      record.thread.unread = true;
      break;
  }
}

export function mockSnoozeThread(threadId: string, until: string | null): void {
  const record = store.get(threadId);
  if (record) record.thread.snoozedUntil = until;
}

export function mockRemindThread(threadId: string, remindAt: string | null): void {
  const record = store.get(threadId);
  if (record) record.thread.remindAt = remindAt;
}

// ---------------------------------------------------------------------------
// Labels, bulk actions, unsubscribe, Get Me To Zero
// ---------------------------------------------------------------------------

const MOCK_LABELS: Label[] = [
  { id: 'lbl_updates', accountId: ACCOUNT_ID, name: 'Updates', kind: 'user', color: null },
  { id: 'lbl_receipts', accountId: ACCOUNT_ID, name: 'Receipts', kind: 'user', color: null },
  { id: 'lbl_travel', accountId: ACCOUNT_ID, name: 'Travel', kind: 'user', color: null },
];

export function getMockLabels(): Label[] {
  return MOCK_LABELS.map((l) => ({ ...l }));
}

export function applyMockLabel(threadId: string, labelId: string, add: boolean): void {
  const record = store.get(threadId);
  if (!record) return;
  const ids = record.thread.labelIds.filter((id) => id !== labelId);
  if (add) ids.push(labelId);
  record.thread.labelIds = ids;
}

export function mockBulkAction(threadIds: string[], action: ThreadAction): void {
  for (const id of threadIds) applyMockAction(id, action);
}

export function mockUnsnoozeThread(threadId: string): void {
  const record = store.get(threadId);
  if (record) record.thread.snoozedUntil = null;
}

export function mockUnsubscribe(threadId: string): UnsubscribeResult {
  const record = store.get(threadId);
  if (!record) return { method: 'link', url: 'https://example.com/unsubscribe' };
  const t = record.thread;
  if (t.unsubscribeOneClick && t.unsubscribeUrl) return { method: 'one_click' };
  if (t.unsubscribeMailto) return { method: 'mailto' };
  return { method: 'link', url: t.unsubscribeUrl ?? 'https://example.com/unsubscribe' };
}

export function mockArchiveOlderThan(olderThanIso: string): number {
  const cutoff = Date.parse(olderThanIso);
  let archived = 0;
  for (const record of store.values()) {
    const t = record.thread;
    const inInbox = !t.snoozedUntil; // demo approximation of inbox membership
    if (inInbox && Date.parse(t.lastMessageAt) < cutoff) {
      applyMockAction(t.id, 'archive');
      archived++;
    }
  }
  return archived;
}

// ---------------------------------------------------------------------------
// AI
// ---------------------------------------------------------------------------

/** Local fallback for POST /v1/ai/compose when the API is unreachable. */
export function mockAiCompose(req: AiComposeRequest): AiComposeResponse {
  const prompt = req.prompt.trim();
  const lines: string[] = ['Hi,', ''];
  if (req.action === 'summarize') {
    lines.splice(0, lines.length,
      'Summary: the thread converged on a decision; the remaining open item is timing.',
      '',
      'Action items:',
      '• Confirm the proposed terms',
      '• Reply with availability for a follow-up this week'
    );
  } else {
    lines.push(
      `Following up on this — ${prompt.replace(/\.$/, '')}.`,
      '',
      'Does that work on your end? Happy to adjust if another approach is easier.',
      '',
      'Best,'
    );
  }
  return { text: lines.join('\n'), model: 'demo/local-fallback' };
}

/** Local fallback for GET .../instant-replies when the API is unreachable. */
export function mockInstantReplies(threadId: string): string[] {
  const record = store.get(threadId);
  if (!record) return [];
  return [
    'Sounds good, thanks!',
    'Let me look into this and follow up shortly.',
    'Can we push this to next week?',
  ];
}

/** Local fallback for POST /v1/ai/ask (cited Q&A) when the API is unreachable. */
export function mockAiAskCited(
  question: string,
  threadId?: string
): { answer: string; model: string; sources: AiSource[] } {
  const records = threadId
    ? [store.get(threadId)].filter((r): r is ThreadRecord => r !== undefined)
    : [...store.values()].slice(0, 2);
  const sources: AiSource[] = records.map((r) => {
    const last = r.messages[r.messages.length - 1];
    return {
      threadId: r.thread.id,
      messageId: last?.id,
      subject: r.thread.subject,
      snippet: last ? firstLine(last.bodyText) : r.thread.snippet,
    };
  });
  const trimmed = question.trim().replace(/\?+$/, '');
  return {
    answer: trimmed
      ? `Based on your mailbox: ${trimmed} — it looks like it's on track. See the linked conversation${sources.length === 1 ? '' : 's'} below for the details.`
      : "I couldn't find anything relevant in your mailbox for that question.",
    model: 'demo/local-fallback',
    sources,
  };
}

/** Local fallback for POST /v1/ai/event-proposal when the API is unreachable. */
export function mockProposeEvent(threadId: string): AiEventProposal {
  const record = store.get(threadId);
  const start = new Date(NOW + 24 * 3_600_000);
  start.setMinutes(0, 0, 0);
  const end = new Date(start.getTime() + 30 * 60_000);
  return {
    title: record ? record.thread.subject.replace(/^(Re|Fwd):\s*/i, '') : 'Follow-up meeting',
    attendees: record
      ? record.thread.participants.map((p) => p.email).filter((email) => email !== MOCK_ME.email)
      : [],
    start: start.toISOString(),
    end: end.toISOString(),
    notes: 'Proposed from thread context (demo mode).',
  };
}
