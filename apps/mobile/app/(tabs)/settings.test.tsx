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
jest.mock('@/lib/api', () => ({
  api: {
    listAccounts: (...args: unknown[]) => mockListAccounts(...args),
    getSubscription: (...args: unknown[]) => mockGetSubscription(...args),
    connectAccount: (...args: unknown[]) => jest.fn()(...args),
  },
}));

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

import { act, render, screen } from '@testing-library/react-native';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import SettingsScreen from './settings';

const AI_ENABLED_CONFIG = {
  mode: 'cloud',
  features: { billing: true, google: false, microsoft: false, ai: true, push: false },
};
const AI_DISABLED_CONFIG = {
  mode: 'cloud',
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
