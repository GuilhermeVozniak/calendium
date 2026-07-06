import { ApiClient, ApiRequestError } from '@calendium/shared';

import { getActiveServerConfig } from './server-config';
import { getAccessToken } from './supabase';

/** Public pricing page — the fallback target for the desktop checkout flow. */
export const PRICING_URL = 'https://calendium.app/pricing';
export const CHECKOUT_SUCCESS_URL = 'https://calendium.app/checkout/success';

/** True when a server is configured; otherwise mock data drives the UI. */
export function apiConfigured(): boolean {
  return !!getActiveServerConfig()?.serverUrl;
}

/**
 * Shared typed client for the Calendium REST API. `baseUrl` is a getter so the
 * client always targets the currently-configured server (lib/server-config),
 * even after the user switches servers at runtime.
 */
export const api = new ApiClient({
  get baseUrl(): string {
    return getActiveServerConfig()?.serverUrl || 'http://localhost:8080';
  },
  getAccessToken,
});

/**
 * Runs `real` against the API, falling back to `mock` only when no server is
 * configured or the API is unreachable (network/connection failure) — keeps the
 * app usable standalone. Real HTTP error responses (ApiRequestError, i.e. 4xx/5xx)
 * propagate so genuine failures surface instead of being masked by demo data.
 */
export async function orMock<T>(real: () => Promise<T>, mock: () => T | Promise<T>): Promise<T> {
  if (!apiConfigured()) return mock();
  try {
    return await real();
  } catch (error) {
    if (error instanceof ApiRequestError) throw error;
    return mock();
  }
}
