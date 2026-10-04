import { createAuthClient } from 'better-auth/react';
import { type AccessTokenCache, createAccessTokenCache } from '@calendium/shared';
import { useEffect, useState, useSyncExternalStore } from 'react';

import { getActiveServerConfig } from './server-config';

/**
 * Better Auth is the identity provider on every platform (docs/architecture.md).
 *
 * The desktop WebView is a SEPARATE ORIGIN from the server it talks to, so we
 * cannot rely on cookies. Instead we use the bearer/JWT approach:
 *   1. Sign in against the server's Better Auth base URL (discovered from
 *      /v1/instance and stored in lib/server-config as `authBaseUrl`).
 *   2. Capture the `set-auth-token` response header (a session token) and
 *      persist it in localStorage; send it as `Authorization: Bearer` on every
 *      subsequent Better Auth call (the server's bearer() plugin accepts it).
 *   3. For the Go API, mint a short-lived EdDSA JWT via `${authBaseUrl}/token`
 *      (getAccessToken below), which ApiClient sends as its Bearer credential.
 */

const TOKEN_KEY = 'calendium.bearerToken';

export function getStoredToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

function setStoredToken(token: string): void {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    // Ignore write failures (private mode, quota).
  }
}

export function clearStoredToken(): void {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    // Ignore.
  }
}

type AuthClient = ReturnType<typeof createAuthClient>;

// The client is rebuilt lazily whenever the configured server (and thus the
// Better Auth base URL) changes — rebuilt per active server config.
let client: AuthClient | null = null;
let clientBaseUrl = '';

/** Better Auth client for the currently-configured server, or null. */
export function getAuthClient(): AuthClient | null {
  const baseURL = getActiveServerConfig()?.authBaseUrl ?? '';
  if (!baseURL) {
    client = null;
    clientBaseUrl = '';
    return null;
  }
  if (baseURL !== clientBaseUrl) {
    clientBaseUrl = baseURL;
    client = createAuthClient({
      baseURL,
      fetchOptions: {
        // Send the persisted session token on every Better Auth request…
        auth: {
          type: 'Bearer',
          token: () => getStoredToken() ?? '',
        },
        // …and capture a freshly-issued one whenever the server returns it.
        onSuccess: (ctx) => {
          const token = ctx.response.headers.get('set-auth-token');
          if (token) setStoredToken(token);
        },
      },
    });
  }
  return client;
}

export interface AuthUser {
  id: string;
  email: string;
  name?: string | null;
  image?: string | null;
}

// Tiny module-level session store so sign-in / sign-out re-render the gate.
let cachedSession: AuthUser | null = null;
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function emit(): void {
  for (const listener of listeners) listener();
}

/** Re-checks the current session against the server and notifies subscribers. */
export async function refreshSession(): Promise<void> {
  const c = getAuthClient();
  if (!c || !getStoredToken()) {
    cachedSession = null;
    emit();
    return;
  }
  try {
    const { data } = await c.getSession();
    cachedSession = data?.user ? (data.user as AuthUser) : null;
  } catch {
    cachedSession = null;
  }
  emit();
}

/** Reactive session hook for the auth gate. */
export function useSession(): { user: AuthUser | null; isPending: boolean } {
  const user = useSyncExternalStore(
    subscribe,
    () => cachedSession,
    () => cachedSession
  );
  const [isPending, setIsPending] = useState(true);
  useEffect(() => {
    let active = true;
    void refreshSession().finally(() => {
      if (active) setIsPending(false);
    });
    return () => {
      active = false;
    };
  }, []);
  return { user, isPending };
}

export interface AuthResult {
  ok: boolean;
  error?: string;
  /** Sign-up succeeded but the server requires email verification first (no session yet). */
  verificationRequired?: boolean;
}

/** Copy shared with the web app for Better Auth client errors. */
function describeAuthError(error: { status?: number; code?: string; message?: string }, fallback: string, retryAfter: string | null): string {
  if (error.status === 429) {
    const n = Number(retryAfter);
    return `Too many attempts, try again in ${Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60} s`;
  }
  if (error.code === 'EMAIL_NOT_VERIFIED') return 'Verify your email first — we sent a new link.';
  return error.message ?? fallback;
}

/** Captures X-Retry-After from a Better Auth client call's onError hook. */
function retryAfterCapture() {
  let value: string | null = null;
  return {
    fetchOptions: { onError: (ctx: { response: Response }) => { value = ctx.response.headers.get('x-retry-after'); } },
    get value() { return value; },
  };
}

export async function signInEmail(email: string, password: string): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const retry = retryAfterCapture();
  const { error } = await c.signIn.email({ email, password }, retry.fetchOptions);
  if (error) return { ok: false, error: describeAuthError(error, 'Could not sign in.', retry.value) };
  accessTokens.invalidate();
  await refreshSession();
  return { ok: true };
}

export async function signUpEmail(name: string, email: string, password: string): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const retry = retryAfterCapture();
  // callbackURL is relative: Better Auth resolves it against BETTER_AUTH_URL,
  // so the emailed link lands on the web /verify-email page.
  const { data, error } = await c.signUp.email({ name, email, password, callbackURL: '/verify-email' }, retry.fetchOptions);
  if (error) return { ok: false, error: describeAuthError(error, 'Could not create your account.', retry.value) };
  // With SMTP configured the server never signs a new account in (and answers
  // the same for an existing address): token === null means "check your inbox".
  if (data && data.token === null) return { ok: true, verificationRequired: true };
  accessTokens.invalidate();
  await refreshSession();
  return { ok: true };
}

/**
 * Completes desktop social sign-in from a one-time token (contract item 11).
 *
 * Google blocks OAuth inside embedded WebViews, so the social flow runs in the
 * system browser (SignInView opens `${webOrigin}/signin?next=/desktop-callback`).
 * The web `/desktop-callback` page mints a one-time token and hands it back via
 * the calendium://auth/callback?ott=... deep link (or a pasted code). We redeem
 * it at Better Auth's one-time-token/verify endpoint; the bearer() plugin
 * returns the new session token in `set-auth-token`, which we persist exactly
 * like email/password sign-in does.
 */
export async function verifyOtt(token: string): Promise<AuthResult> {
  const cfg = getActiveServerConfig();
  if (!cfg?.authBaseUrl) return { ok: false, error: 'Connect to a server first.' };
  const trimmed = token.trim();
  if (!trimmed) return { ok: false, error: 'Enter the code from your browser.' };
  try {
    const res = await fetch(`${cfg.authBaseUrl}/one-time-token/verify`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({ token: trimmed }),
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => null)) as { message?: string } | null;
      return { ok: false, error: data?.message ?? 'That code is invalid or has expired.' };
    }
    const sessionToken = res.headers.get('set-auth-token');
    if (!sessionToken) return { ok: false, error: 'Sign-in did not return a session token.' };
    setStoredToken(sessionToken);
    accessTokens.invalidate();
    await refreshSession();
    return { ok: true };
  } catch {
    return { ok: false, error: 'Could not reach the server.' };
  }
}

export async function signOut(): Promise<void> {
  const c = getAuthClient();
  try {
    await c?.signOut();
  } catch {
    // Ignore — we clear local state regardless.
  }
  clearStoredToken();
  accessTokens.invalidate();
  await refreshSession();
}

/**
 * Mints a short-lived (default 15m) EdDSA JWT via `${authBaseUrl}/token` with
 * the stored session token; null when signed out, unconfigured, rate limited
 * or unreachable (null is never cached).
 */
async function mintAccessToken(): Promise<string | null> {
  const cfg = getActiveServerConfig();
  const token = getStoredToken();
  if (!cfg?.authBaseUrl || !token) return null;
  try {
    const res = await fetch(`${cfg.authBaseUrl}/token`, {
      method: 'GET',
      headers: { accept: 'application/json', authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    const data = (await res.json()) as { token?: string };
    return data.token ?? null;
  } catch {
    return null;
  }
}

const jwtCache = createAccessTokenCache(mintAccessToken);

/** Identity of the server a JWT is minted for; '' when none is configured. */
function activeServerKey(): string {
  const cfg = getActiveServerConfig();
  return cfg ? `${cfg.serverUrl}|${cfg.authBaseUrl}` : '';
}

let jwtServerKey = activeServerKey();

/**
 * JWT cache shared with lib/api.ts (piece 2): reused until 60 s before `exp`.
 * It is invalidated after every successful sign-in and on sign-out (and, on
 * a 401, only when the failing token is still the cached one), so a
 * token minted for one session is never served to the next, and whenever the
 * active server changes (checked on every get(), so every path that switches
 * servers — Connect, demo, clear — is covered without lib/server-config
 * importing this module).
 */
export const accessTokens: AccessTokenCache = {
  get() {
    const key = activeServerKey();
    if (key !== jwtServerKey) {
      jwtServerKey = key;
      jwtCache.invalidate();
    }
    return jwtCache.get();
  },
  // Forward the 401's token: the shared cache drops it only while it is still
  // the cached one, so concurrent 401s on one stale JWT cost a single re-mint.
  invalidate(failedToken) {
    jwtCache.invalidate(failedToken);
  },
};

/** The Bearer credential for the Go API (cached JWT, minted on demand). */
export function getAccessToken(): Promise<string | null> {
  return accessTokens.get();
}
