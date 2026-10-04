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

  it('a mint that started before invalidate() is never cached nor handed to later callers', async () => {
    const stale = jwt((T0 + 900_000) / 1000, { sub: 'old-user' });
    const freshToken = jwt((T0 + 900_000) / 1000, { sub: 'new-user' });
    const resolvers: Array<(t: string | null) => void> = [];
    const mint = vi.fn(() => new Promise<string | null>((r) => resolvers.push(r)));
    const cache = createAccessTokenCache(mint, () => T0);

    const before = cache.get(); // generation 0 mint in flight
    cache.invalidate(); // sign-out / user change
    const after = cache.get(); // generation 1 must NOT join the old mint
    expect(mint).toHaveBeenCalledTimes(2);

    resolvers[0]?.(stale); // the old mint lands late
    expect(await before).toBe(stale); // its own (old-generation) caller still gets it
    resolvers[1]?.(freshToken);
    expect(await after).toBe(freshToken);
    expect(await cache.get()).toBe(freshToken); // the stale token was never cached
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('an old-generation mint finishing last neither caches nor clears the newer in-flight mint', async () => {
    const stale = jwt((T0 + 900_000) / 1000, { sub: 'old-user' });
    const freshToken = jwt((T0 + 900_000) / 1000, { sub: 'new-user' });
    const resolvers: Array<(t: string | null) => void> = [];
    const mint = vi.fn(() => new Promise<string | null>((r) => resolvers.push(r)));
    const cache = createAccessTokenCache(mint, () => T0);

    const before = cache.get();
    cache.invalidate();
    const after = cache.get();
    resolvers[1]?.(freshToken);
    expect(await after).toBe(freshToken);
    resolvers[0]?.(stale);
    expect(await before).toBe(stale);
    expect(await cache.get()).toBe(freshToken);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('invalidate(failedToken) drops only that token — a sibling 401 cannot discard a fresher one', async () => {
    const t1 = jwt((T0 + 900_000) / 1000, { n: 1 });
    const t2 = jwt((T0 + 900_000) / 1000, { n: 2 });
    const mint = vi.fn<() => Promise<string | null>>().mockResolvedValueOnce(t1).mockResolvedValueOnce(t2);
    const cache = createAccessTokenCache(mint, () => T0);

    expect(await cache.get()).toBe(t1);
    // Request A's 401 on t1: drop it and re-mint t2.
    cache.invalidate(t1);
    expect(await cache.get()).toBe(t2);
    // Request B's 401 on the SAME stale t1 arrives late: t2 must survive.
    cache.invalidate(t1);
    expect(await cache.get()).toBe(t2);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('concurrent 401s on one token while the re-mint is in flight share that one re-mint', async () => {
    const t1 = jwt((T0 + 900_000) / 1000, { n: 1 });
    const t2 = jwt((T0 + 900_000) / 1000, { n: 2 });
    let resolveRemint!: (t: string | null) => void;
    const mint = vi
      .fn<() => Promise<string | null>>()
      .mockResolvedValueOnce(t1)
      .mockImplementationOnce(() => new Promise((r) => { resolveRemint = r; }));
    const cache = createAccessTokenCache(mint, () => T0);

    expect(await cache.get()).toBe(t1);
    cache.invalidate(t1); // A
    const a = cache.get();
    cache.invalidate(t1); // B: t1 is already gone — must not orphan A's re-mint
    const b = cache.get();
    resolveRemint(t2);
    expect(await a).toBe(t2);
    expect(await b).toBe(t2);
    expect(await cache.get()).toBe(t2);
    expect(mint).toHaveBeenCalledTimes(2);
  });

  it('recovers when mint throws synchronously', async () => {
    const ok = jwt((T0 + 900_000) / 1000);
    const mint = vi
      .fn<() => Promise<string | null>>()
      .mockImplementationOnce(() => {
        throw new Error('sync boom');
      })
      .mockResolvedValueOnce(ok);
    const cache = createAccessTokenCache(mint, () => T0);
    await expect(cache.get()).rejects.toThrow('sync boom');
    expect(await cache.get()).toBe(ok);
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
