import { getSupabase } from '@/lib/supabase';
import { ApiClient } from '@calendium/shared';

/**
 * Typed Calendium API client (see docs/architecture.md for the REST contract).
 * The base URL comes from runtime server discovery (lib/server-config) and is
 * updated in place via `configureApi`, so every screen that imported `api`
 * keeps talking to the currently-connected server. Authenticates with the
 * current Supabase session JWT; screens fall back to mock data (lib/mock.ts)
 * when the API is unreachable.
 */

const DEFAULT_BASE = (process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080').replace(
  /\/+$/,
  ''
);

// A single, mutable options object: ApiClient reads `opts.baseUrl` on every
// request, so updating it here re-points the shared client at runtime.
const options = {
  baseUrl: DEFAULT_BASE,
  getAccessToken: async (): Promise<string | null> => {
    const supabase = getSupabase();
    if (!supabase) return null;
    const { data } = await supabase.auth.getSession();
    return data.session?.access_token ?? null;
  },
};

export const api = new ApiClient(options);

/** Point the shared API client at a server base URL discovered at runtime. */
export function configureApi(baseUrl: string): void {
  options.baseUrl = baseUrl.replace(/\/+$/, '') || DEFAULT_BASE;
}
