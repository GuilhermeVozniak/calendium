import { addDays, subDays, subMinutes } from 'date-fns';

import type { ConnectedAccount, Snippet, Subscription } from '@calendium/shared';

/**
 * In-memory mock for the settings surface (accounts, snippets, subscription),
 * used as a transparent fallback while the Go backend is unreachable.
 */

interface SettingsStore {
  accounts: ConnectedAccount[];
  snippets: Snippet[];
  subscription: Subscription;
}

let store: SettingsStore | null = null;
let nextId = 1;

function seed(): SettingsStore {
  const now = new Date();
  return {
    accounts: [
      {
        id: 'acct-mock-google',
        provider: 'google',
        email: 'guilherme.vozniak.a@gmail.com',
        status: 'active',
        scopes: ['gmail.modify', 'gmail.send', 'calendar'],
        vipSenders: ['sam@sequoiacap.com', 'founders@calendium.app'],
        lastSyncedAt: subMinutes(now, 2).toISOString(),
        createdAt: subDays(now, 84).toISOString(),
      },
      {
        id: 'acct-mock-microsoft',
        provider: 'microsoft',
        email: 'g.vozniak@fabrikam.com',
        status: 'reauth_required',
        scopes: ['Mail.ReadWrite', 'Mail.Send', 'Calendars.ReadWrite'],
        vipSenders: [],
        lastSyncedAt: subDays(now, 3).toISOString(),
        createdAt: subDays(now, 30).toISOString(),
      },
    ],
    snippets: [
      {
        id: 'snip-thanks',
        name: 'Thanks — will review',
        shortcut: 'ty',
        bodyHtml: '<p>Thanks for sending this over — I’ll review and get back to you by end of day tomorrow.</p>',
        usageCount: 42,
      },
      {
        id: 'snip-call',
        name: 'Schedule a call',
        shortcut: 'call',
        bodyHtml:
          '<p>Happy to jump on a call — here are a few times that work for me this week:</p><p>• Tue 2:00–4:00pm<br/>• Wed 10:00–11:30am<br/>• Thu after 3:00pm</p><p>Or grab any slot that suits you better.</p>',
        usageCount: 31,
      },
      {
        id: 'snip-intro',
        name: 'Intro reply (both to bcc)',
        shortcut: 'intro',
        bodyHtml: '<p>Thanks for the intro (moving you to bcc)!</p><p>Great to meet you — would love to find 30 minutes this week to chat.</p>',
        usageCount: 18,
      },
      {
        id: 'snip-decline',
        name: 'Polite decline',
        shortcut: 'no',
        bodyHtml: '<p>Thanks for thinking of me — I’m going to pass for now, but please keep me posted on how it goes.</p>',
        usageCount: 9,
      },
    ],
    subscription: {
      status: 'trialing',
      plan: 'annual',
      priceUsd: 50,
      currentPeriodEnd: addDays(now, 9).toISOString(),
      cancelAtPeriodEnd: false,
      trialEndsAt: addDays(now, 9).toISOString(),
    },
  };
}

function getStore(): SettingsStore {
  store ??= seed();
  return store;
}

export const settingsMock = {
  listAccounts(): ConnectedAccount[] {
    return getStore().accounts.map((a) => ({ ...a }));
  },

  disconnectAccount(id: string): void {
    const s = getStore();
    s.accounts = s.accounts.filter((a) => a.id !== id);
  },

  setVipSenders(accountId: string, vipSenders: string[]): ConnectedAccount {
    const account = getStore().accounts.find((a) => a.id === accountId);
    if (!account) throw new Error('Account not found');
    account.vipSenders = [...vipSenders];
    return { ...account };
  },

  listSnippets(): Snippet[] {
    return getStore().snippets.map((s) => ({ ...s }));
  },

  createSnippet(input: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml'>): Snippet {
    const snippet: Snippet = { id: `snip-local-${nextId++}`, usageCount: 0, ...input };
    getStore().snippets.unshift(snippet);
    return { ...snippet };
  },

  deleteSnippet(id: string): void {
    const s = getStore();
    s.snippets = s.snippets.filter((x) => x.id !== id);
  },

  getSubscription(): Subscription {
    return { ...getStore().subscription };
  },
};
