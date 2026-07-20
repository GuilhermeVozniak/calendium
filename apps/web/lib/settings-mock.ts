import { addDays, subDays, subMinutes } from 'date-fns';

import type {
  CalendarSubscription,
  CalendarSubscriptionInput,
  CalendarSubscriptionPatch,
  ConnectedAccount,
  Snippet,
  Subscription,
} from '@calendium/shared';

/**
 * In-memory mock for the settings surface (accounts, snippets, subscription),
 * used as a transparent fallback while the Go backend is unreachable.
 */

interface SettingsStore {
  accounts: ConnectedAccount[];
  snippets: Snippet[];
  subscription: Subscription;
  calendarSubscriptions: CalendarSubscription[];
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
        signatureHtml: '<p>Guilherme Vozniak<br/>Calendium</p>',
        autoBcc: ['archive@calendium.app'],
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
        signatureHtml: '',
        autoBcc: [],
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
    calendarSubscriptions: [
      {
        id: 'sub-mock-holidays',
        url: 'https://ics.calendarlabs.com/76/us-holidays.ics',
        name: 'Holidays',
        color: '#8b5cf6',
        isVisible: true,
        lastFetchedAt: subMinutes(now, 25).toISOString(),
        lastError: null,
        createdAt: subDays(now, 12).toISOString(),
      },
    ],
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

  setSignature(accountId: string, signatureHtml: string): ConnectedAccount {
    const account = getStore().accounts.find((a) => a.id === accountId);
    if (!account) throw new Error('Account not found');
    account.signatureHtml = signatureHtml;
    return { ...account };
  },

  setAutoBcc(accountId: string, autoBcc: string[]): ConnectedAccount {
    const account = getStore().accounts.find((a) => a.id === accountId);
    if (!account) throw new Error('Account not found');
    account.autoBcc = [...autoBcc];
    return { ...account };
  },

  listSnippets(): Snippet[] {
    return getStore().snippets.map((s) => ({ ...s }));
  },

  createSnippet(input: Pick<Snippet, 'name' | 'shortcut' | 'bodyHtml' | 'teamId'>): Snippet {
    const snippet: Snippet = {
      id: `snip-local-${nextId++}`,
      usageCount: 0,
      ...input,
      teamId: input.teamId ?? null,
    };
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

  // --- Interesting-calendar ICS feeds (M2.8 Task 15) ---

  listCalendarSubscriptions(): CalendarSubscription[] {
    return getStore().calendarSubscriptions.map((s) => ({ ...s }));
  },

  createCalendarSubscription(input: CalendarSubscriptionInput): CalendarSubscription {
    const sub: CalendarSubscription = {
      id: `sub-local-${nextId++}`,
      url: input.url,
      name: input.name || new URL(input.url).host,
      color: input.color ?? '#8b5cf6',
      isVisible: true,
      lastFetchedAt: new Date().toISOString(),
      lastError: null,
      createdAt: new Date().toISOString(),
    };
    getStore().calendarSubscriptions.push(sub);
    return { ...sub };
  },

  updateCalendarSubscription(id: string, patch: CalendarSubscriptionPatch): CalendarSubscription {
    const sub = getStore().calendarSubscriptions.find((s) => s.id === id);
    if (!sub) throw new Error('Subscription not found');
    if (patch.name !== undefined) sub.name = patch.name;
    if (patch.color !== undefined) sub.color = patch.color;
    if (patch.isVisible !== undefined) sub.isVisible = patch.isVisible;
    return { ...sub };
  },

  deleteCalendarSubscription(id: string): void {
    const s = getStore();
    s.calendarSubscriptions = s.calendarSubscriptions.filter((x) => x.id !== id);
  },
};
