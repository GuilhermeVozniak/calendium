import { supabase } from '@/lib/supabase';
import { ApiClient } from '@calendium/shared';

/**
 * Typed Calendium API client (see docs/architecture.md for the REST contract).
 * Authenticates with the current Supabase session JWT. Screens fall back to
 * mock data (lib/mock.ts) when the API is unreachable.
 */
const baseUrl = process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080';

if (!process.env.EXPO_PUBLIC_API_URL) {
  console.warn('[calendium] EXPO_PUBLIC_API_URL is not set — defaulting to http://localhost:8080');
}

export const api = new ApiClient({
  baseUrl,
  getAccessToken: async () => {
    const { data } = await supabase.auth.getSession();
    return data.session?.access_token ?? null;
  },
});
