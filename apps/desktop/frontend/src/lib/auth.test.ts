import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ServerConfig } from './server-config';

// auth.ts pulls its server config from ./server-config; mocking it lets these
// tests drive getAuthClient()/signInEmail/etc. without touching real
// localStorage-backed server discovery (covered by server-config.test.ts).
const { getActiveServerConfigMock } = vi.hoisted(() => ({
  getActiveServerConfigMock: vi.fn<() => ServerConfig | null>(),
}));
vi.mock('./server-config', () => ({
  getActiveServerConfig: getActiveServerConfigMock,
}));

// ---------------------------------------------------------------------------
// Fake global fetch harness.
//
// auth.ts talks to the network two ways:
//  1. Through the real `better-auth/react` client (signIn.email, signUp.email,
//     getSession, signOut) — which, per @better-fetch/fetch's getFetch(), falls
//     back to whatever `globalThis.fetch` is bound to AT THE TIME the client is
//     constructed (createAuthClient() captures `customFetchImpl: fetch` once).
//  2. Directly, via its own `fetch(...)` calls (verifyOtt, getAccessToken).
//
// Rather than reimplement Better Auth's client, we stub global fetch and
// route by URL substring — the real client and auth.ts's own code both end up
// calling this. Requests are matched on the request path (e.g. "/sign-in/email")
// regardless of how the library joins baseURL + path.
// ---------------------------------------------------------------------------

interface RouteSpec {
  status: number;
  body?: unknown;
  headers?: Record<string, string>;
  /** Simulate a network failure (fetch() rejects) instead of resolving. */
  reject?: boolean;
}

interface RecordedCall {
  url: string;
  method: string;
  body: unknown;
}

function installFetch(routes: Record<string, RouteSpec | RouteSpec[]>) {
  const calls: RecordedCall[] = [];
  const cursors = new Map<string, number>();
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = String(input instanceof Request ? input.url : input);
    const method = init.method ?? (input instanceof Request ? input.method : 'GET');
    let body: unknown;
    if (typeof init.body === 'string') {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    calls.push({ url, method, body });
    const matchKey = Object.keys(routes).find((k) => url.includes(k));
    if (!matchKey) {
      throw new Error(`auth.test.ts fake fetch: no route configured for ${method} ${url}`);
    }
    const spec = routes[matchKey]!;
    const list = Array.isArray(spec) ? spec : [spec];
    const idx = cursors.get(matchKey) ?? 0;
    cursors.set(matchKey, idx + 1);
    const chosen = list[Math.min(idx, list.length - 1)]!;
    if (chosen.reject) throw new TypeError('network error (simulated)');
    const responseBody = chosen.body === undefined ? null : JSON.stringify(chosen.body);
    return new Response(responseBody, {
      status: chosen.status,
      headers: { 'content-type': 'application/json', ...chosen.headers },
    });
  });
  vi.stubGlobal('fetch', fn);
  return { calls, fn };
}

// A fresh authBaseUrl per call forces auth.ts's internal client cache
// (module-level `client`/`clientBaseUrl`) to rebuild, so the new client binds
// to *this* test's fetch stub instead of reusing one captured by an earlier
// test (see the getFetch() note above).
let authUrlCounter = 0;
function configureServer(overrides: Partial<ServerConfig> = {}): ServerConfig {
  authUrlCounter += 1;
  const config: ServerConfig = {
    serverUrl: 'https://api.test',
    authBaseUrl: `https://auth-${authUrlCounter}.test/api/auth`,
    authProviders: ['email'],
    mode: 'cloud',
    name: 'Test',
    features: { billing: false, google: false, microsoft: false, ai: false, push: false },
    undoSendSeconds: 15,
    webUrl: '',
    ...overrides,
  };
  getActiveServerConfigMock.mockReturnValue(config);
  return config;
}

beforeEach(() => {
  vi.resetModules();
  localStorage.clear();
  getActiveServerConfigMock.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('getStoredToken / clearStoredToken', () => {
  it('returns null when nothing is stored', async () => {
    const { getStoredToken } = await import('./auth');
    expect(getStoredToken()).toBeNull();
  });

  it('reads back a token written under the bearer-token localStorage key', async () => {
    localStorage.setItem('calendium.bearerToken', 'abc123');
    const { getStoredToken } = await import('./auth');
    expect(getStoredToken()).toBe('abc123');
  });

  it('clearStoredToken removes the persisted token', async () => {
    localStorage.setItem('calendium.bearerToken', 'abc123');
    const { clearStoredToken, getStoredToken } = await import('./auth');
    clearStoredToken();
    expect(getStoredToken()).toBeNull();
    expect(localStorage.getItem('calendium.bearerToken')).toBeNull();
  });
});

describe('getAuthClient', () => {
  it('returns null when no server is configured', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    const { getAuthClient } = await import('./auth');
    expect(getAuthClient()).toBeNull();
  });

  it('returns a client once a server with an authBaseUrl is configured', async () => {
    configureServer();
    const { getAuthClient } = await import('./auth');
    expect(getAuthClient()).not.toBeNull();
  });
});

describe('refreshSession / useSession (subscriber notifications)', () => {
  it('no server configured: session stays null, no HTTP call is made', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    const { fn } = installFetch({});
    const { refreshSession, useSession } = await import('./auth');
    await refreshSession();
    expect(fn).not.toHaveBeenCalled();

    const { result } = renderHook(() => useSession());
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.user).toBeNull();
  });

  it('server configured but signed out (no stored token): no HTTP call is made', async () => {
    configureServer();
    const { fn } = installFetch({});
    const { refreshSession } = await import('./auth');
    await refreshSession();
    expect(fn).not.toHaveBeenCalled();
  });

  it('fetches and caches the session user when a token is stored', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({
      '/get-session': {
        status: 200,
        body: { user: { id: 'u1', email: 'ada@test.dev', name: 'Ada' }, session: {} },
      },
    });
    const { refreshSession, useSession } = await import('./auth');
    await refreshSession();

    const { result } = renderHook(() => useSession());
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.user).toEqual({ id: 'u1', email: 'ada@test.dev', name: 'Ada' });
  });

  it('treats a get-session network error as signed out', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({ '/get-session': { status: 200, reject: true } });
    const { refreshSession, useSession } = await import('./auth');
    await refreshSession();

    const { result } = renderHook(() => useSession());
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.user).toBeNull();
  });

  it('updates an already-rendered useSession subscriber reactively after sign-in', async () => {
    configureServer();
    installFetch({
      '/sign-in/email': {
        status: 200,
        body: {},
        headers: { 'set-auth-token': 'session-tok-3' },
      },
      '/get-session': {
        status: 200,
        body: { user: { id: 'u1', email: 'ada@test.dev', name: 'Ada' }, session: {} },
      },
    });
    const { useSession, signInEmail } = await import('./auth');

    const { result } = renderHook(() => useSession());
    await waitFor(() => expect(result.current.isPending).toBe(false));
    expect(result.current.user).toBeNull();

    await act(async () => {
      const res = await signInEmail('ada@test.dev', 'hunter2');
      expect(res.ok).toBe(true);
    });

    // No manual re-render call here — this is the point of the test: the
    // module-level `emit()` notifies useSyncExternalStore's subscriber and
    // the already-rendered hook picks up the new session on its own.
    await waitFor(() =>
      expect(result.current.user).toEqual({ id: 'u1', email: 'ada@test.dev', name: 'Ada' })
    );
  });
});

describe('signInEmail', () => {
  it('fails fast without a server configured, and never touches the network', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    const { fn } = installFetch({});
    const { signInEmail } = await import('./auth');
    const result = await signInEmail('ada@test.dev', 'hunter2');
    expect(result).toEqual({ ok: false, error: 'Connect to a server first.' });
    expect(fn).not.toHaveBeenCalled();
  });

  it('signs in, persists the returned session token, and refreshes the session', async () => {
    configureServer();
    const { calls } = installFetch({
      '/sign-in/email': { status: 200, body: {}, headers: { 'set-auth-token': 'session-tok-1' } },
      '/get-session': {
        status: 200,
        body: { user: { id: 'u1', email: 'ada@test.dev' }, session: {} },
      },
    });
    const { signInEmail, getStoredToken } = await import('./auth');
    const result = await signInEmail('ada@test.dev', 'hunter2');
    expect(result).toEqual({ ok: true });
    expect(getStoredToken()).toBe('session-tok-1');

    const signInCall = calls.find((c) => c.url.includes('/sign-in/email'));
    expect(signInCall?.method).toBe('POST');
    expect(signInCall?.body).toMatchObject({ email: 'ada@test.dev', password: 'hunter2' });
    expect(calls.some((c) => c.url.includes('/get-session'))).toBe(true);
  });

  it('surfaces the server-provided error message and does not refresh the session', async () => {
    configureServer();
    const { calls } = installFetch({
      '/sign-in/email': { status: 400, body: { message: 'Invalid credentials' } },
    });
    const { signInEmail, getStoredToken } = await import('./auth');
    const result = await signInEmail('ada@test.dev', 'wrong-password');
    expect(result).toEqual({ ok: false, error: 'Invalid credentials' });
    expect(getStoredToken()).toBeNull();
    expect(calls.some((c) => c.url.includes('/get-session'))).toBe(false);
  });

  it('falls back to a generic error message when the server omits one', async () => {
    configureServer();
    installFetch({ '/sign-in/email': { status: 400, body: {} } });
    const { signInEmail } = await import('./auth');
    const result = await signInEmail('ada@test.dev', 'wrong-password');
    expect(result).toEqual({ ok: false, error: 'Could not sign in.' });
  });
});

describe('signUpEmail', () => {
  it('fails fast without a server configured', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    const { fn } = installFetch({});
    const { signUpEmail } = await import('./auth');
    const result = await signUpEmail('Ada Lovelace', 'ada@test.dev', 'hunter2');
    expect(result).toEqual({ ok: false, error: 'Connect to a server first.' });
    expect(fn).not.toHaveBeenCalled();
  });

  it('signs up, sends the name/email/password, and refreshes the session', async () => {
    configureServer();
    const { calls } = installFetch({
      '/sign-up/email': { status: 200, body: {}, headers: { 'set-auth-token': 'session-tok-2' } },
      '/get-session': {
        status: 200,
        body: { user: { id: 'u2', email: 'ada@test.dev' }, session: {} },
      },
    });
    const { signUpEmail, getStoredToken } = await import('./auth');
    const result = await signUpEmail('Ada Lovelace', 'ada@test.dev', 'hunter2');
    expect(result).toEqual({ ok: true });
    expect(getStoredToken()).toBe('session-tok-2');

    const signUpCall = calls.find((c) => c.url.includes('/sign-up/email'));
    expect(signUpCall?.body).toMatchObject({
      name: 'Ada Lovelace',
      email: 'ada@test.dev',
      password: 'hunter2',
    });
  });

  it('falls back to a generic error message when the server omits one', async () => {
    configureServer();
    installFetch({ '/sign-up/email': { status: 400, body: {} } });
    const { signUpEmail } = await import('./auth');
    const result = await signUpEmail('Ada Lovelace', 'ada@test.dev', 'hunter2');
    expect(result).toEqual({ ok: false, error: 'Could not create your account.' });
  });
});

describe('verifyOtt', () => {
  it('fails fast without a server configured', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    const { fn } = installFetch({});
    const { verifyOtt } = await import('./auth');
    const result = await verifyOtt('some-code');
    expect(result).toEqual({ ok: false, error: 'Connect to a server first.' });
    expect(fn).not.toHaveBeenCalled();
  });

  it('rejects an empty/whitespace code without a network call', async () => {
    configureServer();
    const { fn } = installFetch({});
    const { verifyOtt } = await import('./auth');
    expect(await verifyOtt('')).toEqual({
      ok: false,
      error: 'Enter the code from your browser.',
    });
    expect(await verifyOtt('   ')).toEqual({
      ok: false,
      error: 'Enter the code from your browser.',
    });
    expect(fn).not.toHaveBeenCalled();
  });

  it('redeems the token, persists the session token, and refreshes the session', async () => {
    const config = configureServer();
    const { calls } = installFetch({
      '/one-time-token/verify': {
        status: 200,
        body: {},
        headers: { 'set-auth-token': 'ott-session-tok' },
      },
      '/get-session': {
        status: 200,
        body: { user: { id: 'u3', email: 'ada@test.dev' }, session: {} },
      },
    });
    const { verifyOtt, getStoredToken } = await import('./auth');
    const result = await verifyOtt('  the-code  ');
    expect(result).toEqual({ ok: true });
    expect(getStoredToken()).toBe('ott-session-tok');

    const verifyCall = calls.find((c) => c.url.includes('/one-time-token/verify'));
    expect(verifyCall?.method).toBe('POST');
    expect(verifyCall?.body).toEqual({ token: 'the-code' }); // trimmed
    expect(verifyCall?.url).toBe(`${config.authBaseUrl}/one-time-token/verify`);
  });

  it('errors when the server accepts the code but returns no session token', async () => {
    configureServer();
    const { calls } = installFetch({
      '/one-time-token/verify': { status: 200, body: {} },
    });
    const { verifyOtt, getStoredToken } = await import('./auth');
    const result = await verifyOtt('the-code');
    expect(result).toEqual({ ok: false, error: 'Sign-in did not return a session token.' });
    expect(getStoredToken()).toBeNull();
    expect(calls.some((c) => c.url.includes('/get-session'))).toBe(false);
  });

  it('surfaces the server error message on a failed redemption', async () => {
    configureServer();
    installFetch({
      '/one-time-token/verify': {
        status: 400,
        body: { message: 'That code is invalid or has expired.' },
      },
    });
    const { verifyOtt } = await import('./auth');
    const result = await verifyOtt('bad-code');
    expect(result).toEqual({ ok: false, error: 'That code is invalid or has expired.' });
  });

  it('falls back to a generic error when the failure body has no message', async () => {
    configureServer();
    installFetch({ '/one-time-token/verify': { status: 400 } });
    const { verifyOtt } = await import('./auth');
    const result = await verifyOtt('bad-code');
    expect(result).toEqual({ ok: false, error: 'That code is invalid or has expired.' });
  });

  it('reports a network error as "could not reach the server"', async () => {
    configureServer();
    installFetch({ '/one-time-token/verify': { status: 200, reject: true } });
    const { verifyOtt } = await import('./auth');
    const result = await verifyOtt('the-code');
    expect(result).toEqual({ ok: false, error: 'Could not reach the server.' });
  });
});

describe('getAccessToken', () => {
  it('returns null without a configured server', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const { fn } = installFetch({});
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBeNull();
    expect(fn).not.toHaveBeenCalled();
  });

  it('returns null when signed out (no stored token), even with a server configured', async () => {
    configureServer();
    const { fn } = installFetch({});
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBeNull();
    expect(fn).not.toHaveBeenCalled();
  });

  it('mints and returns a fresh JWT, sending the stored token as a Bearer credential', async () => {
    const config = configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const { calls } = installFetch({ '/token': { status: 200, body: { token: 'jwt-xyz' } } });
    const { getAccessToken } = await import('./auth');
    const token = await getAccessToken();
    expect(token).toBe('jwt-xyz');
    const call = calls[0]!;
    expect(call.url).toBe(`${config.authBaseUrl}/token`);
    expect(call.method).toBe('GET');
  });

  it('returns null when the response omits a token field', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({ '/token': { status: 200, body: {} } });
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBeNull();
  });

  it('returns null on a non-ok response', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({ '/token': { status: 401, body: { message: 'expired' } } });
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBeNull();
  });

  it('returns null on a network error', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({ '/token': { status: 200, reject: true } });
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBeNull();
  });
});

describe('signOut', () => {
  it('clears the stored token and leaves the session signed out', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const { calls } = installFetch({ '/sign-out': { status: 200, body: {} } });
    const { signOut, getStoredToken } = await import('./auth');
    await signOut();
    expect(getStoredToken()).toBeNull();
    // clearStoredToken() runs before refreshSession(), so by the time
    // refreshSession looks for a token there isn't one — it short-circuits
    // without hitting /get-session, leaving only the /sign-out call.
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toContain('/sign-out');
  });

  it('still clears local state when the sign-out request itself fails', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    installFetch({ '/sign-out': { status: 200, reject: true } });
    const { signOut, getStoredToken } = await import('./auth');
    await expect(signOut()).resolves.toBeUndefined();
    expect(getStoredToken()).toBeNull();
  });

  it('is a safe no-op when no server is configured', async () => {
    getActiveServerConfigMock.mockReturnValue(null);
    localStorage.setItem('calendium.bearerToken', 'stray-token');
    const { fn } = installFetch({});
    const { signOut, getStoredToken } = await import('./auth');
    await expect(signOut()).resolves.toBeUndefined();
    expect(getStoredToken()).toBeNull();
    expect(fn).not.toHaveBeenCalled();
  });
});

function fakeJwt(expSeconds: number): string {
  const payload = btoa(JSON.stringify({ sub: 'u1', exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

describe('signUpEmail — verification required (piece 2)', () => {
  it('reports verificationRequired when the server returns token === null and does not fetch a session', async () => {
    configureServer();
    const { calls } = installFetch({
      '/sign-up/email': { status: 200, body: { token: null, user: { id: 'u2', email: 'ada@test.dev' } } },
    });
    const { signUpEmail, getStoredToken } = await import('./auth');
    const result = await signUpEmail('Ada Lovelace', 'ada@test.dev', 'correct-horse-battery');
    expect(result).toEqual({ ok: true, verificationRequired: true });
    expect(getStoredToken()).toBeNull();
    expect(calls.some((c) => c.url.includes('/get-session'))).toBe(false);
    const signUpCall = calls.find((c) => c.url.includes('/sign-up/email'));
    expect(signUpCall?.body).toMatchObject({ callbackURL: '/verify-email' });
  });
});

describe('signInEmail — verification and rate-limit copy (piece 2)', () => {
  it('maps 403 EMAIL_NOT_VERIFIED to the verify-first message', async () => {
    configureServer();
    installFetch({ '/sign-in/email': { status: 403, body: { code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' } } });
    const { signInEmail } = await import('./auth');
    expect(await signInEmail('ada@test.dev', 'correct-horse-battery')).toEqual({
      ok: false,
      error: 'Verify your email first — we sent a new link.',
    });
  });

  it('maps 429 to the retry-after copy', async () => {
    configureServer();
    installFetch({ '/sign-in/email': { status: 429, body: { message: 'Too many requests. Please try again later.' }, headers: { 'X-Retry-After': '42' } } });
    const { signInEmail } = await import('./auth');
    expect(await signInEmail('ada@test.dev', 'correct-horse-battery')).toEqual({ ok: false, error: 'Too many attempts, try again in 42 s' });
  });
});

describe('getAccessToken cache (piece 2)', () => {
  it('reuses a fresh JWT across calls and returns null without re-minting after signOut', async () => {
    const config = configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const token = fakeJwt(Math.floor(Date.now() / 1000) + 900);
    const { calls } = installFetch({
      '/token': { status: 200, body: { token } },
      '/sign-out': { status: 200, body: {} },
    });
    const { getAccessToken, signOut } = await import('./auth');
    expect(await getAccessToken()).toBe(token);
    expect(await getAccessToken()).toBe(token);
    expect(calls.filter((c) => c.url === `${config.authBaseUrl}/token`)).toHaveLength(1);

    await signOut();
    // Signed out: no stored token, so the cache is empty AND minting short-circuits.
    expect(await getAccessToken()).toBeNull();
    expect(calls.filter((c) => c.url === `${config.authBaseUrl}/token`)).toHaveLength(1);
  });
});

describe('getAccessToken cache — invalidation on sign-in and server switch (fix wave)', () => {
  const sessionUser = { status: 200, body: { user: { id: 'u1', email: 'ada@test.dev' }, session: {} } };

  function tokenRoutes() {
    const exp = Math.floor(Date.now() / 1000) + 900;
    const first = fakeJwt(exp);
    const second = `${fakeJwt(exp)}2`;
    return { first, second, route: [{ status: 200, body: { token: first } }, { status: 200, body: { token: second } }] };
  }

  it('signInEmail drops the JWT minted for the previous session', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'old-session');
    const t = tokenRoutes();
    const { calls } = installFetch({
      '/sign-in/email': { status: 200, body: {}, headers: { 'set-auth-token': 'new-session' } },
      '/get-session': sessionUser,
      '/token': t.route,
    });
    const { getAccessToken, signInEmail } = await import('./auth');
    expect(await getAccessToken()).toBe(t.first);
    expect(await signInEmail('ada@test.dev', 'hunter2')).toEqual({ ok: true });
    expect(await getAccessToken()).toBe(t.second);
    expect(calls.filter((c) => c.url.endsWith('/token'))).toHaveLength(2);
  });

  it('a failed signInEmail keeps the cached JWT', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'old-session');
    const t = tokenRoutes();
    const { calls } = installFetch({
      '/sign-in/email': { status: 401, body: { message: 'Invalid email or password' } },
      '/token': t.route,
    });
    const { getAccessToken, signInEmail } = await import('./auth');
    expect(await getAccessToken()).toBe(t.first);
    expect((await signInEmail('ada@test.dev', 'wrong')).ok).toBe(false);
    expect(await getAccessToken()).toBe(t.first);
    expect(calls.filter((c) => c.url.endsWith('/token'))).toHaveLength(1);
  });

  it('signUpEmail that signs in drops the cached JWT', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'old-session');
    const t = tokenRoutes();
    installFetch({
      '/sign-up/email': { status: 200, body: { token: 'new-session', user: { id: 'u2' } }, headers: { 'set-auth-token': 'new-session' } },
      '/get-session': sessionUser,
      '/token': t.route,
    });
    const { getAccessToken, signUpEmail } = await import('./auth');
    expect(await getAccessToken()).toBe(t.first);
    expect(await signUpEmail('Ada', 'ada@test.dev', 'correct-horse-battery')).toEqual({ ok: true });
    expect(await getAccessToken()).toBe(t.second);
  });

  it('verifyOtt drops the JWT minted for the previous session', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'old-session');
    const t = tokenRoutes();
    installFetch({
      '/one-time-token/verify': { status: 200, body: {}, headers: { 'set-auth-token': 'ott-session' } },
      '/get-session': sessionUser,
      '/token': t.route,
    });
    const { getAccessToken, verifyOtt } = await import('./auth');
    expect(await getAccessToken()).toBe(t.first);
    expect(await verifyOtt('the-code')).toEqual({ ok: true });
    expect(await getAccessToken()).toBe(t.second);
  });

  it('switching servers drops the JWT minted for the previous server', async () => {
    configureServer();
    localStorage.setItem('calendium.bearerToken', 'stored-token');
    const t = tokenRoutes();
    const { calls } = installFetch({ '/token': t.route });
    const { getAccessToken } = await import('./auth');
    expect(await getAccessToken()).toBe(t.first);
    expect(await getAccessToken()).toBe(t.first);
    const next = configureServer({ serverUrl: 'https://other.test' });
    expect(await getAccessToken()).toBe(t.second);
    expect(calls.at(-1)?.url).toBe(`${next.authBaseUrl}/token`);
  });
});
