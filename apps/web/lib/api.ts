import { ApiClient } from '@calendium/shared';

import { getActingAs } from '@/lib/act-as';
import { accessTokens, getAccessToken } from '@/lib/auth-client';
import { env } from '@/lib/env';

let client: ApiClient | undefined;
let actingClient: { principalId: string; client: ApiClient } | undefined;

const paymentRequiredListeners = new Set<() => void>();

/**
 * Subscribes to every 402 Payment Required the API client receives (any
 * endpoint, acting-as included) so the billing gate can re-evaluate the
 * subscription mid-session. Returns the unsubscribe function.
 */
export function onPaymentRequired(listener: () => void): () => void {
  paymentRequiredListeners.add(listener);
  return () => {
    paymentRequiredListeners.delete(listener);
  };
}

/** fetch that reports 402s to the listeners above; resolves the global at call time. */
const notifyingFetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
  const res = await fetch(input, init);
  if (res.status === 402) {
    for (const listener of [...paymentRequiredListeners]) listener();
  }
  return res;
}) as typeof fetch;

/**
 * Browser-side Calendium API client (see packages/shared/src/client.ts).
 * Reuses the cached Better Auth JWT (lib/auth-client.ts; GET /api/auth/token until 60 s before expiry) and sends it
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
    // One JWT cache for the whole tab: ApiClient's 401 retry and sign-out
    // both invalidate the same copy (lib/auth-client.ts).
    accessTokens,
    fetch: notifyingFetch,
  });
  const principalId = getActingAs();
  if (!principalId) return client;
  if (actingClient?.principalId !== principalId) {
    actingClient = { principalId, client: client.actAs(principalId) };
  }
  return actingClient.client;
}
