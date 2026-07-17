import { describe, expect, it, vi } from 'vitest';
import { ApiClient, ApiRequestError, fetchInstance } from './client';
import type { AiComposeRequest, DraftInput, EventInput, EventPatch } from './types';

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

const AI_REQUEST: AiComposeRequest = {
  action: 'compose',
  prompt: 'Draft a reply',
  threadId: 't1',
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
