import { getBetterAuthToken } from '@/lib/auth-client';
import { ApiClient } from '@calendium/shared';

/**
 * Typed Calendium API client (see docs/architecture.md for the REST contract).
 * The base URL comes from runtime server discovery (lib/server-config) and is
 * updated in place via `configureApi`, so every screen that imported `api`
 * keeps talking to the currently-connected server. Authenticates with a
 * short-lived Better Auth JWT (cached by ApiClient until 60 s before expiry); screens fall back to mock
 * data (lib/mock.ts) when the API is unreachable.
 */

const DEFAULT_BASE = (process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080').replace(
  /\/+$/,
  ''
);

const paymentRequiredListeners = new Set<() => void>();

/**
 * Subscribes to every 402 Payment Required the shared client receives (any
 * endpoint) so the tabs billing gate can re-check the subscription mid-session
 * instead of waiting for the next foreground. Returns the unsubscribe function.
 */
export function onPaymentRequired(listener: () => void): () => void {
  paymentRequiredListeners.add(listener);
  return () => {
    paymentRequiredListeners.delete(listener);
  };
}

let suspended = false;

/**
 * Pauses every Go API request (and JWT mint) until resumeApi(). Account
 * deletion suspends the client before calling Better Auth's deleteUser: a
 * background request carrying a JWT minted for the user being deleted would
 * pass requireAuth → EnsureUser and re-create the purged `users` row.
 * Suspending also drops the cached JWT.
 */
export function suspendApi(): void {
  suspended = true;
  api.invalidateAccessToken();
}

/** Lifts suspendApi() (a failed delete, or the next sign-in). */
export function resumeApi(): void {
  suspended = false;
}

/** fetch that reports 402s to the listeners above; resolves the global at call time. */
const notifyingFetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  if (suspended) throw new Error('Calendium API requests are paused.');
  const res = await fetch(input, init);
  if (res.status === 402) {
    for (const listener of [...paymentRequiredListeners]) listener();
  }
  return res;
}) as typeof fetch;

// A single, mutable options object: ApiClient reads `opts.baseUrl` on every
// request, so updating it here re-points the shared client at runtime.
const options = {
  baseUrl: DEFAULT_BASE,
  getAccessToken: (): Promise<string | null> =>
    suspended ? Promise.resolve(null) : getBetterAuthToken(),
  fetch: notifyingFetch,
};

export const api = new ApiClient(options);

/** Point the shared API client at a server base URL discovered at runtime. */
export function configureApi(baseUrl: string): void {
  options.baseUrl = baseUrl.replace(/\/+$/, '') || DEFAULT_BASE;
  // A JWT minted for the previous server must never be sent to the next one.
  api.invalidateAccessToken();
}
