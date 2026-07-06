import { ApiClient, ApiRequestError } from '@calendium/shared';

import { getAccessToken } from './supabase';

const baseUrl = (import.meta.env.VITE_API_URL as string | undefined) ?? '';

/** True when a backend URL is configured; otherwise mock data drives the UI. */
export const apiConfigured = baseUrl.length > 0;

/** Public pricing page — the fallback target for the desktop checkout flow. */
export const PRICING_URL = 'https://calendium.app/pricing';
export const CHECKOUT_SUCCESS_URL = 'https://calendium.app/checkout/success';

/** Shared typed client for the Calendium REST API. */
export const api = new ApiClient({
  baseUrl: baseUrl || 'http://localhost:8080',
  getAccessToken,
});

/**
 * Runs `real` against the API, falling back to `mock` only when no backend is
 * configured or the API is unreachable (network/connection failure) — keeps the
 * app usable standalone. Real HTTP error responses (ApiRequestError, i.e. 4xx/5xx)
 * propagate so genuine failures surface instead of being masked by demo data.
 */
export async function orMock<T>(real: () => Promise<T>, mock: () => T | Promise<T>): Promise<T> {
  if (!apiConfigured) return mock();
  try {
    return await real();
  } catch (error) {
    if (error instanceof ApiRequestError) throw error;
    return mock();
  }
}
