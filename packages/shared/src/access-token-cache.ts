/**
 * JWT reuse for API calls (piece 2). Every ApiClient request used to mint a
 * fresh token at /api/auth/token, colliding with the per-IP /token rate limit
 * (60/60 s). The cache returns the last minted token while
 * `now < exp − 60 s`, shares one in-flight mint between concurrent callers,
 * and never caches `null` or a token without a decodable `exp`. `exp` is
 * decoded, NOT verified — the Go API verifies signatures; this only decides
 * when to ask for a new one.
 */

export const ACCESS_TOKEN_REFRESH_MARGIN_MS = 60_000;

export interface AccessTokenCache {
  /** The cached token while fresh, else the result of one shared mint. */
  get(): Promise<string | null>;
  /** Drops the cached token (401 retry, sign-out, server switch). */
  invalidate(): void;
}

const B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/**
 * base64url → UTF-8 without atob/TextDecoder, so the same code runs in
 * browsers, Node and React Native. Returns null on any non-alphabet char.
 */
function decodeBase64Url(input: string): string | null {
  const bytes: number[] = [];
  let buffer = 0;
  let bits = 0;
  for (const ch of input.replace(/=+$/, '')) {
    const v = B64URL.indexOf(ch);
    if (v < 0) return null;
    buffer = (buffer << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((buffer >> bits) & 0xff);
    }
  }
  // Decode UTF-8 by hand (the payload is small; claims may carry names).
  let out = '';
  for (let i = 0; i < bytes.length; ) {
    const b0 = bytes[i] ?? 0;
    if (b0 < 0x80) {
      out += String.fromCharCode(b0);
      i += 1;
    } else if (b0 < 0xe0) {
      out += String.fromCharCode(((b0 & 0x1f) << 6) | ((bytes[i + 1] ?? 0) & 0x3f));
      i += 2;
    } else if (b0 < 0xf0) {
      out += String.fromCharCode(((b0 & 0x0f) << 12) | (((bytes[i + 1] ?? 0) & 0x3f) << 6) | ((bytes[i + 2] ?? 0) & 0x3f));
      i += 3;
    } else {
      const cp = ((b0 & 0x07) << 18) | (((bytes[i + 1] ?? 0) & 0x3f) << 12) | (((bytes[i + 2] ?? 0) & 0x3f) << 6) | ((bytes[i + 3] ?? 0) & 0x3f);
      out += String.fromCodePoint(cp);
      i += 4;
    }
  }
  return out;
}

/** `exp` of an (unverified) JWT in milliseconds since the epoch, or null when absent or undecodable. */
export function decodeJwtExp(token: string): number | null {
  const parts = token.split('.');
  if (parts.length !== 3 || !parts[1]) return null;
  const json = decodeBase64Url(parts[1]);
  if (json === null) return null;
  try {
    const payload = JSON.parse(json) as { exp?: unknown };
    return typeof payload.exp === 'number' && Number.isFinite(payload.exp) ? payload.exp * 1000 : null;
  } catch {
    return null;
  }
}

export function createAccessTokenCache(
  mint: () => Promise<string | null>,
  now: () => number = () => Date.now()
): AccessTokenCache {
  let cached: { token: string; expMs: number } | null = null;
  let inflight: Promise<string | null> | null = null;

  return {
    get() {
      if (cached && now() < cached.expMs - ACCESS_TOKEN_REFRESH_MARGIN_MS) {
        return Promise.resolve(cached.token);
      }
      cached = null;
      if (inflight) return inflight;
      inflight = (async () => {
        try {
          const token = await mint();
          if (token) {
            const expMs = decodeJwtExp(token);
            if (expMs !== null && now() < expMs - ACCESS_TOKEN_REFRESH_MARGIN_MS) {
              cached = { token, expMs };
            }
          }
          return token;
        } finally {
          inflight = null;
        }
      })();
      return inflight;
    },
    invalidate() {
      cached = null;
    },
  };
}
