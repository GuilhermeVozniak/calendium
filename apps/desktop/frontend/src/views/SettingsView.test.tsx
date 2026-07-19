import type { ConnectedAccount } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as the other desktop view suites: force orMock()
// through the mock branch and supply test-controlled fixtures.
const fixtures = vi.hoisted(() => ({
  accounts: [] as ConnectedAccount[],
}));

const setSignatureMock = vi.fn();
const setAutoBccMock = vi.fn();

vi.mock('@/lib/api', () => ({
  api: {
    getMe: vi.fn(),
    listAccounts: vi.fn(),
    getSubscription: vi.fn(),
    disconnectAccount: vi.fn(),
    connectAccount: vi.fn(),
    listClassifiers: vi.fn(),
    createClassifier: vi.fn(),
    updateClassifier: vi.fn(),
    deleteClassifier: vi.fn(),
    setSignature: vi.fn(),
    setAutoBcc: vi.fn(),
  },
  apiConfigured: () => false,
  orMock: async (_real: () => unknown, mock: () => unknown) => mock(),
}));

vi.mock('@/lib/mock', () => ({
  get mockAccounts() {
    return fixtures.accounts;
  },
  mockUser: {
    id: 'usr_me',
    email: 'ada@calendium.app',
    name: 'Ada Lovelace',
    avatarUrl: null,
    createdAt: new Date().toISOString(),
  },
  mockSubscription: () => ({
    status: 'none',
    plan: 'annual',
    priceUsd: 50,
    currentPeriodEnd: null,
    cancelAtPeriodEnd: false,
    trialEndsAt: null,
  }),
  listMockClassifiers: () => [],
  createMockClassifier: vi.fn(),
  updateMockClassifier: vi.fn(),
  deleteMockClassifier: vi.fn(),
  updateMockSignature: (accountId: string, signatureHtml: string) => {
    setSignatureMock(accountId, signatureHtml);
    const account = fixtures.accounts.find((a) => a.id === accountId)!;
    return { ...account, signatureHtml };
  },
  updateMockAutoBcc: (accountId: string, autoBcc: string[]) => {
    setAutoBccMock(accountId, autoBcc);
    const account = fixtures.accounts.find((a) => a.id === accountId)!;
    return { ...account, autoBcc };
  },
}));

vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({
    config: { features: { billing: false, google: true, microsoft: false, ai: false, push: false } },
    clear: vi.fn(),
    demoMode: false,
    exitDemo: vi.fn(),
  }),
  webOrigin: () => null,
}));

vi.mock('@/lib/auth', () => ({
  clearStoredToken: vi.fn(),
  signOut: vi.fn(),
}));

vi.mock('@/lib/wails', () => ({
  desktop: { GetAppVersion: () => Promise.resolve('0.1.0'), OpenExternal: vi.fn() },
  isDesktop: false,
  onDeepLink: () => () => {},
}));

vi.mock('@/lib/toast', () => ({
  toast: vi.fn(),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

const setNamedThemeMock = vi.fn();
vi.mock('@/lib/named-theme', () => ({
  THEME_NAMES: ['neutral', 'ocean', 'forest', 'sunset'],
  THEME_SWATCHES: {
    neutral: { light: 'hsl(0 0% 9%)', dark: 'hsl(0 0% 98%)' },
    ocean: { light: 'hsl(217 72% 46%)', dark: 'hsl(213 80% 66%)' },
    forest: { light: 'hsl(158 55% 34%)', dark: 'hsl(152 45% 60%)' },
    sunset: { light: 'hsl(24 82% 48%)', dark: 'hsl(27 90% 62%)' },
  },
  getStoredNamedTheme: () => 'neutral',
  setNamedTheme: (...args: unknown[]) => setNamedThemeMock(...args),
  syncNamedThemeFromServer: () => Promise.resolve(null),
}));

import { SettingsView } from './SettingsView';

function makeAccount(overrides: Partial<ConnectedAccount> = {}): ConnectedAccount {
  return {
    id: 'acc_google',
    provider: 'google',
    email: 'ada@calendium.app',
    status: 'active',
    scopes: [],
    vipSenders: [],
    signatureHtml: '',
    autoBcc: [],
    lastSyncedAt: null,
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

function renderSettings() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <SettingsView />
    </QueryClientProvider>
  );
}

describe('SettingsView — named theme picker', () => {
  beforeEach(() => {
    setNamedThemeMock.mockReset();
  });

  it('lists the four palettes and switches on click', async () => {
    renderSettings();
    await screen.findByText('Appearance');

    for (const label of ['Neutral', 'Ocean', 'Forest', 'Sunset']) {
      expect(screen.getByRole('button', { name: label })).toBeTruthy();
    }

    await userEvent.click(screen.getByRole('button', { name: 'Ocean' }));
    expect(setNamedThemeMock).toHaveBeenCalledWith('ocean');
  });
});

describe('SettingsView — signature & auto-BCC', () => {
  beforeEach(() => {
    fixtures.accounts = [makeAccount()];
    setSignatureMock.mockReset();
    setAutoBccMock.mockReset();
  });

  it('saves a typed signature and auto-BCC list for the account', async () => {
    renderSettings();
    await screen.findByText(/signature & auto-bcc/i);

    await userEvent.click(screen.getByText(/signature & auto-bcc/i));

    const sigBox = await screen.findByLabelText('Signature');
    await userEvent.type(sigBox, 'Ada Lovelace');
    const bccBox = screen.getByLabelText('Auto-BCC');
    await userEvent.type(bccBox, 'archive@calendium.app');

    await userEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(setSignatureMock).toHaveBeenCalledWith('acc_google', '<p>Ada Lovelace</p>'));
    await waitFor(() => expect(setAutoBccMock).toHaveBeenCalledWith('acc_google', ['archive@calendium.app']));
  });

  it('pre-fills the signature textarea from the account, round-tripped to plain text', async () => {
    fixtures.accounts = [makeAccount({ signatureHtml: '<p>Existing sig</p>', autoBcc: ['a@b.com'] })];
    renderSettings();
    await screen.findByText(/signature & auto-bcc/i);
    await userEvent.click(screen.getByText(/signature & auto-bcc/i));

    expect((await screen.findByLabelText('Signature') as HTMLTextAreaElement).value).toBe('Existing sig');
    expect((screen.getByLabelText('Auto-BCC') as HTMLInputElement).value).toBe('a@b.com');
  });
});
