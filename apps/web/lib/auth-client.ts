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

let apiSuspended = false;
const suspendListeners = new Set<() => void>();

/**
 * Pauses every Go API request from this tab (lib/api.ts refuses to fetch, no
 * JWT is minted, open collab streams drop and the outbox stops replaying)
 * until resumeApi(). Account deletion suspends BEFORE calling deleteUser: a
 * request carrying the deleting user's JWT is what could race the purge.
 */
export function suspendApi(): void {
  apiSuspended = true;
  accessTokens.invalidate();
  for (const listener of [...suspendListeners]) listener();
}

/** Lifts suspendApi() (a failed delete). */
export function resumeApi(): void {
  apiSuspended = false;
}

export function isApiSuspended(): boolean {
  return apiSuspended;
}

/** Called on every suspendApi() (collab streams abort their connection). Returns the unsubscribe function. */
export function onApiSuspended(listener: () => void): () => void {
  suspendListeners.add(listener);
  return () => {
    suspendListeners.delete(listener);
  };
}

/** The Bearer credential for the Go API (cached JWT, minted on demand); null while the API is suspended. */
export function getAccessToken(): Promise<string | null> {
  if (apiSuspended) return Promise.resolve(null);
  return accessTokens.get();
}

/** Drops the cached JWT (sign-out, server switch). The next call re-mints. */
export function invalidateAccessToken(): void {
  accessTokens.invalidate();
}

/** The session user the cached JWT belongs to; `undefined` until the first session is observed. */
let tokenOwner: string | null | undefined;

/**
 * Ties the JWT cache to the signed-in user. Called with the session user id
 * (null when signed out) by the app shell: any change — session expiry,
 * remote revocation, a different account signing in on the same tab —
 * drops the cached token and discards an in-flight mint, so the next account
 * can never call the API with the previous one's JWT. The first observation
 * only records the owner (nothing can be cached for anyone else yet).
 */
export function syncAccessTokenOwner(userId: string | null): void {
  if (userId === tokenOwner) return;
  const known = tokenOwner !== undefined;
  tokenOwner = userId;
  if (known) accessTokens.invalidate();
}
