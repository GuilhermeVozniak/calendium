import { createAuthClient } from 'better-auth/react';
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
}

export async function signInEmail(email: string, password: string): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const { error } = await c.signIn.email({ email, password });
  if (error) return { ok: false, error: error.message ?? 'Could not sign in.' };
  await refreshSession();
  return { ok: true };
}

export async function signUpEmail(
  name: string,
  email: string,
  password: string
): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const { error } = await c.signUp.email({ name, email, password });
  if (error) return { ok: false, error: error.message ?? 'Could not create your account.' };
  await refreshSession();
  return { ok: true };
}

/**
 * Kick off an OAuth flow with Google or Apple. In the WebView this navigates to
 * the provider and returns to the server's Better Auth callback. Full desktop
 * deep-link round-trip back into the app needs a host wails:// handler (see the
 * desktop Go host) — email + password works entirely in-process today.
 */
export async function signInSocial(provider: 'google' | 'apple'): Promise<AuthResult> {
  const c = getAuthClient();
  if (!c) return { ok: false, error: 'Connect to a server first.' };
  const cfg = getActiveServerConfig();
  const callbackURL = cfg?.authBaseUrl ? new URL(cfg.authBaseUrl).origin : undefined;
  const { error } = await c.signIn.social({ provider, callbackURL });
  if (error) return { ok: false, error: error.message ?? 'Could not start sign in.' };
  return { ok: true };
}

export async function signOut(): Promise<void> {
  const c = getAuthClient();
  try {
    await c?.signOut();
  } catch {
    // Ignore — we clear local state regardless.
  }
  clearStoredToken();
  await refreshSession();
}

/**
 * Mint a short-lived (default 15m) EdDSA JWT for the current session and return
 * it as the Bearer credential for the Go API. Hits the jwt() plugin mint
 * endpoint at `${authBaseUrl}/token` with the stored session token; returns
 * null when signed out or no server is configured.
 *
 * Short-lived by design — do NOT cache it; ApiClient calls this per request.
 */
export async function getAccessToken(): Promise<string | null> {
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
