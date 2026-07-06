import type { ConnectedAccount, Provider, Snippet, Subscription } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { settingsMock } from '@/lib/settings-mock';

/**
 * Data-access wrappers for the settings surface: hit the real API first and
 * fall back to the in-memory mock (lib/settings-mock.ts) while the Go backend
 * is not reachable (local dev / demo).
 */

export async function fetchAccounts(): Promise<ConnectedAccount[]> {
  try {
    return await getApiClient().listAccounts();
  } catch {
    return settingsMock.listAccounts();
  }
}

/**
 * Begins the provider OAuth flow. No mock fallback - connecting an account
 * genuinely requires the backend's OAuth dance.
 */
export async function startConnect(provider: Provider, redirectUrl: string): Promise<{ url: string }> {
  return getApiClient().connectAccount(provider, redirectUrl);
}

export async function disconnectAccountApi(id: string): Promise<void> {
  try {
    await getApiClient().disconnectAccount(id);
  } catch {
    settingsMock.disconnectAccount(id);
  }
}

export async function fetchSnippets(): Promise<Snippet[]> {
  try {
    return await getApiClient().listSnippets();
  } catch {
    return settingsMock.listSnippets();
  }
}

export async function createSnippetApi(
  input: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml'>
): Promise<Snippet> {
  try {
    return await getApiClient().createSnippet(input);
  } catch {
    return settingsMock.createSnippet(input);
  }
}

export async function deleteSnippetApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteSnippet(id);
  } catch {
    settingsMock.deleteSnippet(id);
  }
}

export async function fetchSubscription(): Promise<Subscription> {
  try {
    return await getApiClient().getSubscription();
  } catch {
    return settingsMock.getSubscription();
  }
}
