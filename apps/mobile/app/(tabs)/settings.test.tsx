const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  webOrigin: () => 'https://app.example.com',
}));

const mockListAccounts = jest.fn();
const mockGetSubscription = jest.fn();
const mockSetSignature = jest.fn();
const mockSetAutoBcc = jest.fn();
const mockUpdatePreferences = jest.fn();
const mockGetSettings = jest.fn();
const mockUpdateSettings = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listAccounts: (...args: unknown[]) => mockListAccounts(...args),
    getSubscription: (...args: unknown[]) => mockGetSubscription(...args),
    connectAccount: (...args: unknown[]) => jest.fn()(...args),
    setSignature: (...args: unknown[]) => mockSetSignature(...args),
    setAutoBcc: (...args: unknown[]) => mockSetAutoBcc(...args),
    updatePreferences: (...args: unknown[]) => mockUpdatePreferences(...args),
    getSettings: (...args: unknown[]) => mockGetSettings(...args),
    updateSettings: (...args: unknown[]) => mockUpdateSettings(...args),
  },
}));

jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

const mockPush = jest.fn();
const mockReplace = jest.fn();
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: mockPush, replace: mockReplace }),
}));

const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({
  createURL: jest.fn(() => 'calendium://settings'),
  openURL: (...args: unknown[]) => mockOpenURL(...args),
}));

// Better Auth client surface used by Settings → Account (list-accounts rows
// carry the provider as `providerId`).
const mockListAuthAccounts = jest.fn();
const mockDeleteUser = jest.fn();
const mockAuthClient = {
  listAccounts: (...args: unknown[]) => mockListAuthAccounts(...args),
  deleteUser: (...args: unknown[]) => mockDeleteUser(...args),
};
const mockSignOut = jest.fn();
const mockSignInWithOAuth = jest.fn();

jest.mock('expo-web-browser', () => ({
  openAuthSessionAsync: jest.fn(),
  openBrowserAsync: jest.fn(),
}));

jest.mock('nativewind', () => ({
  ...jest.requireActual('nativewind'),
  useColorScheme: () => ({ colorScheme: 'light', toggleColorScheme: jest.fn() }),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen, within } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Alert } from 'react-native';
import SettingsScreen from './settings';

const AI_ENABLED_CONFIG = {
  mode: 'cloud',
  webUrl: 'https://web.example',
  features: { billing: true, google: false, microsoft: false, ai: true, push: false },
};
const AI_DISABLED_CONFIG = {
  mode: 'cloud',
  webUrl: 'https://web.example',
  features: { billing: true, google: false, microsoft: false, ai: false, push: false },
};

// useQuery resolves on a real macrotask; drive a tick inside `act()` (same
// pattern as compose.test.tsx / ask-ai.test.tsx / classifiers.test.tsx) since
// `waitFor` is unreliable in this jest-expo + React 19 setup.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function renderScreen() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SettingsScreen />
    </QueryClientProvider>
  );
}

const ACCOUNT = {
  id: 'acct_1',
  provider: 'google' as const,
  email: 'you@gmail.com',
  status: 'active' as const,
  scopes: [],
  vipSenders: [],
  signatureHtml: '<p>Best,<br/>Jordan</p>',
  autoBcc: ['archive@example.com'],
  lastSyncedAt: new Date().toISOString(),
  createdAt: new Date().toISOString(),
};

beforeEach(() => {
  jest.clearAllMocks();
  jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  mockUseAuth.mockReturnValue({
    user: { id: 'u1', name: 'You', email: 'you@example.com', image: null },
    signOut: mockSignOut,
    signInWithOAuth: mockSignInWithOAuth,
  });
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG, clear: jest.fn(), authClient: mockAuthClient });
  mockListAccounts.mockResolvedValue([]);
  mockGetSubscription.mockResolvedValue({ status: 'none', priceUsd: 50 });
  mockGetSettings.mockResolvedValue({ timeZone: 'UTC', workingHours: [], workingLocation: '', aiBackground: true });
  mockUpdateSettings.mockImplementation(async (s: unknown) => s);
  mockListAuthAccounts.mockResolvedValue({ data: [{ id: 'acc1', providerId: 'credential' }], error: null });
  mockDeleteUser.mockResolvedValue({ data: { success: true }, error: null });
  mockSignOut.mockResolvedValue(undefined);
});

/** App Store 3.1.1/3.1.3: no price, purchase CTA or purchase/billing link. */
const STORE_FORBIDDEN = /\$|price|subscribe|pricing|checkout/i;
const DAY_MS = 86_400_000;

describe('SettingsScreen — subscription card (store-compliant)', () => {
  it.each([
    [
      'trialing',
      { status: 'trialing', trialEndsAt: new Date(Date.now() + 9 * DAY_MS - 60_000).toISOString() },
      'Trial — 9 days left',
    ],
    ['active', { status: 'active', currentPeriodEnd: null, cancelAtPeriodEnd: false }, 'Active'],
    ['none', { status: 'none' }, 'Inactive'],
  ])('shows %s as status text only, with no link or price', async (_s, sub, text) => {
    mockGetSubscription.mockResolvedValue({ plan: 'annual', priceUsd: 50, trialEndsAt: null, ...sub });
    await renderScreen();
    await flush();

    const card = within(screen.getByTestId('subscription-card'));
    expect(card.getByText(text)).toBeTruthy();
    expect(card.queryAllByText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(card.queryAllByLabelText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(card.queryAllByRole('button')).toHaveLength(0);
    expect(card.queryAllByRole('link')).toHaveLength(0);
    expect(screen.queryByText('Manage on the web')).toBeNull();
  });
});

describe('SettingsScreen — named theme picker', () => {
  it('lists the four palettes and PUTs the chosen theme to the server', async () => {
    mockUpdatePreferences.mockResolvedValue({ theme: 'ocean' });
    await renderScreen();
    await flush();

    for (const name of ['neutral', 'ocean', 'forest', 'sunset']) {
      expect(screen.getByTestId(`theme-${name}`)).toBeTruthy();
    }

    await fireEvent.press(screen.getByTestId('theme-ocean'));
    await flush();

    expect(mockUpdatePreferences).toHaveBeenCalledWith({ theme: 'ocean' });
  });
});

/**
 * Task 17 review fix: nothing previously covered the Settings "AI
 * classifiers" row's visibility toggle (apps/mobile/app/(tabs)/settings.tsx
 * gates the row behind `config.features.ai`, same flag as the tab bar and
 * classifiers.tsx's own gate) — a server that disables AI should hide the
 * entry point entirely.
 */
describe('SettingsScreen — AI classifiers row visibility', () => {
  it('shows the AI classifiers row when the server enables AI', async () => {
    await renderScreen();
    await flush();

    expect(screen.getByText('AI classifiers')).toBeTruthy();
  });

  it('hides the AI classifiers row when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG, clear: jest.fn(), authClient: mockAuthClient });
    await renderScreen();
    await flush();

    expect(screen.queryByText('AI classifiers')).toBeNull();
  });
});

describe('SettingsScreen — signature and auto-BCC editors', () => {
  it('seeds each editor from the account (plain-text signature, comma-joined auto-BCC)', async () => {
    mockListAccounts.mockResolvedValue([ACCOUNT]);
    await renderScreen();
    await flush();

    expect(screen.getByDisplayValue('Best,\nJordan')).toBeTruthy();
    expect(screen.getByDisplayValue('archive@example.com')).toBeTruthy();
  });

  it('saving calls both api.setSignature and api.setAutoBcc with the edited values', async () => {
    mockListAccounts.mockResolvedValue([ACCOUNT]);
    mockSetSignature.mockResolvedValue({ ...ACCOUNT, signatureHtml: '<br/>Best,<br/>Jordan R.' });
    mockSetAutoBcc.mockResolvedValue({ ...ACCOUNT, autoBcc: ['archive@example.com', 'cc@example.com'] });
    await renderScreen();
    await flush();

    await fireEvent.changeText(screen.getByTestId('signature-input-acct_1'), 'Best,\nJordan R.');
    await fireEvent.changeText(
      screen.getByTestId('auto-bcc-input-acct_1'),
      'archive@example.com, cc@example.com'
    );
    await fireEvent.press(screen.getByTestId('save-mail-prefs-acct_1'));
    await flush();

    expect(mockSetSignature).toHaveBeenCalledWith('acct_1', 'Best,<br/>Jordan R.');
    expect(mockSetAutoBcc).toHaveBeenCalledWith('acct_1', ['archive@example.com', 'cc@example.com']);
  });

  /**
   * M2.5 review fix (MUST-FIX 2): the backend's account Update is a full-row
   * read-modify-write, so firing setSignature and setAutoBcc concurrently
   * (Promise.all) let one clobber the other — whichever write landed first
   * got overwritten by the second request's read-before-either-write-lands
   * snapshot. Sequencing (signature, then auto-BCC) fixes it; this proves
   * both the call order and that the cache ends up holding the final
   * response's account (with both fields updated, as the real backend would
   * return once the writes are sequenced).
   */
  it('sequences setSignature before setAutoBcc and caches the final response with both fields', async () => {
    mockListAccounts.mockResolvedValue([ACCOUNT]);
    mockSetSignature.mockResolvedValue({ ...ACCOUNT, signatureHtml: '<br/>Best,<br/>Jordan R.' });
    // Simulates the real backend's read-modify-write: by the time auto-BCC is
    // saved (after signature, sequenced), the signature write has already
    // been persisted, so the final response reflects both fields.
    mockSetAutoBcc.mockResolvedValue({
      ...ACCOUNT,
      signatureHtml: '<br/>Best,<br/>Jordan R.',
      autoBcc: ['archive@example.com', 'cc@example.com'],
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    await render(
      <QueryClientProvider client={client}>
        <SettingsScreen />
      </QueryClientProvider>
    );
    await flush();

    await fireEvent.changeText(screen.getByTestId('signature-input-acct_1'), 'Best,\nJordan R.');
    await fireEvent.changeText(
      screen.getByTestId('auto-bcc-input-acct_1'),
      'archive@example.com, cc@example.com'
    );
    await fireEvent.press(screen.getByTestId('save-mail-prefs-acct_1'));
    await flush();

    expect(mockSetSignature.mock.invocationCallOrder[0]).toBeLessThan(
      mockSetAutoBcc.mock.invocationCallOrder[0]!
    );
    expect(mockSetSignature).toHaveBeenCalledWith('acct_1', 'Best,<br/>Jordan R.');
    expect(mockSetAutoBcc).toHaveBeenCalledWith('acct_1', ['archive@example.com', 'cc@example.com']);
    expect(client.getQueryData(['accounts'])).toEqual([
      {
        ...ACCOUNT,
        signatureHtml: '<br/>Best,<br/>Jordan R.',
        autoBcc: ['archive@example.com', 'cc@example.com'],
      },
    ]);
  });
});

/** Presses the destructive button of the most recent Alert.alert call. */
async function pressAlertConfirm() {
  const calls = (Alert.alert as jest.Mock).mock.calls;
  const buttons = calls[calls.length - 1]?.[2] as { style?: string; onPress?: () => void }[] | undefined;
  const confirm = buttons?.find((b) => b.style === 'destructive');
  if (!confirm?.onPress) throw new Error('no destructive alert button');
  await act(async () => {
    await confirm.onPress?.();
  });
}

describe('SettingsScreen — account lifecycle', () => {
  it('"Download my data" opens the web account page in the browser', async () => {
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Download my data'));
    expect(mockOpenURL).toHaveBeenCalledWith('https://app.example.com/settings?tab=account');
  });

  it('delete: password prompt → deleteUser({password}) → sign-out (clears offline state) → sign-in screen', async () => {
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    await fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'hunter2');
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(mockDeleteUser).toHaveBeenCalledWith({ password: 'hunter2' });
    expect(mockSignOut).toHaveBeenCalled();
    expect(mockReplace).toHaveBeenCalledWith('/');
  });

  it('social-only accounts get no password field and call deleteUser({})', async () => {
    mockListAuthAccounts.mockResolvedValue({ data: [{ id: 'acc2', providerId: 'google' }], error: null });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    expect(screen.queryByPlaceholderText('Your password')).toBeNull();
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(mockDeleteUser).toHaveBeenCalledWith({});
  });

  it('owns_teams lists the blocking teams and keeps the session', async () => {
    mockDeleteUser.mockResolvedValue({
      data: null,
      error: { status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'Design' }] } },
    });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    await fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'hunter2');
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(Alert.alert).toHaveBeenLastCalledWith('Transfer your teams first', expect.stringContaining('Design'));
    expect(mockSignOut).not.toHaveBeenCalled();
  });

  it('a wrong password keeps the session and says so', async () => {
    mockDeleteUser.mockResolvedValue({ data: null, error: { status: 400, code: 'INVALID_PASSWORD' } });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    await fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'nope');
    await fireEvent.press(screen.getByText('Delete my account'));
    await pressAlertConfirm();
    await flush();
    expect(Alert.alert).toHaveBeenLastCalledWith('Incorrect password', expect.any(String));
    expect(mockSignOut).not.toHaveBeenCalled();
  });

  it('demo mode renders the rows but never calls deleteUser or opens a URL', async () => {
    mockUseServerConfig.mockReturnValue({
      config: { ...AI_ENABLED_CONFIG, demoMode: true },
      clear: jest.fn(),
      authClient: mockAuthClient,
    });
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await fireEvent.press(screen.getByText('Download my data'));
    expect(Alert.alert).toHaveBeenCalledWith('Not available in demo');
    expect(mockDeleteUser).not.toHaveBeenCalled();
    expect(mockListAuthAccounts).not.toHaveBeenCalled();
    expect(mockOpenURL).not.toHaveBeenCalled();
  });

  it('shows no price or purchase wording in the Account section', async () => {
    await renderScreen();
    await flush();
    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    expect(screen.queryAllByText(STORE_FORBIDDEN)).toHaveLength(0);
  });
});

describe('SettingsScreen — background AI switch', () => {
  it('reflects the stored value and PUTs the whole document with aiBackground flipped', async () => {
    await renderScreen();
    await flush();
    const toggle = screen.getByLabelText('Background AI processing');
    expect(toggle.props.value).toBe(true);
    await act(async () => {
      fireEvent(toggle, 'valueChange', false);
    });
    await flush();
    expect(mockUpdateSettings).toHaveBeenCalledWith({
      timeZone: 'UTC',
      workingHours: [],
      workingLocation: '',
      aiBackground: false,
    });
    expect(screen.getByLabelText('Background AI processing').props.value).toBe(false);
  });

  it('is hidden when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG, clear: jest.fn(), authClient: mockAuthClient });
    await renderScreen();
    await flush();
    expect(screen.queryByLabelText('Background AI processing')).toBeNull();
    expect(mockGetSettings).not.toHaveBeenCalled();
  });
});
