'use client';

import { createAuthClient } from 'better-auth/react';
import { oneTimeTokenClient } from 'better-auth/client/plugins';
import { createAccessTokenCache } from '@calendium/shared';

/**
 * Browser Better Auth client. Talks to the same-origin catch-all at
 * `/api/auth/*`, so no baseURL/NEXT_PUBLIC var is needed — Better Auth infers
 * the current origin in the browser.
 *
 * The one-time-token client plugin exposes `authClient.oneTimeToken.generate()`,
 * used by /desktop-callback to hand a short-lived token to the desktop app.
 */
export const authClient = createAuthClient({
  plugins: [oneTimeTokenClient()],
});

export const { useSession, signIn, signOut, signUp } = authClient;

/**
 * Mints a short-lived (default 15m) EdDSA JWT for the current session via the
 * jwt() plugin's GET /api/auth/token (same-origin, session cookie). Returns
 * null when signed out, rate limited (429) or unreachable — null is never
 * cached, so the next call tries again.
 */
async function mintAccessToken(): Promise<string | null> {
  try {
    const res = await fetch('/api/auth/token', {
      method: 'GET',
      credentials: 'include',
      headers: { accept: 'application/json' },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string };
    return data.token ?? null;
  } catch {
    return null;
  }
}

/**
 * Process-wide JWT cache (piece 2): reuse the token until 60 s before `exp`,
 * share one in-flight mint between concurrent callers. lib/api.ts hands this
 * SAME cache to ApiClient so a 401 retry and sign-out invalidate the one copy;
 * every other caller (attachments, collab stream, web push) goes through
 * getAccessToken and benefits too.
 */
export const accessTokens = createAccessTokenCache(mintAccessToken);

/** The Bearer credential for the Go API (cached JWT, minted on demand). */
export function getAccessToken(): Promise<string | null> {
  return accessTokens.get();
}

/** Drops the cached JWT (sign-out, server switch). The next call re-mints. */
export function invalidateAccessToken(): void {
  accessTokens.invalidate();
}
