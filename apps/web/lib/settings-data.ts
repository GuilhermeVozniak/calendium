import type { ConnectedAccount, Provider, Snippet, Subscription } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import { settingsMock } from '@/lib/settings-mock';

/**
 * Data-access wrappers for the settings surface. They hit the real API and, in
 * explicit demo mode only (lib/demo.ts), fall back to the in-memory mock
 * (lib/settings-mock.ts). Outside demo mode failures propagate so the UI shows
 * real loading / empty / error states instead of fabricated data.
 */

export async function fetchAccounts(): Promise<ConnectedAccount[]> {
  try {
    return await getApiClient().listAccounts();
  } catch (err) {
    if (DEMO_MODE) return settingsMock.listAccounts();
    throw err;
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
  } catch (err) {
    if (DEMO_MODE) {
      settingsMock.disconnectAccount(id);
      return;
    }
    throw err;
  }
}

/** Replaces the VIP-sender list for a connected account (PUT .../vip-senders). */
export async function setVipSendersApi(
  accountId: string,
  vipSenders: string[]
): Promise<ConnectedAccount> {
  try {
    return await getApiClient().setVipSenders(accountId, vipSenders);
  } catch (err) {
    if (DEMO_MODE) return settingsMock.setVipSenders(accountId, vipSenders);
    throw err;
  }
}

export async function fetchSnippets(): Promise<Snippet[]> {
  try {
    return await getApiClient().listSnippets();
  } catch (err) {
    if (DEMO_MODE) return settingsMock.listSnippets();
    throw err;
  }
}

export async function createSnippetApi(
  input: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml'>
): Promise<Snippet> {
  try {
    return await getApiClient().createSnippet(input);
  } catch (err) {
    if (DEMO_MODE) return settingsMock.createSnippet(input);
    throw err;
  }
}

export async function deleteSnippetApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteSnippet(id);
  } catch (err) {
    if (DEMO_MODE) {
      settingsMock.deleteSnippet(id);
      return;
    }
    throw err;
  }
}

export async function fetchSubscription(): Promise<Subscription> {
  try {
    return await getApiClient().getSubscription();
  } catch (err) {
    if (DEMO_MODE) return settingsMock.getSubscription();
    throw err;
  }
}
