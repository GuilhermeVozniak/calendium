import { ApiClient } from '@calendium/shared';

import { getActingAs } from '@/lib/act-as';
import { getAccessToken } from '@/lib/auth-client';
import { env } from '@/lib/env';

let client: ApiClient | undefined;
let actingClient: { principalId: string; client: ApiClient } | undefined;

/**
 * Browser-side Calendium API client (see packages/shared/src/client.ts).
 * Mints a fresh Better Auth JWT (GET /api/auth/token) per request and sends it
 * as the Bearer credential; lazily constructed singleton, safe to call from
 * components, hooks, and queries.
 *
 * While the user is explicitly acting for a principal (lib/act-as.ts), a
 * derived actAs client is returned instead: delegable mail/calendar requests
 * carry the X-Calendium-Act-As header, everything else stays untouched. The
 * header is NEVER sent outside an explicit acting selection.
 */
export function getApiClient(): ApiClient {
  client ??= new ApiClient({
    baseUrl: env.apiUrl,
    getAccessToken,
  });
  const principalId = getActingAs();
  if (!principalId) return client;
  if (actingClient?.principalId !== principalId) {
    actingClient = { principalId, client: client.actAs(principalId) };
  }
  return actingClient.client;
}
