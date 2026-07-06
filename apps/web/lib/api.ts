import { ApiClient } from '@calendium/shared';

import { getAccessToken } from '@/lib/auth-client';
import { env } from '@/lib/env';

let client: ApiClient | undefined;

/**
 * Browser-side Calendium API client (see packages/shared/src/client.ts).
 * Mints a fresh Better Auth JWT (GET /api/auth/token) per request and sends it
 * as the Bearer credential; lazily constructed singleton, safe to call from
 * components, hooks, and queries.
 */
export function getApiClient(): ApiClient {
  client ??= new ApiClient({
    baseUrl: env.apiUrl,
    getAccessToken,
  });
  return client;
}
