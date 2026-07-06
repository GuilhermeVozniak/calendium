'use client';

import { createAuthClient } from 'better-auth/react';
import { oneTimeTokenClient } from 'better-auth/client/plugins';

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
 * Mint a short-lived (default 15m) EdDSA JWT for the current session and return
 * it as the Bearer credential for the Go API. Hits the jwt() plugin's mint
 * endpoint at `/api/auth/token` same-origin with the session cookie; returns
 * null when there is no authenticated session.
 *
 * The JWT is short-lived by design — do not cache it; call this per request
 * (lib/api.ts wires it into the shared ApiClient's getAccessToken).
 */
export async function getAccessToken(): Promise<string | null> {
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
