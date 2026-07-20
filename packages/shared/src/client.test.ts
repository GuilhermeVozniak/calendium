import { describe, expect, it, vi } from 'vitest';
import {
  ApiClient,
  ApiRequestError,
  createPublicBooking,
  fetchInstance,
  isDelegablePath,
  fetchPublicBookingPage,
  fetchPublicPoll,
  fetchPublicSlots,
  votePublicPoll,
} from './client';
import type {
  AiAskRequest,
  AiComposeRequest,
  Place,
  BookingLinkInput,
  BookingRequest,
  CalendarPrefs,
  CalendarPrefsPatch,
  CalendarSetInput,
  ClassifierInput,
  CommentInput,
  ShareThreadInput,
  DraftInput,
  EventInput,
  EventPatch,
  EventTemplateInput,
  PollBallot,
  PollInput,
  TaskInput,
  TaskPatch,
  TimeProposalInput,
  UserSettings,
} from './types';

// ---------------------------------------------------------------------------
// Test harness
//
// A fake `fetch` that records every request it receives (url/method/headers/
// body) and replays a queue of programmable Response objects. No network is
// ever touched — this is the seam `ApiClientOptions.fetch` exists for.
// ---------------------------------------------------------------------------

interface RecordedRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

interface FakeResponseSpec {
  status: number;
  body?: unknown;
}

function createFakeFetch(responses: FakeResponseSpec[] = [{ status: 200, body: {} }]) {
  const calls: RecordedRequest[] = [];
  let callIndex = 0;

  const fetchFn = vi.fn(async (input: string | URL, init: RequestInit = {}) => {
    const headers: Record<string, string> = {};
    if (init.headers) {
      for (const [key, value] of Object.entries(init.headers as Record<string, string>)) {
        headers[key] = value;
      }
    }
    let body: unknown;
    if (typeof init.body === 'string') {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    calls.push({ url: String(input), method: init.method ?? 'GET', headers, body });

    // Repeat the last spec once the queue is exhausted, so a single-entry
    // queue can back an arbitrary number of calls within one test.
    const spec = responses[Math.min(callIndex, responses.length - 1)];
    callIndex++;
    const responseBody = spec.status === 204 || spec.body === undefined ? null : JSON.stringify(spec.body);
    return new Response(responseBody, {
      status: spec.status,
      headers: { 'Content-Type': 'application/json' },
    });
  });

  return { fetchFn, calls };
}

const BASE_URL = 'https://api.calendium.test';
const TOKEN = 'test-access-token';

// `URLSearchParams` iteration (`for...of`, `Object.fromEntries`, `.keys()`)
// requires the `DOM.Iterable` lib, which this package's tsconfig omits
// (shared is DOM-free); `forEach` is part of plain `lib.dom` instead.
function searchParamsToObject(sp: URLSearchParams): Record<string, string> {
  const result: Record<string, string> = {};
  sp.forEach((value, key) => {
    result[key] = value;
  });
  return result;
}

function makeClient(opts?: { responses?: FakeResponseSpec[]; token?: string | null }) {
  const { fetchFn, calls } = createFakeFetch(opts?.responses);
  const getAccessToken = vi.fn(async () => (opts?.token === undefined ? TOKEN : opts.token));
  const client = new ApiClient({
    baseUrl: BASE_URL,
    getAccessToken,
    fetch: fetchFn as unknown as typeof fetch,
  });
  return { client, fetchFn, calls, getAccessToken };
}

// ---------------------------------------------------------------------------
// fetchInstance — standalone, pre-auth instance discovery
// ---------------------------------------------------------------------------

describe('fetchInstance', () => {
  it('GETs {baseUrl}/v1/instance with an Accept header and no auth', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: { name: 'calendium' } }]);
    const result = await fetchInstance(BASE_URL, fetchFn as unknown as typeof fetch);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/instance`);
    expect(calls[0].method).toBe('GET');
    expect(calls[0].headers.Accept).toBe('application/json');
    expect(calls[0].headers.Authorization).toBeUndefined();
    expect(result).toEqual({ name: 'calendium' });
  });

  it('strips trailing slashes from baseUrl', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: {} }]);
    await fetchInstance(`${BASE_URL}///`, fetchFn as unknown as typeof fetch);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/instance`);
  });

  it('throws ApiRequestError with the status/code/message from the error body', async () => {
    const { fetchFn } = createFakeFetch([
      { status: 503, body: { error: { code: 'unavailable', message: 'down for maintenance' } } },
    ]);
    await expect(fetchInstance(BASE_URL, fetchFn as unknown as typeof fetch)).rejects.toBeInstanceOf(
      ApiRequestError
    );
    const { fetchFn: fetchFn2 } = createFakeFetch([
      { status: 503, body: { error: { code: 'unavailable', message: 'down for maintenance' } } },
    ]);
    await expect(fetchInstance(BASE_URL, fetchFn2 as unknown as typeof fetch)).rejects.toMatchObject({
      status: 503,
      code: 'unavailable',
      message: 'down for maintenance',
    });
  });

  it('falls back to default code/message when the error body is unparsable', async () => {
    const fetchFn = vi.fn(async () => new Response('not json', { status: 500 }));
    await expect(fetchInstance(BASE_URL, fetchFn as unknown as typeof fetch)).rejects.toMatchObject({
      status: 500,
      code: 'unknown',
      message: 'Request failed with status 500',
    });
  });

  it('uses the global fetch when no fetchImpl is provided', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: { name: 'global' } }]);
    vi.stubGlobal('fetch', fetchFn);
    try {
      const result = await fetchInstance(BASE_URL);
      expect(result).toEqual({ name: 'global' });
      expect(calls[0].url).toBe(`${BASE_URL}/v1/instance`);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// ---------------------------------------------------------------------------
// Auth injection
// ---------------------------------------------------------------------------

describe('auth injection', () => {
  it('sends Authorization: Bearer <token> from getAccessToken on authenticated requests', async () => {
    const { client, calls, getAccessToken } = makeClient();
    await client.getMe();
    expect(getAccessToken).toHaveBeenCalledTimes(1);
    expect(calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(calls[0].headers['Content-Type']).toBe('application/json');
  });

  it('omits the Authorization header when getAccessToken resolves null', async () => {
    const { client, calls } = makeClient({ token: null });
    await client.getMe();
    expect(calls[0].headers.Authorization).toBeUndefined();
    expect(calls[0].headers['Content-Type']).toBe('application/json');
  });

  it('getInstance() is public: no Authorization header and getAccessToken is never called', async () => {
    const { client, calls, getAccessToken } = makeClient({
      responses: [{ status: 200, body: { name: 'calendium', mode: 'cloud' } }],
    });
    const result = await client.getInstance();
    expect(getAccessToken).not.toHaveBeenCalled();
    expect(calls[0].url).toBe(`${BASE_URL}/v1/instance`);
    expect(calls[0].method).toBe('GET');
    expect(calls[0].headers.Authorization).toBeUndefined();
    expect(result).toEqual({ name: 'calendium', mode: 'cloud' });
  });

  it('uses the global fetch when no fetch option is provided to the ApiClient', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: { id: 'me1' } }]);
    vi.stubGlobal('fetch', fetchFn);
    try {
      const client = new ApiClient({ baseUrl: BASE_URL, getAccessToken: async () => TOKEN });
      const result = await client.getMe();
      expect(result).toEqual({ id: 'me1' });
      expect(calls[0].url).toBe(`${BASE_URL}/v1/me`);
      expect(calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

describe('error mapping', () => {
  it('throws ApiRequestError carrying status/code/message from a JSON error body', async () => {
    const { client } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'Thread not found' } } }],
    });
    await expect(client.getThread('t1')).rejects.toBeInstanceOf(ApiRequestError);
    const { client: client2 } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'Thread not found' } } }],
    });
    await expect(client2.getThread('t1')).rejects.toMatchObject({
      name: 'ApiRequestError',
      status: 404,
      code: 'not_found',
      message: 'Thread not found',
    });
  });

  it('falls back to code="unknown" and a status-derived message when the body has no error field', async () => {
    const { client } = makeClient({ responses: [{ status: 500, body: { oops: true } }] });
    await expect(client.getMe()).rejects.toMatchObject({
      status: 500,
      code: 'unknown',
      message: 'Request failed with status 500',
    });
  });

  it('falls back to defaults when the error body is not valid JSON at all', async () => {
    const fetchFn = vi.fn(async () => new Response('<html>bad gateway</html>', { status: 502 }));
    const client = new ApiClient({
      baseUrl: BASE_URL,
      getAccessToken: async () => TOKEN,
      fetch: fetchFn as unknown as typeof fetch,
    });
    await expect(client.getMe()).rejects.toMatchObject({
      status: 502,
      code: 'unknown',
      message: 'Request failed with status 502',
    });
  });

  it('returns undefined for a 204 No Content response without attempting to parse a body', async () => {
    const fetchFn = vi.fn(async () => new Response(null, { status: 204 }));
    const client = new ApiClient({
      baseUrl: BASE_URL,
      getAccessToken: async () => TOKEN,
      fetch: fetchFn as unknown as typeof fetch,
    });
    const result = await client.disconnectAccount('acc1');
    expect(result).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// unsendDraft — undo-send grace window vs. already-delivered conflict
// ---------------------------------------------------------------------------

describe('unsendDraft', () => {
  it('returns the reverted draft when cancel succeeds within the grace window', async () => {
    const revertedDraft = { id: 'd1', scheduledAt: null };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: revertedDraft }] });
    const result = await client.unsendDraft('d1');
    expect(result).toEqual(revertedDraft);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/drafts/d1/unsend`);
    expect(calls[0].body).toBeUndefined();
  });

  it('throws ApiRequestError(409, "conflict") when the message was already delivered', async () => {
    const { client } = makeClient({
      responses: [
        { status: 409, body: { error: { code: 'conflict', message: 'Message already delivered' } } },
      ],
    });
    await expect(client.unsendDraft('d1')).rejects.toBeInstanceOf(ApiRequestError);
    const { client: client2 } = makeClient({
      responses: [
        { status: 409, body: { error: { code: 'conflict', message: 'Message already delivered' } } },
      ],
    });
    await expect(client2.unsendDraft('d1')).rejects.toMatchObject({
      status: 409,
      code: 'conflict',
      message: 'Message already delivered',
    });
  });
});

// ---------------------------------------------------------------------------
// Query-string building
// ---------------------------------------------------------------------------

describe('query-string building', () => {
  it('listThreads sets every provided param and nothing else', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { items: [], nextCursor: null } }] });
    await client.listThreads({
      split: 'important',
      view: 'starred',
      labelId: 'lbl1',
      q: 'invoice',
      cursor: 'cur1',
      limit: 25,
      accountId: 'acc1',
    });
    const url = new URL(calls[0].url);
    expect(url.pathname).toBe('/v1/mail/threads');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      split: 'important',
      view: 'starred',
      labelId: 'lbl1',
      q: 'invoice',
      cursor: 'cur1',
      limit: '25',
      accountId: 'acc1',
    });
  });

  it('listThreads omits params that are not set', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { items: [], nextCursor: null } }] });
    await client.listThreads({});
    const url = new URL(calls[0].url);
    expect(url.pathname).toBe('/v1/mail/threads');
    expect(searchParamsToObject(url.searchParams)).toEqual({});
  });

  it('listEvents sets from/to and comma-joins calendarIds when present', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listEvents('2026-01-01', '2026-01-31', ['cal1', 'cal2']);
    const url = new URL(calls[0].url);
    expect(url.pathname).toBe('/v1/events');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      from: '2026-01-01',
      to: '2026-01-31',
      calendarIds: 'cal1,cal2',
    });
  });

  it('listEvents omits calendarIds when absent or empty', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listEvents('2026-01-01', '2026-01-31');
    const url = new URL(calls[0].url);
    expect(url.searchParams.has('calendarIds')).toBe(false);

    const { client: client2, calls: calls2 } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client2.listEvents('2026-01-01', '2026-01-31', []);
    const url2 = new URL(calls2[0].url);
    expect(url2.searchParams.has('calendarIds')).toBe(false);
  });

  it('getAvailability sets from/to/duration', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.getAvailability('2026-01-01', '2026-01-02', 30);
    const url = new URL(calls[0].url);
    expect(url.pathname).toBe('/v1/availability');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      from: '2026-01-01',
      to: '2026-01-02',
      duration: '30',
    });
  });

  it('search URL-encodes the q param', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { threads: [], events: [] } }] });
    await client.search('a b&c=d');
    const url = new URL(calls[0].url);
    expect(url.pathname).toBe('/v1/search');
    expect(url.searchParams.get('q')).toBe('a b&c=d');
  });
});

describe('mail M2.5 endpoints', () => {
  it('listOpens sets every provided param and nothing else', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { items: [], nextCursor: null } }],
    });
    await client.listOpens({ cursor: 'cur1', limit: 25 });
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/mail/opens');
    expect(searchParamsToObject(url.searchParams)).toEqual({ cursor: 'cur1', limit: '25' });
    expect(calls[0]!.method).toBe('GET');
  });

  it('listOpens omits params that are not set', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { items: [], nextCursor: null } }],
    });
    await client.listOpens({});
    const url = new URL(calls[0]!.url);
    expect(searchParamsToObject(url.searchParams)).toEqual({});
  });

  it('getSendSuggestion GETs /v1/mail/send-suggestion?email=...', async () => {
    const suggestion = {
      email: 'ada@example.com',
      suggestedAt: '2026-07-19T09:00:00Z',
      utcOffsetHours: -5,
      confidence: 0.82,
      sampleSize: 12,
    };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: suggestion }] });
    const result = await client.getSendSuggestion('ada@example.com');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/mail/send-suggestion');
    expect(searchParamsToObject(url.searchParams)).toEqual({ email: 'ada@example.com' });
    expect(calls[0]!.method).toBe('GET');
    expect(result).toEqual(suggestion);
  });

  it('getSendSuggestion throws ApiRequestError(404) when there is no suggestion yet', async () => {
    const { client } = makeClient({
      responses: [
        { status: 404, body: { error: { code: 'not_found', message: 'not enough history' } } },
      ],
    });
    await expect(client.getSendSuggestion('ada@example.com')).rejects.toBeInstanceOf(ApiRequestError);
    const { client: client2 } = makeClient({
      responses: [
        { status: 404, body: { error: { code: 'not_found', message: 'not enough history' } } },
      ],
    });
    await expect(client2.getSendSuggestion('ada@example.com')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    });
  });

  it('searchAttachments sets every provided param and nothing else', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { items: [], nextCursor: null } }],
    });
    await client.searchAttachments({
      q: 'invoice',
      contact: 'ada@example.com',
      threadId: 't1',
      cursor: 'cur1',
      limit: 10,
    });
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/mail/attachments');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      q: 'invoice',
      contact: 'ada@example.com',
      threadId: 't1',
      cursor: 'cur1',
      limit: '10',
    });
    expect(calls[0]!.method).toBe('GET');
  });

  it('searchAttachments omits params that are not set', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { items: [], nextCursor: null } }],
    });
    await client.searchAttachments({});
    const url = new URL(calls[0]!.url);
    expect(searchParamsToObject(url.searchParams)).toEqual({});
  });

  it('attachmentContentPath returns the content path without making a request', () => {
    const { client, calls } = makeClient();
    expect(client.attachmentContentPath('att1')).toBe('/v1/mail/attachments/att1/content');
    expect(calls).toHaveLength(0);
  });

  it('reactToMessage POSTs emoji+sendReply (default false) to the reactions route', async () => {
    const result = { reaction: { id: 'r1', messageId: 'm1', emoji: '\u{1F44D}' }, draftId: null };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: result }] });
    const res = await client.reactToMessage('m1', '\u{1F44D}');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/mail/messages/m1/reactions`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.body).toEqual({ emoji: '\u{1F44D}', sendReply: false });
    expect(res).toEqual(result);
  });

  it('reactToMessage passes sendReply=true through when requested', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { reaction: {}, draftId: 'd1' } }],
    });
    await client.reactToMessage('m1', '\u{1F44D}', true);
    expect(calls[0]!.body).toEqual({ emoji: '\u{1F44D}', sendReply: true });
  });

  it('removeReaction DELETEs the percent-encoded emoji-scoped reactions route', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    const result = await client.removeReaction('m1', '\u{1F44D}');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/mail/messages/m1/reactions/%F0%9F%91%8D`);
    expect(calls[0]!.method).toBe('DELETE');
    expect(result).toBeUndefined();
  });

  // Keycap sequences (e.g. '#️⃣') start with ASCII '#', which the WHATWG URL
  // state machine treats as the fragment delimiter in every runtime. Raw
  // interpolation would silently truncate the DELETE path before the emoji;
  // encodeURIComponent must run first so '#' never reaches the URL parser.
  it('removeReaction percent-encodes keycap emoji so the leading "#" cannot truncate the URL path', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.removeReaction('m1', '#\u{FE0F}\u{20E3}');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/mail/messages/m1/reactions/%23%EF%B8%8F%E2%83%A3`);
    expect(calls[0]!.url).not.toContain('#');
  });
});

// ---------------------------------------------------------------------------
// Path + verb + body contract for every remaining method, table-driven.
//
// (listThreads, listEvents, getAvailability, search and unsendDraft are
// exercised in dedicated sections above; getInstance/fetchInstance are
// exercised in the fetchInstance/auth-injection sections above.)
// ---------------------------------------------------------------------------

const DRAFT_INPUT: DraftInput = {
  accountId: 'acc1',
  threadId: null,
  to: [{ name: 'Ada', email: 'ada@example.com' }],
  cc: [],
  bcc: [],
  subject: 'Re: Launch',
  bodyHtml: '<p>Sounds good.</p>',
  scheduledAt: null,
};

const EVENT_INPUT: EventInput = {
  calendarId: 'cal1',
  title: 'Standup',
  start: '2026-08-01T09:00:00Z',
  end: '2026-08-01T09:30:00Z',
};

const EVENT_PATCH: EventPatch = {
  title: 'Standup (updated)',
  start: '2026-08-01T09:15:00Z',
};

const EVENT_TEMPLATE_INPUT: EventTemplateInput = {
  name: '1:1',
  title: 'One-on-one',
  description: 'Weekly sync',
  location: 'Video call',
  durationMinutes: 30,
  allDay: false,
  calendarId: 'cal1',
  attendeeEmails: ['alice@example.com'],
  addConferencing: true,
  reminderMinutes: [15],
  recurrenceRule: 'FREQ=WEEKLY',
};

const CALENDAR_SET_INPUT: CalendarSetInput = {
  name: 'Work',
  calendarIds: ['cal1', 'cal2'],
  position: 0,
};

const AI_REQUEST: AiComposeRequest = {
  action: 'compose',
  prompt: 'Draft a reply',
  threadId: 't1',
};

const AI_ASK_REQUEST: AiAskRequest = {
  question: 'When is the deadline?',
  threadId: 't1',
};

const CLASSIFIER_INPUT: ClassifierInput = {
  name: 'Invoices',
  prompt: 'invoice emails',
  targetSplit: 'important',
  enabled: true,
};

const BOOKING_LINK_INPUT: BookingLinkInput = {
  slug: 'thirty-min',
  title: '30 Minute Meeting',
  description: 'Quick sync',
  calendarId: 'cal1',
  durationMinutes: 30,
  timeZone: 'America/New_York',
  windows: [{ weekday: 1, start: '09:00', end: '17:00' }],
  bufferBeforeMin: 5,
  bufferAfterMin: 5,
  dailyLimit: 0,
  minNoticeMin: 60,
  maxAdvanceDays: 60,
  respectWorkingHours: true,
  addConferencing: true,
  active: true,
};

const POLL_INPUT: PollInput = {
  title: 'Team Sync',
  description: 'Pick a time',
  calendarId: 'cal1',
  durationMinutes: 30,
  options: [
    { start: '2026-08-01T09:00:00Z', end: '2026-08-01T09:30:00Z' },
    { start: '2026-08-01T10:00:00Z', end: '2026-08-01T10:30:00Z' },
  ],
};

const TIME_PROPOSAL_INPUT: TimeProposalInput = {
  start: '2026-08-01T09:00:00Z',
  end: '2026-08-01T09:30:00Z',
  note: 'Works better for me',
};

const USER_SETTINGS: UserSettings = {
  timeZone: 'America/New_York',
  workingHours: [{ weekday: 1, start: '09:00', end: '17:00' }],
  workingLocation: 'home',
};

interface MethodCase {
  name: string;
  call: (client: ApiClient) => Promise<unknown>;
  method: string;
  path: string;
  body?: unknown;
}

const methodCases: MethodCase[] = [
  // --- Me & billing ---
  { name: 'getMe', call: (c) => c.getMe(), method: 'GET', path: '/v1/me' },
  {
    name: 'getSubscription',
    call: (c) => c.getSubscription(),
    method: 'GET',
    path: '/v1/billing/subscription',
  },
  {
    name: 'createCheckoutSession',
    call: (c) => c.createCheckoutSession('https://x.test/success', 'https://x.test/cancel'),
    method: 'POST',
    path: '/v1/billing/checkout',
    body: { successUrl: 'https://x.test/success', cancelUrl: 'https://x.test/cancel' },
  },
  {
    name: 'createBillingPortalSession',
    call: (c) => c.createBillingPortalSession('https://x.test/return'),
    method: 'POST',
    path: '/v1/billing/portal',
    body: { returnUrl: 'https://x.test/return' },
  },

  // --- Connected accounts ---
  { name: 'listAccounts', call: (c) => c.listAccounts(), method: 'GET', path: '/v1/accounts' },
  {
    name: 'connectAccount',
    call: (c) => c.connectAccount('google', 'https://x.test/redirect'),
    method: 'POST',
    path: '/v1/accounts/connect/google',
    body: { redirectUrl: 'https://x.test/redirect' },
  },
  {
    name: 'setVipSenders',
    call: (c) => c.setVipSenders('acc1', ['vip@example.com']),
    method: 'PUT',
    path: '/v1/accounts/acc1/vip-senders',
    body: { vipSenders: ['vip@example.com'] },
  },
  {
    name: 'setSignature',
    call: (c) => c.setSignature('acc1', '<p>Best, Ada</p>'),
    method: 'PUT',
    path: '/v1/accounts/acc1/signature',
    body: { signatureHtml: '<p>Best, Ada</p>' },
  },
  {
    name: 'setAutoBcc',
    call: (c) => c.setAutoBcc('acc1', ['archive@example.com']),
    method: 'PUT',
    path: '/v1/accounts/acc1/auto-bcc',
    body: { autoBcc: ['archive@example.com'] },
  },
  {
    name: 'disconnectAccount',
    call: (c) => c.disconnectAccount('acc1'),
    method: 'DELETE',
    path: '/v1/accounts/acc1',
  },

  // --- Mail ---
  { name: 'getThread', call: (c) => c.getThread('t1'), method: 'GET', path: '/v1/mail/threads/t1' },
  {
    name: 'actOnThread',
    call: (c) => c.actOnThread('t1', 'archive'),
    method: 'POST',
    path: '/v1/mail/threads/t1/actions',
    body: { action: 'archive' },
  },
  {
    name: 'markThreadOpened',
    call: (c) => c.markThreadOpened('t1'),
    method: 'POST',
    path: '/v1/mail/threads/t1/open',
  },
  {
    name: 'snoozeThread',
    call: (c) => c.snoozeThread('t1', '2026-08-01T00:00:00Z'),
    method: 'POST',
    path: '/v1/mail/threads/t1/snooze',
    body: { until: '2026-08-01T00:00:00Z' },
  },
  {
    name: 'setThreadReminder',
    call: (c) => c.setThreadReminder('t1', '2026-08-02T00:00:00Z'),
    method: 'POST',
    path: '/v1/mail/threads/t1/reminder',
    body: { remindAt: '2026-08-02T00:00:00Z' },
  },
  { name: 'listDrafts', call: (c) => c.listDrafts(), method: 'GET', path: '/v1/mail/drafts' },
  { name: 'getDraft', call: (c) => c.getDraft('d1'), method: 'GET', path: '/v1/mail/drafts/d1' },
  {
    name: 'saveDraft',
    call: (c) => c.saveDraft({ accountId: 'acc1', subject: 'hi' }),
    method: 'POST',
    path: '/v1/mail/drafts',
    body: { accountId: 'acc1', subject: 'hi' },
  },
  {
    name: 'updateDraft',
    call: (c) => c.updateDraft('d1', DRAFT_INPUT),
    method: 'PUT',
    path: '/v1/mail/drafts/d1',
    body: DRAFT_INPUT,
  },
  { name: 'deleteDraft', call: (c) => c.deleteDraft('d1'), method: 'DELETE', path: '/v1/mail/drafts/d1' },
  {
    name: 'sendDraft',
    call: (c) => c.sendDraft('d1'),
    method: 'POST',
    path: '/v1/mail/drafts/d1/send',
  },
  { name: 'listSnippets', call: (c) => c.listSnippets(), method: 'GET', path: '/v1/mail/snippets' },
  {
    name: 'createSnippet',
    call: (c) => c.createSnippet({ name: 'Thanks', shortcut: ';ty', bodyHtml: '<p>Thanks!</p>' }),
    method: 'POST',
    path: '/v1/mail/snippets',
    body: { name: 'Thanks', shortcut: ';ty', bodyHtml: '<p>Thanks!</p>' },
  },
  {
    name: 'updateSnippet',
    call: (c) => c.updateSnippet('s1', { name: 'Thanks', shortcut: ';ty', bodyHtml: '<p>Thx</p>' }),
    method: 'PUT',
    path: '/v1/mail/snippets/s1',
    body: { name: 'Thanks', shortcut: ';ty', bodyHtml: '<p>Thx</p>' },
  },
  {
    name: 'deleteSnippet',
    call: (c) => c.deleteSnippet('s1'),
    method: 'DELETE',
    path: '/v1/mail/snippets/s1',
  },

  // --- Calendar ---
  { name: 'listCalendars', call: (c) => c.listCalendars(), method: 'GET', path: '/v1/calendars' },
  {
    name: 'updateCalendar',
    call: (c) => c.updateCalendar('cal1', { isVisible: false }),
    method: 'PATCH',
    path: '/v1/calendars/cal1',
    body: { isVisible: false },
  },
  {
    name: 'createEvent',
    call: (c) => c.createEvent(EVENT_INPUT),
    method: 'POST',
    path: '/v1/events',
    body: EVENT_INPUT,
  },
  {
    name: 'updateEvent',
    call: (c) => c.updateEvent('e1', EVENT_PATCH),
    method: 'PATCH',
    path: '/v1/events/e1',
    body: EVENT_PATCH,
  },
  { name: 'deleteEvent', call: (c) => c.deleteEvent('e1'), method: 'DELETE', path: '/v1/events/e1' },
  {
    name: 'rsvp',
    call: (c) => c.rsvp('e1', 'accepted'),
    method: 'POST',
    path: '/v1/events/e1/rsvp',
    body: { response: 'accepted' },
  },

  // --- AI ---
  {
    name: 'aiCompose',
    call: (c) => c.aiCompose(AI_REQUEST),
    method: 'POST',
    path: '/v1/ai/compose',
    body: AI_REQUEST,
  },
  {
    name: 'aiSummarize',
    call: (c) => c.aiSummarize({ prompt: 'tl;dr', threadId: 't1' }),
    method: 'POST',
    path: '/v1/ai/compose',
    body: { prompt: 'tl;dr', threadId: 't1', action: 'summarize' },
  },
  {
    name: 'aiAsk',
    call: (c) => c.aiAsk({ prompt: 'when is the meeting?', threadId: 't1' }),
    method: 'POST',
    path: '/v1/ai/compose',
    body: { prompt: 'when is the meeting?', threadId: 't1', action: 'ask' },
  },
  {
    name: 'aiAskCited',
    call: (c) => c.aiAskCited(AI_ASK_REQUEST),
    method: 'POST',
    path: '/v1/ai/ask',
    body: AI_ASK_REQUEST,
  },
  {
    name: 'getInstantReplies',
    call: (c) => c.getInstantReplies('t1'),
    method: 'GET',
    path: '/v1/mail/threads/t1/instant-replies',
  },
  {
    name: 'aiEditDraft (no tone)',
    call: (c) => c.aiEditDraft('improve', 'd1'),
    method: 'POST',
    path: '/v1/ai/compose',
    body: { action: 'improve', draftId: 'd1', prompt: '' },
  },
  {
    name: 'aiEditDraft (change_tone with tone)',
    call: (c) => c.aiEditDraft('change_tone', 'd1', 'formal'),
    method: 'POST',
    path: '/v1/ai/compose',
    body: { action: 'change_tone', draftId: 'd1', prompt: '', tone: 'formal' },
  },
  {
    name: 'proposeEvent',
    call: (c) => c.proposeEvent('t1'),
    method: 'POST',
    path: '/v1/ai/event-proposal',
    body: { threadId: 't1' },
  },
  {
    name: 'listClassifiers',
    call: (c) => c.listClassifiers(),
    method: 'GET',
    path: '/v1/classifiers',
  },
  {
    name: 'createClassifier',
    call: (c) => c.createClassifier(CLASSIFIER_INPUT),
    method: 'POST',
    path: '/v1/classifiers',
    body: CLASSIFIER_INPUT,
  },
  {
    name: 'updateClassifier',
    call: (c) => c.updateClassifier('c1', CLASSIFIER_INPUT),
    method: 'PATCH',
    path: '/v1/classifiers/c1',
    body: CLASSIFIER_INPUT,
  },
  {
    name: 'deleteClassifier',
    call: (c) => c.deleteClassifier('c1'),
    method: 'DELETE',
    path: '/v1/classifiers/c1',
  },

  // --- Mail (M2.5) ---
  // reactToMessage/removeReaction take a raw emoji, which `new URL(...)`
  // percent-encodes on parse — exercised directly (no URL parsing) in the
  // "mail M2.5 endpoints" describe block below instead of this table.
  {
    // email is percent-encoded into the path segment (encodeURIComponent) so
    // an address is never mistaken for an extra path segment/query string;
    // the '@' here becoming '%40' is the load-bearing part of this case.
    name: 'getContact',
    call: (c) => c.getContact('ada@example.com'),
    method: 'GET',
    path: '/v1/mail/contacts/ada%40example.com',
  },

  // --- Push devices ---
  {
    name: 'registerDevice',
    call: (c) => c.registerDevice('ios', 'device-token-1'),
    method: 'POST',
    path: '/v1/devices',
    body: { platform: 'ios', token: 'device-token-1' },
  },
  {
    name: 'unregisterDevice',
    call: (c) => c.unregisterDevice('dev1'),
    method: 'DELETE',
    path: '/v1/devices/dev1',
  },

  // --- Triage power / mail labels ---
  { name: 'listLabels', call: (c) => c.listLabels(), method: 'GET', path: '/v1/mail/labels' },
  {
    name: 'setThreadLabel',
    call: (c) => c.setThreadLabel('t1', 'lbl1', true),
    method: 'POST',
    path: '/v1/mail/threads/t1/labels',
    body: { labelId: 'lbl1', add: true },
  },
  {
    name: 'bulkThreadAction',
    call: (c) => c.bulkThreadAction({ threadIds: ['t1'], action: 'archive' }),
    method: 'POST',
    path: '/v1/mail/threads/bulk-actions',
    body: { threadIds: ['t1'], action: 'archive' },
  },
  {
    name: 'unsnoozeThread',
    call: (c) => c.unsnoozeThread('t1'),
    method: 'DELETE',
    path: '/v1/mail/threads/t1/snooze',
  },
  {
    name: 'unsubscribeThread',
    call: (c) => c.unsubscribeThread('t1'),
    method: 'POST',
    path: '/v1/mail/threads/t1/unsubscribe',
  },
  {
    name: 'archiveOlderThan',
    call: (c) => c.archiveOlderThan('2026-07-10T00:00:00Z'),
    method: 'POST',
    path: '/v1/mail/threads/zero',
    body: { olderThan: '2026-07-10T00:00:00Z' },
  },
  { name: 'getPrefs', call: (c) => c.getPrefs(), method: 'GET', path: '/v1/prefs' },
  {
    name: 'updatePrefs',
    call: (c) => c.updatePrefs({ splitOrder: ['vip', 'important'] }),
    method: 'PUT',
    path: '/v1/prefs',
    body: { splitOrder: ['vip', 'important'] },
  },
  {
    name: 'getPreferences',
    call: (c) => c.getPreferences(),
    method: 'GET',
    path: '/v1/me/preferences',
  },
  {
    name: 'updatePreferences',
    call: (c) => c.updatePreferences({ theme: 'ocean' }),
    method: 'PUT',
    path: '/v1/me/preferences',
    body: { theme: 'ocean' },
  },

  // --- Event templates ---
  {
    name: 'listEventTemplates',
    call: (c) => c.listEventTemplates(),
    method: 'GET',
    path: '/v1/event-templates',
  },
  {
    name: 'createEventTemplate',
    call: (c) => c.createEventTemplate(EVENT_TEMPLATE_INPUT),
    method: 'POST',
    path: '/v1/event-templates',
    body: EVENT_TEMPLATE_INPUT,
  },
  {
    name: 'updateEventTemplate',
    call: (c) => c.updateEventTemplate('et1', EVENT_TEMPLATE_INPUT),
    method: 'PUT',
    path: '/v1/event-templates/et1',
    body: EVENT_TEMPLATE_INPUT,
  },
  {
    name: 'deleteEventTemplate',
    call: (c) => c.deleteEventTemplate('et1'),
    method: 'DELETE',
    path: '/v1/event-templates/et1',
  },
  {
    name: 'useEventTemplate',
    call: (c) => c.useEventTemplate('et1'),
    method: 'POST',
    path: '/v1/event-templates/et1/use',
  },

  // --- Calendar sets ---
  {
    name: 'listCalendarSets',
    call: (c) => c.listCalendarSets(),
    method: 'GET',
    path: '/v1/calendar-sets',
  },
  {
    name: 'createCalendarSet',
    call: (c) => c.createCalendarSet(CALENDAR_SET_INPUT),
    method: 'POST',
    path: '/v1/calendar-sets',
    body: CALENDAR_SET_INPUT,
  },
  {
    name: 'updateCalendarSet',
    call: (c) => c.updateCalendarSet('cs1', CALENDAR_SET_INPUT),
    method: 'PUT',
    path: '/v1/calendar-sets/cs1',
    body: CALENDAR_SET_INPUT,
  },
  {
    name: 'deleteCalendarSet',
    call: (c) => c.deleteCalendarSet('cs1'),
    method: 'DELETE',
    path: '/v1/calendar-sets/cs1',
  },

  // --- Scheduling (M2.4) ---
  {
    name: 'listBookingLinks',
    call: (c) => c.listBookingLinks(),
    method: 'GET',
    path: '/v1/booking-links',
  },
  {
    name: 'createBookingLink',
    call: (c) => c.createBookingLink(BOOKING_LINK_INPUT),
    method: 'POST',
    path: '/v1/booking-links',
    body: BOOKING_LINK_INPUT,
  },
  {
    name: 'updateBookingLink',
    call: (c) => c.updateBookingLink('bl1', BOOKING_LINK_INPUT),
    method: 'PUT',
    path: '/v1/booking-links/bl1',
    body: BOOKING_LINK_INPUT,
  },
  {
    name: 'deleteBookingLink',
    call: (c) => c.deleteBookingLink('bl1'),
    method: 'DELETE',
    path: '/v1/booking-links/bl1',
  },
  { name: 'listBookings', call: (c) => c.listBookings(), method: 'GET', path: '/v1/bookings' },
  {
    name: 'cancelBooking',
    call: (c) => c.cancelBooking('bk1'),
    method: 'POST',
    path: '/v1/bookings/bk1/cancel',
  },
  { name: 'listPolls', call: (c) => c.listPolls(), method: 'GET', path: '/v1/polls' },
  {
    name: 'createPoll',
    call: (c) => c.createPoll(POLL_INPUT),
    method: 'POST',
    path: '/v1/polls',
    body: POLL_INPUT,
  },
  {
    name: 'confirmPoll',
    call: (c) => c.confirmPoll('p1', 'opt1'),
    method: 'POST',
    path: '/v1/polls/p1/confirm',
    body: { optionId: 'opt1' },
  },
  { name: 'deletePoll', call: (c) => c.deletePoll('p1'), method: 'DELETE', path: '/v1/polls/p1' },
  {
    name: 'proposeTime',
    call: (c) => c.proposeTime('e1', TIME_PROPOSAL_INPUT),
    method: 'POST',
    path: '/v1/events/e1/propose-time',
    body: TIME_PROPOSAL_INPUT,
  },
  {
    name: 'listProposals',
    call: (c) => c.listProposals('e1'),
    method: 'GET',
    path: '/v1/events/e1/proposals',
  },
  {
    name: 'acceptProposal',
    call: (c) => c.acceptProposal('e1', 'tp1'),
    method: 'POST',
    path: '/v1/events/e1/proposals/tp1/accept',
  },
  {
    name: 'declineProposal',
    call: (c) => c.declineProposal('e1', 'tp1'),
    method: 'POST',
    path: '/v1/events/e1/proposals/tp1/decline',
  },
  {
    name: 'getFreeBusy',
    call: (c) => c.getFreeBusy(['a@example.com', 'b@example.com'], '2026-08-01T00:00:00Z', '2026-08-08T00:00:00Z'),
    method: 'POST',
    path: '/v1/freebusy',
    body: {
      emails: ['a@example.com', 'b@example.com'],
      from: '2026-08-01T00:00:00Z',
      to: '2026-08-08T00:00:00Z',
    },
  },
  { name: 'getSettings', call: (c) => c.getSettings(), method: 'GET', path: '/v1/settings' },
  {
    name: 'updateSettings',
    call: (c) => c.updateSettings(USER_SETTINGS),
    method: 'PUT',
    path: '/v1/settings',
    body: USER_SETTINGS,
  },
];

describe('method contracts (path, verb, body, auth, response passthrough)', () => {
  for (const tc of methodCases) {
    it(`${tc.name} -> ${tc.method} ${tc.path}`, async () => {
      const echoBody = { case: tc.name };
      const { client, calls } = makeClient({ responses: [{ status: 200, body: echoBody }] });

      const result = await tc.call(client);

      expect(calls).toHaveLength(1);
      const req = calls[0];
      const url = new URL(req.url);
      expect(url.origin + url.pathname).toBe(`${BASE_URL}${tc.path}`);
      expect(req.method).toBe(tc.method);
      expect(req.headers.Authorization).toBe(`Bearer ${TOKEN}`);
      expect(req.headers['Content-Type']).toBe('application/json');

      if (tc.body !== undefined) {
        expect(req.body).toEqual(tc.body);
      } else {
        expect(req.body).toBeUndefined();
      }

      expect(result).toEqual(echoBody);
    });
  }
});

describe('triage power endpoints', () => {
  it('bulkThreadAction POSTs ids + action to /v1/mail/threads/bulk-actions', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { threads: [], failedIds: ['ghost'] } }],
    });
    const res = await client.bulkThreadAction({ threadIds: ['t1', 'ghost'], action: 'archive' });
    expect(res.failedIds).toEqual(['ghost']);
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/threads/bulk-actions');
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.body).toEqual({
      threadIds: ['t1', 'ghost'],
      action: 'archive',
    });
  });

  it('setThreadLabel POSTs labelId+add to the thread labels route', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { id: 't1' } }] });
    await client.setThreadLabel('t1', 'lbl1', true);
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/threads/t1/labels');
    expect(calls[0]!.body).toEqual({ labelId: 'lbl1', add: true });
  });

  it('unsnoozeThread DELETEs the snooze', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { id: 't1' } }] });
    await client.unsnoozeThread('t1');
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/threads/t1/snooze');
    expect(calls[0]!.method).toBe('DELETE');
  });

  it('unsubscribeThread POSTs and returns the method', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { method: 'one_click' } }] });
    const res = await client.unsubscribeThread('t1');
    expect(res.method).toBe('one_click');
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/threads/t1/unsubscribe');
  });

  it('archiveOlderThan POSTs the cutoff to /zero', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { archivedCount: 7 } }] });
    const res = await client.archiveOlderThan('2026-07-10T00:00:00Z');
    expect(res.archivedCount).toBe(7);
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/threads/zero');
    expect(calls[0]!.body).toEqual({ olderThan: '2026-07-10T00:00:00Z' });
  });

  it('prefs round-trip GET/PUT /v1/prefs', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { splitOrder: ['vip', 'important'] } }] });
    await client.getPrefs();
    await client.updatePrefs({ splitOrder: ['vip', 'important'] });
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/prefs');
    expect(calls[1]!.method).toBe('PUT');
  });

  it('listLabels GETs the labels list', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listLabels();
    expect(calls[0]!.url).toBe('https://api.calendium.test/v1/mail/labels');
    expect(calls[0]!.method).toBe('GET');
  });
});

// ---------------------------------------------------------------------------
// Public scheduling fetchers (M2.4) — standalone, unauthenticated.
//
// These back the public /book/{slug} and /poll/{token} visitor pages, which
// load before any signed-in ApiClient exists, so every one of them must work
// with no Authorization header at all.
// ---------------------------------------------------------------------------

const BOOKING_REQUEST: BookingRequest = {
  start: '2026-08-01T09:00:00Z',
  inviteeName: 'Ada Lovelace',
  inviteeEmail: 'ada@example.com',
  inviteeTimeZone: 'America/New_York',
  note: 'Looking forward to it',
};

const POLL_BALLOT: PollBallot = {
  voterEmail: 'ada@example.com',
  voterName: 'Ada Lovelace',
  choices: { opt1: 'yes', opt2: 'if_needed' },
};

describe('public scheduling fetchers', () => {
  it('fetchPublicBookingPage GETs /v1/public/booking/{slug} with no auth', async () => {
    const page = {
      slug: 'thirty-min',
      title: '30 Minute Meeting',
      description: null,
      ownerName: 'Ada Lovelace',
      durationMinutes: 30,
      timeZone: 'America/New_York',
    };
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: page }]);
    const result = await fetchPublicBookingPage(BASE_URL, 'thirty-min', fetchFn as unknown as typeof fetch);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/booking/thirty-min`);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.headers.Authorization).toBeUndefined();
    expect(result).toEqual(page);
  });

  it('fetchPublicSlots GETs /v1/public/booking/{slug}/slots with from/to and no auth', async () => {
    const slots = [{ start: '2026-08-01T09:00:00Z', end: '2026-08-01T09:30:00Z' }];
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: slots }]);
    const result = await fetchPublicSlots(
      BASE_URL,
      'thirty-min',
      '2026-08-01T00:00:00Z',
      '2026-08-08T00:00:00Z',
      fetchFn as unknown as typeof fetch
    );
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/public/booking/thirty-min/slots');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      from: '2026-08-01T00:00:00Z',
      to: '2026-08-08T00:00:00Z',
    });
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.headers.Authorization).toBeUndefined();
    expect(result).toEqual(slots);
  });

  it('createPublicBooking POSTs the booking request with no auth', async () => {
    const booking = { id: 'bk1', linkId: 'bl1', status: 'hold' };
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: booking }]);
    const result = await createPublicBooking(
      BASE_URL,
      'thirty-min',
      BOOKING_REQUEST,
      fetchFn as unknown as typeof fetch
    );
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/booking/thirty-min/bookings`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.headers.Authorization).toBeUndefined();
    expect(calls[0]!.headers['Content-Type']).toBe('application/json');
    expect(calls[0]!.body).toEqual(BOOKING_REQUEST);
    expect(result).toEqual(booking);
  });

  it('fetchPublicPoll GETs /v1/public/polls/{token} with no auth', async () => {
    const poll = { token: 'abc123', title: 'Team Sync', options: [], tallies: {} };
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: poll }]);
    const result = await fetchPublicPoll(BASE_URL, 'abc123', fetchFn as unknown as typeof fetch);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/polls/abc123`);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.headers.Authorization).toBeUndefined();
    expect(result).toEqual(poll);
  });

  it('votePublicPoll POSTs the ballot with no auth', async () => {
    const poll = { token: 'abc123', title: 'Team Sync', options: [], tallies: { opt1: { yes: 1, no: 0, ifNeeded: 0 } } };
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: poll }]);
    const result = await votePublicPoll(BASE_URL, 'abc123', POLL_BALLOT, fetchFn as unknown as typeof fetch);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/polls/abc123/votes`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.headers.Authorization).toBeUndefined();
    expect(calls[0]!.headers['Content-Type']).toBe('application/json');
    expect(calls[0]!.body).toEqual(POLL_BALLOT);
    expect(result).toEqual(poll);
  });

  it('strips trailing slashes from baseUrl', async () => {
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: {} }]);
    await fetchPublicBookingPage(`${BASE_URL}///`, 'thirty-min', fetchFn as unknown as typeof fetch);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/booking/thirty-min`);
  });

  it('maps 400 validation_failed to ApiRequestError(400, "validation_failed")', async () => {
    const { fetchFn } = createFakeFetch([
      {
        status: 400,
        body: { error: { code: 'validation_failed', message: 'inviteeEmail is required' } },
      },
    ]);
    await expect(
      createPublicBooking(BASE_URL, 'thirty-min', BOOKING_REQUEST, fetchFn as unknown as typeof fetch)
    ).rejects.toMatchObject({
      status: 400,
      code: 'validation_failed',
      message: 'inviteeEmail is required',
    });
  });

  it('maps 429 rate_limited to ApiRequestError(429, "rate_limited") so callers can branch on error.code', async () => {
    const { fetchFn } = createFakeFetch([
      {
        status: 429,
        body: { error: { code: 'rate_limited', message: 'too many requests, slow down' } },
      },
    ]);
    await expect(
      fetchPublicBookingPage(BASE_URL, 'thirty-min', fetchFn as unknown as typeof fetch)
    ).rejects.toBeInstanceOf(ApiRequestError);
    const { fetchFn: fetchFn2 } = createFakeFetch([
      {
        status: 429,
        body: { error: { code: 'rate_limited', message: 'too many requests, slow down' } },
      },
    ]);
    await expect(
      fetchPublicBookingPage(BASE_URL, 'thirty-min', fetchFn2 as unknown as typeof fetch)
    ).rejects.toMatchObject({
      status: 429,
      code: 'rate_limited',
      message: 'too many requests, slow down',
    });
  });

  it('maps 404 not_found to ApiRequestError for an unknown slug/token', async () => {
    const { fetchFn } = createFakeFetch([
      { status: 404, body: { error: { code: 'not_found', message: 'booking link not found' } } },
    ]);
    await expect(
      fetchPublicBookingPage(BASE_URL, 'ghost', fetchFn as unknown as typeof fetch)
    ).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
      message: 'booking link not found',
    });
  });

  it('uses the global fetch when no fetchImpl is provided', async () => {
    const page = { slug: 'thirty-min', title: '30 Minute Meeting' };
    const { fetchFn, calls } = createFakeFetch([{ status: 200, body: page }]);
    vi.stubGlobal('fetch', fetchFn);
    try {
      const result = await fetchPublicBookingPage(BASE_URL, 'thirty-min');
      expect(result).toEqual(page);
      expect(calls[0]!.url).toBe(`${BASE_URL}/v1/public/booking/thirty-min`);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// ---------------------------------------------------------------------------
// Teams (M2.7)
// ---------------------------------------------------------------------------

describe('teams (M2.7)', () => {
  const TEAM = { id: 'team_1', name: 'Ops', createdBy: 'user_1', createdAt: '2026-07-19T00:00:00Z' };
  const MEMBER = {
    teamId: 'team_1',
    userId: 'user_2',
    role: 'admin',
    shareReadStatuses: false,
    joinedAt: '2026-07-19T00:00:00Z',
  };
  const INVITATION = {
    id: 'inv_1',
    teamId: 'team_1',
    email: 'new@example.com',
    role: 'member',
    invitedBy: 'user_1',
    status: 'pending',
    expiresAt: '2026-08-02T00:00:00Z',
    createdAt: '2026-07-19T00:00:00Z',
  };

  it('listTeams GETs /v1/teams with the bearer token', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [TEAM] }] });
    const result = await client.listTeams();
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams`);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(result).toEqual([TEAM]);
  });

  it('createTeam POSTs {name} and returns the created team', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 201, body: TEAM }] });
    const result = await client.createTeam('Ops');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.body).toEqual({ name: 'Ops' });
    expect(result).toEqual(TEAM);
  });

  it('getTeam GETs /v1/teams/{id} and returns team + members', async () => {
    const payload = { team: TEAM, members: [MEMBER] };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: payload }] });
    const result = await client.getTeam('team_1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1`);
    expect(calls[0]!.method).toBe('GET');
    expect(result).toEqual(payload);
  });

  it('renameTeam PATCHes /v1/teams/{id} with {name}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { ...TEAM, name: 'Platform' } }] });
    await client.renameTeam('team_1', 'Platform');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1`);
    expect(calls[0]!.method).toBe('PATCH');
    expect(calls[0]!.body).toEqual({ name: 'Platform' });
  });

  it('deleteTeam DELETEs /v1/teams/{id} and resolves undefined on 204', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    const result = await client.deleteTeam('team_1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1`);
    expect(calls[0]!.method).toBe('DELETE');
    expect(result).toBeUndefined();
  });

  it('setMemberRole PATCHes /v1/teams/{id}/members/{userId} with {role}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: MEMBER }] });
    const result = await client.setMemberRole('team_1', 'user_2', 'admin');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/members/user_2`);
    expect(calls[0]!.method).toBe('PATCH');
    expect(calls[0]!.body).toEqual({ role: 'admin' });
    expect(result).toEqual(MEMBER);
  });

  it('setShareReadStatuses PUTs /v1/teams/{id}/read-status-sharing with {share}', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { ...MEMBER, shareReadStatuses: true } }],
    });
    await client.setShareReadStatuses('team_1', true);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/read-status-sharing`);
    expect(calls[0]!.method).toBe('PUT');
    expect(calls[0]!.body).toEqual({ share: true });
  });

  it('removeMember DELETEs /v1/teams/{id}/members/{userId}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.removeMember('team_1', 'user_2');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/members/user_2`);
    expect(calls[0]!.method).toBe('DELETE');
  });

  it('invite POSTs /v1/teams/{id}/invitations with {email, role}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 201, body: INVITATION }] });
    const result = await client.invite('team_1', 'new@example.com', 'member');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/invitations`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.body).toEqual({ email: 'new@example.com', role: 'member' });
    expect(result).toEqual(INVITATION);
  });

  it('listInvitations GETs /v1/teams/{id}/invitations', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [INVITATION] }] });
    const result = await client.listInvitations('team_1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/invitations`);
    expect(calls[0]!.method).toBe('GET');
    expect(result).toEqual([INVITATION]);
  });

  it('revokeInvitation DELETEs /v1/teams/{id}/invitations/{invitationId}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.revokeInvitation('team_1', 'inv_1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/team_1/invitations/inv_1`);
    expect(calls[0]!.method).toBe('DELETE');
  });

  it('acceptInvitation POSTs /v1/invitations/accept with {token}', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: TEAM }] });
    const result = await client.acceptInvitation('raw-invite-token');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/invitations/accept`);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.body).toEqual({ token: 'raw-invite-token' });
    expect(result).toEqual(TEAM);
  });

  it('percent-encodes path params in team routes', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: TEAM }] });
    await client.getTeam('team/../1?x=1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/teams/${encodeURIComponent('team/../1?x=1')}`);
    const { client: client2, calls: calls2 } = makeClient({ responses: [{ status: 200, body: MEMBER }] });
    await client2.setMemberRole('team 1', 'user#2', 'member');
    expect(calls2[0]!.url).toBe(`${BASE_URL}/v1/teams/team%201/members/user%232`);
  });

  it('maps a 404 non-member response to ApiRequestError(404, "not_found")', async () => {
    const { client } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'resource not found' } } }],
    });
    await expect(client.getTeam('team_x')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    });
  });

  it('maps a 403 insufficient-role response to ApiRequestError(403, "forbidden")', async () => {
    const { client } = makeClient({
      responses: [{ status: 403, body: { error: { code: 'forbidden', message: 'insufficient team role' } } }],
    });
    await expect(client.deleteTeam('team_1')).rejects.toMatchObject({
      status: 403,
      code: 'forbidden',
    });
  });

  it('maps a 409 last-owner response to ApiRequestError(409, "conflict")', async () => {
    const { client } = makeClient({
      responses: [{ status: 409, body: { error: { code: 'conflict', message: 'a team must keep at least one owner' } } }],
    });
    await expect(client.removeMember('team_1', 'user_1')).rejects.toMatchObject({
      status: 409,
      code: 'conflict',
    });
  });
});

// ---------------------------------------------------------------------------
// Shared conversations (M2.7) — thread share links + the public share view
// ---------------------------------------------------------------------------

describe('thread shares', () => {
  it('shareThread POSTs the audience payload and returns the share + one-time token', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 201, body: { share: { id: 'sh1', threadId: 't1' }, token: 'raw-once' } }],
    });
    const input: ShareThreadInput = {
      audience: 'team',
      teamId: 'team1',
      expiresAt: '2026-08-01T00:00:00Z',
    };
    const result = await client.shareThread('t1', input);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/threads/t1/share`);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].body).toEqual(input);
    expect(result).toEqual({ share: { id: 'sh1', threadId: 't1' }, token: 'raw-once' });
  });

  it('percent-encodes the thread id in share paths', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 201, body: { share: {}, token: 't' } }] });
    await client.shareThread('t/1 x', { audience: 'external' });
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/threads/t%2F1%20x/share`);
  });

  it('listThreadShares GETs the share list for a thread', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: [{ id: 'sh1', audience: 'external' }] }],
    });
    const shares = await client.listThreadShares('t1');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/threads/t1/shares`);
    expect(calls[0].method).toBe('GET');
    expect(shares).toEqual([{ id: 'sh1', audience: 'external' }]);
  });

  it('revokeThreadShare DELETEs the share by id (encoded) and resolves on 204', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.revokeThreadShare('t1', 'sh 1');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/threads/t1/shares/sh%201`);
    expect(calls[0].method).toBe('DELETE');
  });

  it('getSharedThread GETs the public view with the token percent-encoded', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { subject: 'Launch plan', audience: 'external', messages: [] } }],
    });
    const view = await client.getSharedThread('tok/123');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/shared/threads/tok%2F123`);
    expect(view.subject).toBe('Launch plan');
  });

  it('getSharedThread omits Authorization when signed out (anonymous external viewer)', async () => {
    const { client, calls } = makeClient({
      token: null,
      responses: [{ status: 200, body: { subject: 's', audience: 'external', messages: [] } }],
    });
    await client.getSharedThread('tok123');
    expect(calls[0].headers.Authorization).toBeUndefined();
  });

  it('getSharedThread sends the bearer when signed in (team-audience viewer)', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { subject: 's', audience: 'team', messages: [] } }],
    });
    await client.getSharedThread('tok123');
    expect(calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
  });

  it('maps unknown/revoked tokens to ApiRequestError(404, "not_found")', async () => {
    const { client } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'not found' } } }],
    });
    await expect(client.getSharedThread('nope')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    });
  });
});

// ---------------------------------------------------------------------------
// Team comments (M2.7)
// ---------------------------------------------------------------------------

describe('team comments', () => {
  it('listComments GETs the comments with teamId as a query parameter', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { comments: [{ id: 'c1', body: 'hello' }] } }],
    });
    const result = await client.listComments('t1', 'team 1');
    const [path, query] = calls[0].url.split('?');
    expect(path).toBe(`${BASE_URL}/v1/mail/threads/t1/comments`);
    expect(searchParamsToObject(new URLSearchParams(query))).toEqual({ teamId: 'team 1' });
    expect(result.comments).toEqual([{ id: 'c1', body: 'hello' }]);
  });

  it('addComment POSTs the teamId + body payload', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 201, body: { id: 'c9', body: 'hi @ada@example.com' } }],
    });
    const input: CommentInput = { teamId: 'team1', body: 'hi @ada@example.com' };
    const comment = await client.addComment('t1', input);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/mail/threads/t1/comments`);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].body).toEqual(input);
    expect(comment.id).toBe('c9');
  });

  it('updateComment PATCHes the body by comment id', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { id: 'c1', body: 'edited' } }],
    });
    await client.updateComment('c1', 'edited');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/comments/c1`);
    expect(calls[0].method).toBe('PATCH');
    expect(calls[0].body).toEqual({ body: 'edited' });
  });

  it('deleteComment DELETEs by comment id and resolves on 204', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.deleteComment('c 1');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/comments/c%201`);
    expect(calls[0].method).toBe('DELETE');
  });

  it('maps a non-member 404 without leaking existence', async () => {
    const { client } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'not found' } } }],
    });
    await expect(client.listComments('t1', 'ghost')).rejects.toMatchObject({
      status: 404,
      code: 'not_found',
    });
  });
});

// ---------------------------------------------------------------------------
// EA delegation (M2.7 Task 15) — grants, audit, and act-as
// ---------------------------------------------------------------------------

describe('ApiClient delegation endpoints', () => {
  it('createDelegation POSTs /v1/delegations with assistantEmail and scopes', async () => {
    const { client, calls } = makeClient();
    await client.createDelegation('ea@example.com', ['mail_read', 'calendar_write']);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations`);
    expect(calls[0].method).toBe('POST');
    expect(calls[0].body).toEqual({
      assistantEmail: 'ea@example.com',
      scopes: ['mail_read', 'calendar_write'],
    });
  });

  it('listDelegations GETs /v1/delegations and returns both sides', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { asPrincipal: [], asAssistant: [] } }],
    });
    const result = await client.listDelegations();
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations`);
    expect(calls[0].method).toBe('GET');
    expect(result).toEqual({ asPrincipal: [], asAssistant: [] });
  });

  it('acceptDelegation POSTs to a percent-encoded id path', async () => {
    const { client, calls } = makeClient();
    await client.acceptDelegation('del/1?x');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations/${encodeURIComponent('del/1?x')}/accept`);
    expect(calls[0].method).toBe('POST');
  });

  it('revokeDelegation DELETEs the percent-encoded id path', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await client.revokeDelegation('del 2');
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations/${encodeURIComponent('del 2')}`);
    expect(calls[0].method).toBe('DELETE');
  });

  it('listDelegationAudit GETs /v1/delegations/audit without a limit by default', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { entries: [] } }] });
    const result = await client.listDelegationAudit();
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations/audit`);
    expect(result.entries).toEqual([]);
  });

  it('listDelegationAudit passes an explicit limit as a query param', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: { entries: [] } }] });
    await client.listDelegationAudit(50);
    expect(calls[0].url).toBe(`${BASE_URL}/v1/delegations/audit?limit=50`);
  });
});

describe('ApiClient.actAs', () => {
  it('adds X-Calendium-Act-As on delegable mail/calendar routes', async () => {
    const { client, calls } = makeClient();
    const acting = client.actAs('user_principal');
    await acting.getThread('t1');
    await acting.listCalendars();
    await acting.listEvents('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z');
    await acting.search('hello');
    await acting.getAvailability('2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', 30);
    for (const call of calls) {
      expect(call.headers['X-Calendium-Act-As']).toBe('user_principal');
    }
  });

  it('never adds the header on non-delegable routes (backend rejects act-as there)', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { asPrincipal: [], asAssistant: [] } }],
    });
    const acting = client.actAs('user_principal');
    await acting.listDelegations();
    await acting.getMe();
    await acting.listTeams();
    await acting.listAccounts();
    for (const call of calls) {
      expect(call.headers['X-Calendium-Act-As']).toBeUndefined();
    }
  });

  it('omits the header on denied team/collab sub-paths under delegable prefixes', async () => {
    const { client, calls } = makeClient({
      responses: [
        { status: 201, body: { share: {}, token: 'tok' } },
        { status: 200, body: [] },
        { status: 200, body: { comments: [] } },
        { status: 200, body: [] },
        { status: 200, body: [] },
        { status: 200, body: [] },
      ],
    });
    const acting = client.actAs('user_principal');
    await acting.shareThread('t1', { audience: 'external' } as ShareThreadInput);
    await acting.listThreadShares('t1');
    await acting.listComments('t1', 'team_1');
    await acting.teamThreadActivity('t1');
    await acting.listSnippets();
    await acting.listCalendarShares('c1');
    expect(calls.length).toBe(6);
    for (const call of calls) {
      expect(call.headers['X-Calendium-Act-As'], call.url).toBeUndefined();
    }
  });

  it('keeps the Authorization bearer token on delegated requests', async () => {
    const { client, calls } = makeClient();
    await client.actAs('user_principal').actOnThread('t1', 'archive');
    expect(calls[0].headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(calls[0].headers['X-Calendium-Act-As']).toBe('user_principal');
  });

  it('returns a NEW client and leaves the original free of the header', async () => {
    const { client, calls } = makeClient();
    const acting = client.actAs('user_principal');
    expect(acting).not.toBe(client);
    await client.getThread('t1');
    expect(calls[0].headers['X-Calendium-Act-As']).toBeUndefined();
    await acting.getThread('t1');
    expect(calls[1].headers['X-Calendium-Act-As']).toBe('user_principal');
  });
});

describe('isDelegablePath', () => {
  it('accepts exactly the backend delegationScopeForRoute route groups', () => {
    const delegable = [
      '/v1/search',
      '/v1/mail',
      '/v1/mail/threads',
      '/v1/mail/threads/t1/actions',
      '/v1/events',
      '/v1/events/e1/rsvp',
      '/v1/calendars',
      '/v1/calendars/c1',
      '/v1/availability',
    ];
    for (const path of delegable) {
      expect(isDelegablePath(path), path).toBe(true);
    }
    const notDelegable = [
      // Team/collab sub-surfaces under the delegable prefixes (I1 fix).
      '/v1/mail/threads/t1/share',
      '/v1/mail/threads/t1/shares',
      '/v1/mail/threads/t1/shares/sh1',
      '/v1/mail/threads/t1/comments',
      '/v1/mail/threads/t1/team-activity',
      '/v1/mail/snippets',
      '/v1/mail/snippets/sn1',
      '/v1/calendars/c1/shares',
      '/v1/calendars/c1/shares/sh1',
      '/v1/comments/cm1',
      '/v1/teams',
      '/v1/delegations',
      '/v1/delegations/audit',
      '/v1/billing/subscription',
      '/v1/accounts',
      '/v1/devices',
      '/v1/me',
      '/v1/mailbox', // prefix must not match beyond the exact segment
      '/v1/eventsx',
      '/v1/instance',
    ];
    for (const path of notDelegable) {
      expect(isDelegablePath(path), path).toBe(false);
    }
  });
});

// ---------------------------------------------------------------------------
// Shared calendars & team availability (M2.7 Tasks 12–13)
// ---------------------------------------------------------------------------

describe('calendar shares & team availability', () => {
  const SHARE = {
    id: 'sh_1',
    calendarId: 'cal_1',
    granteeTeamId: 'team_1',
    permission: 'free_busy',
    createdBy: 'user_1',
    createdAt: '2026-07-07T09:00:00Z',
  };

  it('lists a calendar’s shares via GET /v1/calendars/{id}/shares', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [SHARE] }] });
    const shares = await client.listCalendarShares('cal_1');
    expect(shares).toEqual([SHARE]);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/calendars/cal_1/shares`);
  });

  it('shares a calendar with a team via POST, passing the input verbatim', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: SHARE }] });
    const share = await client.shareCalendar('cal_1', {
      granteeTeamId: 'team_1',
      permission: 'free_busy',
    });
    expect(share).toEqual(SHARE);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/calendars/cal_1/shares`);
    expect(calls[0]!.body).toEqual({ granteeTeamId: 'team_1', permission: 'free_busy' });
  });

  it('updates a share’s permission via PATCH .../shares/{shareId}', async () => {
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { ...SHARE, permission: 'editor' } }],
    });
    await client.updateCalendarShare('cal_1', 'sh_1', 'editor');
    expect(calls[0]!.method).toBe('PATCH');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/calendars/cal_1/shares/sh_1`);
    expect(calls[0]!.body).toEqual({ permission: 'editor' });
  });

  it('revokes a share via DELETE and resolves on 204', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await expect(client.revokeCalendarShare('cal_1', 'sh_1')).resolves.toBeUndefined();
    expect(calls[0]!.method).toBe('DELETE');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/calendars/cal_1/shares/sh_1`);
  });

  it('fetches team availability with RFC 3339 from/to query params', async () => {
    const rows = [
      {
        userId: 'user_1',
        shared: true,
        busy: [{ start: '2026-07-07T10:00:00Z', end: '2026-07-07T11:00:00Z' }],
      },
      { userId: 'user_2', shared: false, busy: [] },
    ];
    const { client, calls } = makeClient({ responses: [{ status: 200, body: rows }] });
    const got = await client.teamAvailability(
      'team_1',
      '2026-07-07T00:00:00Z',
      '2026-07-08T00:00:00Z'
    );
    expect(got).toEqual(rows);
    expect(calls[0]!.method).toBe('GET');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/teams/team_1/availability');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      from: '2026-07-07T00:00:00Z',
      to: '2026-07-08T00:00:00Z',
    });
  });

  it('percent-encodes path params in share and availability routes', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listCalendarShares('cal/1');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/calendars/${encodeURIComponent('cal/1')}/shares`);

    const { client: c2, calls: calls2 } = makeClient({ responses: [{ status: 204 }] });
    await c2.revokeCalendarShare('cal 1', 'sh#2');
    expect(calls2[0]!.url).toBe(`${BASE_URL}/v1/calendars/cal%201/shares/sh%232`);

    const { client: c3, calls: calls3 } = makeClient({ responses: [{ status: 200, body: [] }] });
    await c3.teamAvailability('team 1', '2026-07-07T00:00:00Z', '2026-07-08T00:00:00Z');
    expect(new URL(calls3[0]!.url).pathname).toBe('/v1/teams/team%201/availability');
  });

  it('maps a 404 non-member availability response to ApiRequestError(404)', async () => {
    const { client } = makeClient({
      responses: [{ status: 404, body: { error: { code: 'not_found', message: 'resource not found' } } }],
    });
    await expect(
      client.teamAvailability('team_x', '2026-07-07T00:00:00Z', '2026-07-08T00:00:00Z')
    ).rejects.toMatchObject({ status: 404, code: 'not_found' });
  });
});

// ---------------------------------------------------------------------------
// Event notes (M2.8 Task 4)
// ---------------------------------------------------------------------------

describe('event notes', () => {
  const NOTE = {
    eventId: 'ev1',
    bodyMd: '# Prep\n- read the doc',
    links: ['https://notion.so/doc'],
    updatedAt: '2026-07-20T12:00:00Z',
  };

  it('getEventNote GETs /v1/events/{id}/note with the id percent-encoded', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: NOTE }] });
    const result = await client.getEventNote('ev 1');
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/events/ev%201/note`);
    expect(calls[0]!.headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(result).toEqual(NOTE);
  });

  it('getEventNote resolves an empty note for events without one (200, never 404)', async () => {
    const empty = { eventId: 'ev1', bodyMd: '', links: [], updatedAt: '0001-01-01T00:00:00Z' };
    const { client } = makeClient({ responses: [{ status: 200, body: empty }] });
    await expect(client.getEventNote('ev1')).resolves.toEqual(empty);
  });

  it('putEventNote PUTs the { bodyMd, links } payload and returns the stored note', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: NOTE }] });
    const result = await client.putEventNote('ev1', '# Prep\n- read the doc', [
      'https://notion.so/doc',
    ]);
    expect(calls[0]!.method).toBe('PUT');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/events/ev1/note`);
    expect(calls[0]!.body).toEqual({
      bodyMd: '# Prep\n- read the doc',
      links: ['https://notion.so/doc'],
    });
    expect(result).toEqual(NOTE);
  });

  it('maps a rejected link to ApiRequestError(400)', async () => {
    const { client } = makeClient({
      responses: [
        { status: 400, body: { error: { code: 'validation_failed', message: 'bad link' } } },
      ],
    });
    await expect(client.putEventNote('ev1', 'x', ['javascript:alert(1)'])).rejects.toMatchObject({
      status: 400,
      code: 'validation_failed',
    });
  });
});

// ---------------------------------------------------------------------------
// Calendar automation preferences (M2.8 Task 5)
// ---------------------------------------------------------------------------

describe('calendar automation preferences', () => {
  const PREFS: CalendarPrefs = {
    timeZone: 'Europe/Amsterdam',
    workDays: [1, 2, 3, 4, 5],
    workdayStartMinutes: 540,
    workdayEndMinutes: 1020,
    focusGoalMinutesPerWeek: 600,
    focusAutoDecline: true,
    focusDeclineMessage: 'Deep work — back later.',
    autoBufferMinutes: 10,
    oooAutoDecline: false,
    oooDeclineMessage: '',
    travelBuffers: true,
    travelMode: 'transit',
    leaveAlerts: true,
    homeLat: 52.37,
    homeLon: 4.89,
    weatherEnabled: true,
  };

  it('getCalendarPrefs GETs /v1/prefs/calendar and returns the document', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: PREFS }] });
    const result = await client.getCalendarPrefs();
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/prefs/calendar`);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.headers.Authorization).toBe(`Bearer ${TOKEN}`);
    expect(result).toEqual(PREFS);
  });

  it('updateCalendarPrefs PATCHes only the provided fields', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: PREFS }] });
    const patch: CalendarPrefsPatch = { autoBufferMinutes: 10, travelMode: 'transit' };
    const result = await client.updateCalendarPrefs(patch);
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/prefs/calendar`);
    expect(calls[0]!.method).toBe('PATCH');
    expect(calls[0]!.body).toEqual({ autoBufferMinutes: 10, travelMode: 'transit' });
    expect(result).toEqual(PREFS);
  });

  it('maps a 400 validation response to ApiRequestError(400)', async () => {
    const { client } = makeClient({
      responses: [
        { status: 400, body: { error: { code: 'validation_failed', message: 'validation failed' } } },
      ],
    });
    await expect(client.updateCalendarPrefs({ autoBufferMinutes: 3 })).rejects.toMatchObject({
      status: 400,
      code: 'validation_failed',
    });
  });

  it('maps a 402 paywall response to ApiRequestError(402)', async () => {
    const { client } = makeClient({
      responses: [
        { status: 402, body: { error: { code: 'payment_required', message: 'payment required' } } },
      ],
    });
    await expect(client.getCalendarPrefs()).rejects.toMatchObject({
      status: 402,
      code: 'payment_required',
    });
  });
});

// ---------------------------------------------------------------------------
// Tasks (M2.8)
// ---------------------------------------------------------------------------

describe('tasks', () => {
  const TASK = {
    id: 'task_1',
    title: 'Write brief',
    notes: null,
    due: null,
    allDayDue: false,
    scheduledStart: null,
    scheduledEnd: null,
    completedAt: null,
    source: 'local',
    sourceUrl: null,
    position: 1024,
    createdAt: '2026-07-19T12:00:00Z',
    updatedAt: '2026-07-19T12:00:00Z',
  };

  it('lists tasks with a bare GET /v1/tasks when no query is given', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [TASK] }] });
    const got = await client.listTasks();
    expect(got).toEqual([TASK]);
    expect(calls[0]!.method).toBe('GET');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/tasks`);
    expect(calls[0]!.body).toBeUndefined();
  });

  it('encodes list query params; boolean flags spell "1"', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listTasks({
      from: '2026-07-20T00:00:00Z',
      to: '2026-07-27T00:00:00Z',
      unscheduled: true,
      includeCompleted: true,
    });
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/tasks');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      from: '2026-07-20T00:00:00Z',
      to: '2026-07-27T00:00:00Z',
      unscheduled: '1',
      includeCompleted: '1',
    });
  });

  it('encodes the due range and omits false flags', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.listTasks({
      dueFrom: '2026-07-20T00:00:00Z',
      dueTo: '2026-07-21T00:00:00Z',
      unscheduled: false,
    });
    expect(searchParamsToObject(new URL(calls[0]!.url).searchParams)).toEqual({
      dueFrom: '2026-07-20T00:00:00Z',
      dueTo: '2026-07-21T00:00:00Z',
    });
  });

  it('creates a task via POST /v1/tasks with the input as the body', async () => {
    const input: TaskInput = {
      title: 'Write brief',
      notes: 'details',
      due: '2026-07-21T17:00:00Z',
      position: 512,
    };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: TASK }] });
    const got = await client.createTask(input);
    expect(got).toEqual(TASK);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/tasks`);
    expect(calls[0]!.body).toEqual(input);
  });

  it('PATCHes a task preserving explicit nulls and dropping undefined keys', async () => {
    const patch: TaskPatch = { title: 'renamed', due: null, notes: undefined, position: 2048 };
    const { client, calls } = makeClient({
      responses: [{ status: 200, body: { ...TASK, title: 'renamed' } }],
    });
    await client.updateTask('task_1', patch);
    expect(calls[0]!.method).toBe('PATCH');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/tasks/task_1`);
    const body = calls[0]!.body as Record<string, unknown>;
    expect(body).toEqual({ title: 'renamed', due: null, position: 2048 });
    expect('notes' in body).toBe(false);
    expect(body.due).toBeNull();
  });

  it('percent-encodes the task id in every path', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: TASK }] });
    await client.updateTask('task 1', { title: 'x' });
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/tasks/task%201');

    const { client: c2, calls: calls2 } = makeClient({ responses: [{ status: 200, body: TASK }] });
    await c2.completeTask('task/2');
    expect(calls2[0]!.url).toBe(`${BASE_URL}/v1/tasks/${encodeURIComponent('task/2')}/complete`);
  });

  it('completes and reopens via POST subroutes with no body', async () => {
    const done = { ...TASK, completedAt: '2026-07-19T13:00:00Z' };
    const { client, calls } = makeClient({ responses: [{ status: 200, body: done }] });
    const got = await client.completeTask('task_1');
    expect(got).toEqual(done);
    expect(calls[0]!.method).toBe('POST');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/tasks/task_1/complete`);
    expect(calls[0]!.body).toBeUndefined();

    const { client: c2, calls: calls2 } = makeClient({ responses: [{ status: 200, body: TASK }] });
    await c2.reopenTask('task_1');
    expect(calls2[0]!.method).toBe('POST');
    expect(calls2[0]!.url).toBe(`${BASE_URL}/v1/tasks/task_1/reopen`);
    expect(calls2[0]!.body).toBeUndefined();
  });

  it('deletes via DELETE and resolves undefined on 204', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 204 }] });
    await expect(client.deleteTask('task_1')).resolves.toBeUndefined();
    expect(calls[0]!.method).toBe('DELETE');
    expect(calls[0]!.url).toBe(`${BASE_URL}/v1/tasks/task_1`);
  });

  it('surfaces the error envelope as ApiRequestError', async () => {
    const { client } = makeClient({
      responses: [
        { status: 402, body: { error: { code: 'payment_required', message: 'subscribe' } } },
      ],
    });
    await expect(client.createTask({ title: 't' })).rejects.toMatchObject({
      status: 402,
      code: 'payment_required',
    });
    await expect(client.createTask({ title: 't' })).rejects.toBeInstanceOf(ApiRequestError);
  });
});

describe('weather (M2.8 Task 13)', () => {
  const rows = [
    { date: '2026-07-19', code: 3, highCelsius: 24.6, lowCelsius: 13.1, precipChance: 20 },
    { date: '2026-07-20', code: 61, highCelsius: 19.2, lowCelsius: 12.4, precipChance: 85 },
  ];

  it('requests GET /v1/weather with lat/lon/tz/days and returns the rows', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: rows }] });
    const got = await client.getWeather(52.52, 13.405, 'Europe/Berlin', 5);
    expect(got).toEqual(rows);
    expect(calls[0]!.method).toBe('GET');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/weather');
    expect(searchParamsToObject(url.searchParams)).toEqual({
      lat: '52.52',
      lon: '13.405',
      tz: 'Europe/Berlin',
      days: '5',
    });
  });

  it('defaults days to 7', async () => {
    const { client, calls } = makeClient({ responses: [{ status: 200, body: [] }] });
    await client.getWeather(0, 0, 'UTC');
    expect(new URL(calls[0]!.url).searchParams.get('days')).toBe('7');
  });

  it('maps a 501 unconfigured response to ApiRequestError(501, not_implemented)', async () => {
    const { client } = makeClient({
      responses: [
        { status: 501, body: { error: { code: 'not_implemented', message: 'no vendor' } } },
      ],
    });
    await expect(client.getWeather(0, 0, 'UTC')).rejects.toMatchObject({
      status: 501,
      code: 'not_implemented',
    });
  });
});

// ---------------------------------------------------------------------------
// Places — location autocomplete (M2.8 Task 11)
// ---------------------------------------------------------------------------

describe('places', () => {
  it('GETs /v1/places/autocomplete with a percent-encoded user query', async () => {
    const places: Place[] = [
      { name: 'Alexanderplatz', address: 'Alexanderplatz, Mitte, Berlin, Germany', lat: 52.5219, lon: 13.4132 },
    ];
    const { client, calls } = makeClient({ responses: [{ status: 200, body: places }] });
    const got = await client.autocompletePlaces('café & bar berlin');
    expect(got).toEqual(places);
    expect(calls[0]!.method).toBe('GET');
    const url = new URL(calls[0]!.url);
    expect(url.pathname).toBe('/v1/places/autocomplete');
    expect(searchParamsToObject(url.searchParams)).toEqual({ q: 'café & bar berlin' });
    // Raw wire form must be percent-encoded — no raw multibyte chars or
    // bare ampersands from user input in the URL.
    expect(calls[0]!.url).toContain('caf%C3%A9%20%26%20bar%20berlin');
  });

  it('maps the unconfigured-server 501 to ApiRequestError(not_implemented)', async () => {
    const { client } = makeClient({
      responses: [
        {
          status: 501,
          body: { error: { code: 'not_implemented', message: 'maps are not available on this instance' } },
        },
      ],
    });
    await expect(client.autocompletePlaces('berlin')).rejects.toMatchObject({
      status: 501,
      code: 'not_implemented',
    });
  });
});
