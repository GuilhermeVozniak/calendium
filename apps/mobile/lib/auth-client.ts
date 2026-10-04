import { expoClient } from '@better-auth/expo/client';
import { createAuthClient } from 'better-auth/react';
import * as SecureStore from 'expo-secure-store';

/**
 * Better Auth is the identity provider on every platform (docs/architecture.md).
 * It is hosted by the Next.js web app at `${origin}/api/auth/*`; this client
 * talks to whichever Calendium server the user connected to. The base URL is
 * the Better Auth base (`authBaseUrl`, e.g. `https://host/api/auth`) discovered
 * at runtime from `/v1/instance` (lib/server-config); the client is null until
 * a server is configured. Sessions/cookies are persisted in expo-secure-store
 * via the @better-auth/expo storage adapter.
 */

function build(baseURL: string) {
  return createAuthClient({
    baseURL,
    plugins: [
      expoClient({
        scheme: 'calendium', // matches app.json "scheme" + server trustedOrigins ("calendium://")
        storagePrefix: 'calendium',
        storage: SecureStore,
      }),
    ],
  });
}

export type AuthClient = ReturnType<typeof build>;

/** The Better Auth user shape we surface to the app (subset of the server user). */
export interface AuthUser {
  id: string;
  email: string;
  name?: string | null;
  image?: string | null;
}

let currentBaseUrl = '';
let client: AuthClient | null = null;

/** The current Better Auth client, or null until a server has been configured. */
export function getAuthClient(): AuthClient | null {
  return client;
}

/**
 * (Re)build the Better Auth client from runtime server config. Returns the new
 * client (or null when `authBaseUrl` is absent). No-op when the base URL is
 * unchanged, so we keep a single client instance and its SecureStore session.
 */
export function configureAuthClient(authBaseUrl: string): AuthClient | null {
  const next = authBaseUrl.replace(/\/+$/, '');
  if (next === currentBaseUrl) return client;
  currentBaseUrl = next;
  client = next ? build(next) : null;
  return client;
}

/**
 * Mints a short-lived Better Auth JWT (EdDSA/Ed25519) for the Go API. The
 * jwt() plugin exposes `GET ${authBaseUrl}/token`; native clients authenticate
 * it with the session cookie the expo adapter persists. Minted on demand
 * (default 15m expiry); ApiClient caches the result until 60 s before
 * expiry and drops it on sign-out or server switch. Returns null when signed out/unreachable.
 */
export async function getBetterAuthToken(): Promise<string | null> {
  if (!client || !currentBaseUrl) return null;
  try {
    // getCookie is provided by the @better-auth/expo client plugin.
    const cookie = (client as unknown as { getCookie: () => string }).getCookie();
    const res = await fetch(`${currentBaseUrl}/token`, {
      headers: {
        accept: 'application/json',
        ...(cookie ? { Cookie: cookie } : {}),
      },
    });
    if (!res.ok) return null;
    const json = (await res.json()) as { token?: string };
    return json.token ?? null;
  } catch {
    return null;
  }
}
