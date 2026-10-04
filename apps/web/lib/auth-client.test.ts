import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { accessTokens, getAccessToken, invalidateAccessToken } from '@/lib/auth-client';

function jwt(expSeconds: number): string {
  const payload = btoa(JSON.stringify({ sub: 'u1', exp: expSeconds })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  accessTokens.invalidate();
});

afterEach(() => {
  vi.unstubAllGlobals();
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
