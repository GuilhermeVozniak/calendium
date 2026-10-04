import { ApiClient } from '@calendium/shared';

import { getActiveServerConfig, isDemoMode } from './server-config';
import { getAccessToken } from './auth';

/** True when a server is configured; otherwise mock data drives the UI. */
export function apiConfigured(): boolean {
  return !!getActiveServerConfig()?.serverUrl;
}

const paymentRequiredListeners = new Set<() => void>();

/**
 * Subscribes to every 402 Payment Required the shared client receives (any
 * endpoint) so the App billing gate can re-check the subscription mid-session
 * instead of waiting for its poll. Returns the unsubscribe function.
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
 * Shared typed client for the Calendium REST API. `baseUrl` is a getter so the
 * client always targets the currently-configured server (lib/server-config),
 * even after the user switches servers at runtime.
 */
export const api = new ApiClient({
  get baseUrl(): string {
    return getActiveServerConfig()?.serverUrl || 'http://localhost:8080';
  },
  getAccessToken,
  fetch: notifyingFetch,
});

/**
 * In explicit demo mode ("Try the demo") serves local mock data with no server.
 * Otherwise runs the real API call and lets errors propagate, so the UI shows
 * honest loading / empty / error states instead of masking failures as fake
 * success (honesty policy, docs/architecture.md).
 */
export async function orMock<T>(real: () => Promise<T>, mock: () => T | Promise<T>): Promise<T> {
  if (isDemoMode()) return mock();
  return real();
}

/**
 * Fetches an attachment's raw bytes for preview (the PDF blob-iframe dialog,
 * M2.5). Not part of ApiClient because it returns a Blob rather than JSON —
 * mirrors ApiClient's private request() auth handling for this one binary
 * endpoint (see ApiClient#attachmentContentPath).
 */
export async function fetchAttachmentBlob(attachmentId: string): Promise<Blob> {
  const token = await getAccessToken();
  const baseUrl = getActiveServerConfig()?.serverUrl ?? '';
  const res = await fetch(`${baseUrl}${api.attachmentContentPath(attachmentId)}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) throw new Error(`Could not load the attachment (${res.status}).`);
  return res.blob();
}
