const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockListAccounts = jest.fn();
const mockGetSubscription = jest.fn();
const mockSetSignature = jest.fn();
const mockSetAutoBcc = jest.fn();
const mockUpdatePreferences = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    listAccounts: (...args: unknown[]) => mockListAccounts(...args),
    getSubscription: (...args: unknown[]) => mockGetSubscription(...args),
    connectAccount: (...args: unknown[]) => jest.fn()(...args),
    setSignature: (...args: unknown[]) => mockSetSignature(...args),
    setAutoBcc: (...args: unknown[]) => mockSetAutoBcc(...args),
    updatePreferences: (...args: unknown[]) => mockUpdatePreferences(...args),
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

jest.mock('expo-linking', () => ({
  createURL: jest.fn(() => 'calendium://settings'),
}));

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

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
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
  mockUseAuth.mockReturnValue({
    user: { id: 'u1', name: 'You', email: 'you@example.com', image: null },
    signOut: jest.fn(),
  });
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG, clear: jest.fn() });
  mockListAccounts.mockResolvedValue([]);
  mockGetSubscription.mockResolvedValue({ status: 'none', priceUsd: 50 });
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
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG, clear: jest.fn() });
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
