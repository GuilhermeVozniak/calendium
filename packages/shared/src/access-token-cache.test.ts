import { describe, expect, it, vi } from 'vitest';

import { ACCESS_TOKEN_REFRESH_MARGIN_MS, createAccessTokenCache, decodeJwtExp } from './access-token-cache';

/** A syntactically valid, unsigned JWT with the given exp (seconds). */
function jwt(expSeconds: number, extra: Record<string, unknown> = {}): string {
  const payload = Buffer.from(JSON.stringify({ sub: 'u1', exp: expSeconds, ...extra })).toString('base64url');
  return `eyJhbGciOiJFZERTQSJ9.${payload}.sig`;
}

const T0 = 1_800_000_000_000; // fixed "now" in ms

describe('decodeJwtExp', () => {
  it('returns exp in milliseconds for a well-formed token', () => {
    expect(decodeJwtExp(jwt(1_800_000_900))).toBe(1_800_000_900_000);
  });

  it('decodes payloads with non-ASCII claims', () => {
    expect(decodeJwtExp(jwt(1_800_000_900, { name: 'José — café' }))).toBe(1_800_000_900_000);
  });

  it('returns null for opaque strings, missing or non-numeric exp, and bad base64', () => {
    expect(decodeJwtExp('test-access-token')).toBeNull();
    expect(decodeJwtExp('a.b')).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('{"sub":"u1"}').toString('base64url')}.s`)).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('{"exp":"soon"}').toString('base64url')}.s`)).toBeNull();
    expect(decodeJwtExp('h.%%%.s')).toBeNull();
    expect(decodeJwtExp(`h.${Buffer.from('not json').toString('base64url')}.s`)).toBeNull();
  });
});

describe('createAccessTokenCache', () => {
  it('reuses a token until 60 s before exp, then mints again', async () => {
    let now = T0;
    const first = jwt((T0 + 900_000) / 1000);
    const second = jwt((T0 + 1_800_000) / 1000);
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const cache = createAccessTokenCache(mint, () => now);

    expect(await cache.get()).toBe(first);
    now = T0 + 900_000 - ACCESS_TOKEN_REFRESH_MARGIN_MS - 1;
    expect(await cache.get()).toBe(first);
    expect(mint).toHaveBeenCalledTimes(1);

    now = T0 + 900_000 - ACCESS_TOKEN_REFRESH_MARGIN_MS;
    expect(await cache.get()).toBe(second);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('shares one in-flight mint between concurrent callers', async () => {
    let resolve!: (t: string | null) => void;
    const mint = vi.fn(() => new Promise<string | null>((r) => { resolve = r; }));
    const cache = createAccessTokenCache(mint, () => T0);
    const a = cache.get();
    const b = cache.get();
    expect(mint).toHaveBeenCalledTimes(1);
    resolve(jwt((T0 + 900_000) / 1000));
    expect(await a).toBe(await b);
    expect(await cache.get()).toBe(await a);
    expect(mint).toHaveBeenCalledTimes(1);
  });

  it('never caches null, opaque tokens, or tokens without a decodable exp', async () => {
    const mint = vi.fn<() => Promise<string | null>>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce('test-access-token')
      .mockResolvedValueOnce(`h.${Buffer.from('{"sub":"u1"}').toString('base64url')}.s`)
      .mockResolvedValueOnce(jwt((T0 + 900_000) / 1000));
    const cache = createAccessTokenCache(mint, () => T0);
    expect(await cache.get()).toBeNull();
    expect(await cache.get()).toBe('test-access-token');
    expect(await cache.get()).toMatch(/^h\./);
    expect(mint).toHaveBeenCalledTimes(3);
    await cache.get();
    await cache.get();
    expect(mint).toHaveBeenCalledTimes(4);
  });

  it('returns but does not cache a token that is already inside the refresh margin', async () => {
    const stale = jwt((T0 + ACCESS_TOKEN_REFRESH_MARGIN_MS - 1) / 1000);
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValue(stale);
    const cache = createAccessTokenCache(mint, () => T0);
    expect(await cache.get()).toBe(stale);
    expect(await cache.get()).toBe(stale);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('invalidate() forces the next get() to mint', async () => {
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValue(jwt((T0 + 900_000) / 1000));
    const cache = createAccessTokenCache(mint, () => T0);
    await cache.get();
    cache.invalidate();
    await cache.get();
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('propagates a mint rejection to every waiter and recovers afterwards', async () => {
    const mint = vi.fn<() => Promise<string | null>>().mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(null);
    const cache = createAccessTokenCache(mint, () => T0);
    const a = cache.get();
    const b = cache.get();
    await expect(a).rejects.toThrow('boom');
    await expect(b).rejects.toThrow('boom');
    expect(await cache.get()).toBeNull();
  });
});
