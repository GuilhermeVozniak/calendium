import '@testing-library/jest-dom/vitest';
import type { ConnectedAccount } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Same isolation approach as the other desktop view suites: force orMock()
// through the mock branch and supply test-controlled fixtures.
const fixtures = vi.hoisted(() => ({
  accounts: [] as ConnectedAccount[],
  ai: false,
  origin: 'https://app.example.com' as string | null,
  authClient: null as null | {
    listAccounts: () => Promise<{ data: { id: string; providerId: string }[] | null; error?: unknown }>;
    deleteUser: (input: { password?: string }) => Promise<{ error: null | Record<string, unknown> }>;
  },
}));
const openExternalMock = vi.hoisted(() => vi.fn());
const clearStoredTokenMock = vi.hoisted(() => vi.fn());
const signOutMock = vi.hoisted(() => vi.fn(async () => undefined));
const signOutLocallyMock = vi.hoisted(() => vi.fn());
const suspendApiMock = vi.hoisted(() => vi.fn());
const resumeApiMock = vi.hoisted(() => vi.fn());
const getMeMock = vi.hoisted(() => vi.fn());
const getSettingsMock = vi.hoisted(() => vi.fn());
const updateSettingsMock = vi.hoisted(() => vi.fn());
const toastMock = vi.hoisted(() => vi.fn());

const setSignatureMock = vi.fn();
const setAutoBccMock = vi.fn();

vi.mock('@/lib/api', () => ({
  api: {
    getMe: getMeMock,
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
    getSettings: (...args: unknown[]) => getSettingsMock(...args),
    updateSettings: (...args: unknown[]) => updateSettingsMock(...args),
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
  mockSettings: () => ({ timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true }),
  updateMockSettings: (s: unknown) => {
    updateSettingsMock(s);
    return s;
  },
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

const serverState = vi.hoisted(() => ({ demoMode: false }));
vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({
    config: { features: { billing: false, google: true, microsoft: false, ai: fixtures.ai, push: false } },
    clear: vi.fn(),
    demoMode: serverState.demoMode,
    exitDemo: vi.fn(),
  }),
  billingWebOrigin: () => fixtures.origin,
  webOrigin: () => fixtures.origin,
}));

vi.mock('@/lib/auth', () => ({
  clearStoredToken: (...args: unknown[]) => clearStoredTokenMock(...args),
  signOut: () => signOutMock(),
  signOutLocally: () => signOutLocallyMock(),
  suspendApi: () => suspendApiMock(),
  resumeApi: () => resumeApiMock(),
  getAuthClient: () => fixtures.authClient,
}));

vi.mock('@/lib/offline', () => ({ clearOfflineState: vi.fn(async () => undefined) }));

const setGlobalShortcutsEnabledMock = vi.hoisted(() => vi.fn(() => Promise.resolve()));

vi.mock('@/lib/wails', () => ({
  desktop: { GetAppVersion: () => Promise.resolve('0.1.0'), OpenExternal: (...args: unknown[]) => openExternalMock(...args) },
  isDesktop: false,
  onDeepLink: () => () => {},
  globalShortcutsEnabled: () => true,
  setGlobalShortcutsEnabled: setGlobalShortcutsEnabledMock,
}));

vi.mock('@/lib/toast', () => ({
  toast: (...args: unknown[]) => toastMock(...args),
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

  it('renders the global-shortcuts toggle on (persisted default) and flips the host on change', async () => {
    renderSettings();
    const toggle = (await screen.findByLabelText(
      'Enable global shortcuts'
    )) as HTMLInputElement;
    expect(toggle.checked).toBe(true);

    await userEvent.click(toggle);
    expect(toggle.checked).toBe(false);
    expect(setGlobalShortcutsEnabledMock).toHaveBeenCalledWith(false);

    await userEvent.click(toggle);
    expect(setGlobalShortcutsEnabledMock).toHaveBeenCalledWith(true);
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

describe('SettingsView — account lifecycle', () => {
  const deleteUser = vi.fn(async (_input: { password?: string }) => ({ error: null as null | Record<string, unknown> }));
  beforeEach(() => {
    fixtures.ai = false;
    fixtures.origin = 'https://app.example.com';
    serverState.demoMode = false;
    deleteUser.mockReset();
    deleteUser.mockResolvedValue({ error: null });
    fixtures.authClient = {
      listAccounts: async () => ({ data: [{ id: 'acc1', providerId: 'credential' }] }),
      deleteUser,
    };
    openExternalMock.mockClear();
    clearStoredTokenMock.mockClear();
    signOutMock.mockClear();
    signOutLocallyMock.mockClear();
    suspendApiMock.mockClear();
    resumeApiMock.mockClear();
    toastMock.mockClear();
  });

  async function fillAndDelete(password: string | null = 'hunter2') {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'ada@calendium.app');
    if (password !== null) await userEvent.type(await screen.findByLabelText('Your password'), password);
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() => expect(deleteUser).toHaveBeenCalled());
  }

  it('pauses the API before deleting and signs out locally with no further request', async () => {
    await fillAndDelete();
    await waitFor(() => expect(signOutLocallyMock).toHaveBeenCalledTimes(1));
    const deleteOrder = deleteUser.mock.invocationCallOrder[0]!;
    expect(suspendApiMock.mock.invocationCallOrder[0]).toBeLessThan(deleteOrder);
    expect(resumeApiMock).not.toHaveBeenCalled();
    // No network sign-out and no Go API call after the delete.
    expect(signOutMock).not.toHaveBeenCalled();
    for (const order of getMeMock.mock.invocationCallOrder) expect(order).toBeLessThan(deleteOrder);
    expect(toastMock).toHaveBeenCalledWith({ title: 'Account deleted' });
  });

  it('a failed delete resumes the API and keeps the session', async () => {
    deleteUser.mockResolvedValue({ error: { status: 500 } });
    await fillAndDelete();
    await waitFor(() => expect(resumeApiMock).toHaveBeenCalledTimes(1));
    expect(signOutLocallyMock).not.toHaveBeenCalled();
    expect(toastMock).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Could not delete your account', description: 'Try again.' })
    );
  });

  it('surfaces a 503 server message', async () => {
    deleteUser.mockResolvedValue({
      error: { status: 503, message: 'Account deletion is not available right now.' },
    });
    await fillAndDelete();
    await waitFor(() =>
      expect(toastMock).toHaveBeenCalledWith(
        expect.objectContaining({ description: 'Account deletion is not available right now.' })
      )
    );
  });

  it('SESSION_EXPIRED on a social account hands off to the browser re-auth, never "Incorrect password"', async () => {
    fixtures.authClient = {
      listAccounts: async () => ({ data: [{ id: 'acc2', providerId: 'google' }] }),
      deleteUser,
    };
    deleteUser.mockResolvedValue({ error: { status: 400, code: 'SESSION_EXPIRED' } });
    await fillAndDelete(null);
    const handoff = await screen.findByRole('button', { name: /continue in browser/i });
    expect(toastMock).not.toHaveBeenCalledWith(expect.objectContaining({ title: 'Incorrect password' }));
    expect(signOutLocallyMock).not.toHaveBeenCalled();
    await userEvent.click(handoff);
    expect(openExternalMock).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
  });

  it('SESSION_EXPIRED on a password account asks for the password again', async () => {
    deleteUser.mockResolvedValueOnce({ error: { status: 400, code: 'SESSION_EXPIRED' } });
    await fillAndDelete();
    await waitFor(() =>
      expect(toastMock).toHaveBeenCalledWith(expect.objectContaining({ title: 'Enter your password' }))
    );
    expect(toastMock).not.toHaveBeenCalledWith(expect.objectContaining({ title: 'Incorrect password' }));
    const pw = screen.getByLabelText('Your password') as HTMLInputElement;
    expect(pw.value).toBe('');
    await userEvent.type(pw, 'hunter2');
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() => expect(deleteUser).toHaveBeenCalledTimes(2));
    expect(deleteUser).toHaveBeenLastCalledWith({ password: 'hunter2' });
  });

  it.each([
    ['rejects', async () => Promise.reject(new Error('offline'))],
    ['returns an error', async () => ({ data: null, error: { status: 500 } })],
  ])('when listAccounts %s it shows an error with Retry instead of assuming social-only', async (_c, failing) => {
    let calls = 0;
    fixtures.authClient = {
      listAccounts: async () => {
        calls += 1;
        return calls === 1 ? failing() : { data: [{ id: 'acc1', providerId: 'credential' }] };
      },
      deleteUser,
    };
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    expect(await screen.findByText(/Couldn’t check how you sign in/)).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Type your email to confirm'), 'ada@calendium.app');
    expect(screen.getByRole('button', { name: /delete my account/i })).toBeDisabled();
    expect(screen.queryByLabelText('Your password')).toBeNull();

    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByLabelText('Your password')).toBeInTheDocument();
    expect(screen.queryByText(/Couldn’t check how you sign in/)).toBeNull();
  });

  it('"Download my data" opens the web account page', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /download my data/i }));
    expect(openExternalMock).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
  });

  it('deleteUser with the password, then signs out locally', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'ada@calendium.app');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() => expect(deleteUser).toHaveBeenCalledWith({ password: 'hunter2' }));
    await waitFor(() => expect(signOutLocallyMock).toHaveBeenCalled());
  });

  it('enables confirm when the typed email matches ignoring case and whitespace', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), '  Ada@Calendium.app ');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(screen.getByRole('button', { name: /delete my account/i })).toBeEnabled();
  });

  it('keeps "Delete my account" disabled until the typed email matches', async () => {
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'someone@else.test');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(screen.getByRole('button', { name: /delete my account/i })).toBeDisabled();
  });

  it('social-only accounts get no password field and call deleteUser({})', async () => {
    fixtures.authClient = {
      listAccounts: async () => ({ data: [{ id: 'acc2', providerId: 'google' }] }),
      deleteUser,
    };
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'ada@calendium.app');
    expect(screen.queryByLabelText('Your password')).toBeNull();
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() => expect(deleteUser).toHaveBeenCalledWith({}));
  });

  it('owns_teams names the blocking teams and keeps the session', async () => {
    deleteUser.mockResolvedValue({
      error: { status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'Design' }] } },
    });
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.type(await screen.findByLabelText('Type your email to confirm'), 'ada@calendium.app');
    await userEvent.type(await screen.findByLabelText('Your password'), 'hunter2');
    await userEvent.click(screen.getByRole('button', { name: /delete my account/i }));
    await waitFor(() =>
      expect(toastMock).toHaveBeenCalledWith(expect.objectContaining({ title: 'Transfer your teams first', description: 'Design' }))
    );
    expect(signOutLocallyMock).not.toHaveBeenCalled();
    expect(signOutMock).not.toHaveBeenCalled();
  });

  it('demo mode renders the buttons but never deletes or opens a URL', async () => {
    serverState.demoMode = true;
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    await userEvent.click(screen.getByRole('button', { name: /download my data/i }));
    expect(toastMock).toHaveBeenCalledWith({ title: 'Not available in demo' });
    expect(deleteUser).not.toHaveBeenCalled();
    expect(openExternalMock).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('Type your email to confirm')).toBeNull();
  });

  it('without a Better Auth client it opens the web settings page instead', async () => {
    fixtures.authClient = null;
    renderSettings();
    await userEvent.click(await screen.findByRole('button', { name: /delete account/i }));
    expect(openExternalMock).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
    expect(screen.queryByLabelText('Type your email to confirm')).toBeNull();
  });
});

describe('SettingsView — background AI switch', () => {
  beforeEach(() => {
    updateSettingsMock.mockClear();
    serverState.demoMode = false;
  });

  it('renders when AI is on and PUTs the flipped document', async () => {
    fixtures.ai = true;
    renderSettings();
    const toggle = (await screen.findByLabelText('Background AI processing')) as HTMLInputElement;
    await waitFor(() => expect(toggle.checked).toBe(true));
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(updateSettingsMock).toHaveBeenCalledWith({
        timeZone: 'UTC',
        workingHours: [],
        workingLocation: '',
        aiBackground: false,
      })
    );
    await waitFor(() => expect(toggle.checked).toBe(false));
  });

  it('a failed PUT leaves the toggle unchanged and says so', async () => {
    fixtures.ai = true;
    toastMock.mockClear();
    updateSettingsMock.mockImplementationOnce(() => {
      throw new Error('boom');
    });
    renderSettings();
    const toggle = (await screen.findByLabelText('Background AI processing')) as HTMLInputElement;
    await waitFor(() => expect(toggle.checked).toBe(true));
    await userEvent.click(toggle);
    await waitFor(() =>
      expect(toastMock).toHaveBeenCalledWith(
        expect.objectContaining({ title: 'Could not update background AI', description: 'boom' })
      )
    );
    expect(updateSettingsMock).toHaveBeenCalledTimes(1);
    expect(toggle.checked).toBe(true);
  });

  it('is absent when the server disables AI', async () => {
    fixtures.ai = false;
    renderSettings();
    await screen.findByText('Appearance');
    expect(screen.queryByLabelText('Background AI processing')).toBeNull();
  });
});
