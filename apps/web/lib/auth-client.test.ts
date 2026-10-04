import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  accessTokens,
  getAccessToken,
  invalidateAccessToken,
  isApiSuspended,
  onApiSuspended,
  resumeApi,
  suspendApi,
  syncAccessTokenOwner,
} from '@/lib/auth-client';

function jwt(expSeconds: number, sub = 'u1'): string {
  const payload = btoa(JSON.stringify({ sub, exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  accessTokens.invalidate();
});

afterEach(() => {
  resumeApi();
  vi.unstubAllGlobals();
});

describe('suspendApi (account deletion)', () => {
  it('drops the cached JWT, mints nothing while suspended, notifies listeners, and resumeApi lifts it', async () => {
    const token = jwt(Math.floor(Date.now() / 1000) + 900);
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ token }), { status: 200 }));
    expect(await getAccessToken()).toBe(token);
    const listener = vi.fn();
    const off = onApiSuspended(listener);

    suspendApi();
    expect(isApiSuspended()).toBe(true);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(await getAccessToken()).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1); // no mint while suspended

    resumeApi();
    expect(isApiSuspended()).toBe(false);
    expect(await getAccessToken()).toBe(token);
    expect(fetchMock).toHaveBeenCalledTimes(2); // the suspended cache was dropped, so it re-mints
    off();
    suspendApi();
    expect(listener).toHaveBeenCalledTimes(1);
  });
});

describe('getAccessToken (web, cached)', () => {
  it('mints from /api/auth/token with the session cookie and caches a fresh JWT', async () => {
    const token = jwt(Math.floor(Date.now() / 1000) + 900);
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ token }), { status: 200 }));

    expect(await getAccessToken()).toBe(token);
    expect(await getAccessToken()).toBe(token);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/token', expect.objectContaining({ method: 'GET', credentials: 'include' }));
  });

  it('returns null (uncached) on 401/429 and network errors, then mints again next time', async () => {
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ error: 'nope' }), { status: 401 }));
    expect(await getAccessToken()).toBeNull();
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 429, headers: { 'X-Retry-After': '30' } }));
    expect(await getAccessToken()).toBeNull();
    fetchMock.mockRejectedValueOnce(new TypeError('offline'));
    expect(await getAccessToken()).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('invalidateAccessToken forces a re-mint', async () => {
    const first = jwt(Math.floor(Date.now() / 1000) + 900);
    const second = jwt(Math.floor(Date.now() / 1000) + 950);
    fetchMock
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: first }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: second }), { status: 200 }));
    expect(await getAccessToken()).toBe(first);
    invalidateAccessToken();
    expect(await getAccessToken()).toBe(second);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe('syncAccessTokenOwner (cache follows the session user)', () => {
  const exp = () => Math.floor(Date.now() / 1000) + 900;
  const mintResponse = (token: string) => new Response(JSON.stringify({ token }), { status: 200 });

  it('drops the cached JWT when the session user changes, and when the session ends', async () => {
    const a = jwt(exp(), 'user-a');
    const b = jwt(exp(), 'user-b');
    syncAccessTokenOwner('user-a');
    fetchMock.mockResolvedValueOnce(mintResponse(a)).mockResolvedValueOnce(mintResponse(b));
    expect(await getAccessToken()).toBe(a);

    syncAccessTokenOwner('user-a'); // same user re-render: keep the token
    expect(await getAccessToken()).toBe(a);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    syncAccessTokenOwner(null); // session expired / revoked elsewhere
    syncAccessTokenOwner('user-b'); // next account on the same tab
    expect(await getAccessToken()).toBe(b);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('a mint started for the previous user is never handed to the next one', async () => {
    const a = jwt(exp(), 'user-a');
    const b = jwt(exp(), 'user-b');
    syncAccessTokenOwner('user-a');
    let resolveA!: (r: Response) => void;
    fetchMock
      .mockImplementationOnce(() => new Promise<Response>((r) => { resolveA = r; }))
      .mockResolvedValueOnce(mintResponse(b));

    const pendingA = getAccessToken(); // user A's mint is in flight
    syncAccessTokenOwner('user-b'); // …when user B takes over the tab
    const forB = getAccessToken();
    resolveA(mintResponse(a));
    expect(await pendingA).toBe(a);
    expect(await forB).toBe(b);
    expect(await getAccessToken()).toBe(b);
  });
});
