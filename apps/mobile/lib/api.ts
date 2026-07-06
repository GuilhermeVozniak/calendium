import { getBetterAuthToken } from '@/lib/auth-client';
import { ApiClient } from '@calendium/shared';

/**
 * Typed Calendium API client (see docs/architecture.md for the REST contract).
 * The base URL comes from runtime server discovery (lib/server-config) and is
 * updated in place via `configureApi`, so every screen that imported `api`
 * keeps talking to the currently-connected server. Authenticates with a
 * short-lived Better Auth JWT (minted per request); screens fall back to mock
 * data (lib/mock.ts) when the API is unreachable.
 */

const DEFAULT_BASE = (process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080').replace(
  /\/+$/,
  ''
);

// A single, mutable options object: ApiClient reads `opts.baseUrl` on every
// request, so updating it here re-points the shared client at runtime.
const options = {
  baseUrl: DEFAULT_BASE,
  getAccessToken: (): Promise<string | null> => getBetterAuthToken(),
};

export const api = new ApiClient(options);

/** Point the shared API client at a server base URL discovered at runtime. */
export function configureApi(baseUrl: string): void {
  options.baseUrl = baseUrl.replace(/\/+$/, '') || DEFAULT_BASE;
}
