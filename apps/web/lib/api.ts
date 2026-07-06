import { ApiClient } from '@calendium/shared';

import { env } from '@/lib/env';
import { createClient } from '@/lib/supabase/client';

let client: ApiClient | undefined;

/**
 * Browser-side Calendium API client (see packages/shared/src/client.ts).
 * Sends the current Supabase access token as the Bearer credential; lazily
 * constructed singleton, safe to call from components, hooks, and queries.
 */
export function getApiClient(): ApiClient {
  client ??= new ApiClient({
    baseUrl: env.apiUrl,
    getAccessToken: async () => {
      const { data } = await createClient().auth.getSession();
      return data.session?.access_token ?? null;
    },
  });
  return client;
}
