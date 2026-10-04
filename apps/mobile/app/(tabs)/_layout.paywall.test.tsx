const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));
jest.mock('@/hooks/use-push-registration', () => ({ usePushRegistration: jest.fn() }));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockGetSubscription = jest.fn();
const mockPaymentRequiredListeners = new Set<() => void>();
jest.mock('@/lib/api', () => ({
  api: { getSubscription: (...a: unknown[]) => mockGetSubscription(...a) },
  onPaymentRequired: (listener: () => void) => {
    mockPaymentRequiredListeners.add(listener);
    return () => mockPaymentRequiredListeners.delete(listener);
  },
}));
jest.mock('@/lib/mock', () => ({
  withMockFallback: (real: () => unknown) => real(),
  mockSubscription: { status: 'trialing' },
}));
jest.mock('expo-web-browser', () => ({ openBrowserAsync: jest.fn() }));
// `...requireActual` keeps `cssInterop` (used at import time by components/ui/icon,
// reached through PaywallScreen) alongside the stubbed colour scheme.
jest.mock('nativewind', () => ({
  ...jest.requireActual('nativewind'),
  useColorScheme: () => ({ colorScheme: 'light' }),
}));
jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);
jest.mock('expo-router', () => {
  const React = require('react');
  const { Text, View } = require('react-native');
  function Tabs({ children }: { children: React.ReactNode }) {
    const items = React.Children.toArray(children).filter(React.isValidElement) as React.ReactElement<{ name: string }>[];
    return (
      <View>
        {items.map((c) => (
          <Text key={c.props.name}>{c.props.name}</Text>
        ))}
      </View>
    );
  }
  Tabs.Screen = () => null;
  return { Tabs, Redirect: () => null, useRouter: () => ({ push: jest.fn() }) };
});

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { focusManager, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import TabsLayout from './_layout';

const CLOUD = { mode: 'cloud', webUrl: 'https://web.example', features: { billing: true, google: false, microsoft: false, ai: false, push: false } };
const SELF_HOST = { mode: 'self_host', webUrl: '', features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function renderLayout() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TabsLayout />
    </QueryClientProvider>
  );
}

afterEach(() => focusManager.setFocused(undefined));

beforeEach(() => {
  jest.clearAllMocks();
  mockUseAuth.mockReturnValue({ user: { id: 'u1' }, loading: false, signOut: jest.fn() });
});

describe('TabsLayout billing gate', () => {
  it('renders tabs without fetching when billing is off', async () => {
    mockUseServerConfig.mockReturnValue({ config: SELF_HOST });
    await renderLayout();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();
    expect(mockGetSubscription).not.toHaveBeenCalled();
  });

  it('renders tabs for an entitled subscription', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'trialing', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: new Date(Date.now() + 86_400_000).toISOString() });
    await renderLayout();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();
  });

  it('replaces the tabs with the paywall for a denied subscription', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'canceled', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    await renderLayout();
    await flush();
    expect(screen.getByText("This account doesn't have an active subscription.")).toBeTruthy();
    expect(screen.queryByText('inbox')).toBeNull();
  });

  it('denies a paused subscription and lets Refresh re-check it', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'paused', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    await renderLayout();
    await flush();
    expect(screen.getByText("This account doesn't have an active subscription.")).toBeTruthy();

    mockGetSubscription.mockResolvedValue({ status: 'active', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    await fireEvent.press(screen.getByText('Refresh'));
    await flush();
    expect(mockGetSubscription).toHaveBeenCalledTimes(2);
    expect(screen.getByText('inbox')).toBeTruthy();
  });

  it('refetches the subscription when the app returns to the foreground', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'canceled', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    await renderLayout();
    await flush();
    expect(mockGetSubscription).toHaveBeenCalledTimes(1);

    // Subscribed elsewhere, then foregrounded within the 60s staleTime: the
    // gate must still re-check rather than keep a paying user paywalled.
    mockGetSubscription.mockResolvedValue({ status: 'active', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null });
    await act(async () => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
    });
    await flush();
    expect(mockGetSubscription).toHaveBeenCalledTimes(2);
    expect(screen.getByText('inbox')).toBeTruthy();
  });

  // Mid-session lapse: a 402 from any gated call re-checks the subscription at
  // once, so the user lands on the paywall instead of per-screen 402 errors.
  it('re-evaluates the subscription when the API client sees a 402', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockResolvedValue({ status: 'trialing', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: new Date(Date.now() + 86_400_000).toISOString() });
    await renderLayout();
    await flush();
    expect(screen.getByText('inbox')).toBeTruthy();

    mockGetSubscription.mockResolvedValue({ status: 'trialing', plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: new Date(Date.now() - 1000).toISOString() });
    await act(async () => {
      for (const listener of mockPaymentRequiredListeners) listener();
    });
    await flush();
    expect(mockGetSubscription).toHaveBeenCalledTimes(2);
    expect(screen.queryByText('inbox')).toBeNull();
  });

  it('fails open when the subscription cannot be fetched', async () => {
    mockUseServerConfig.mockReturnValue({ config: CLOUD });
    mockGetSubscription.mockRejectedValue(new Error('offline'));
    await renderLayout();
    await flush();
    // The gate's query-level `retry: 1` overrides the client's `retry: false`
    // and react-query waits its default 1s before that retry; the query stays
    // pending (spinner) until the retry has failed too, so wait it out.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 1_200));
    });
    expect(screen.getByText('inbox')).toBeTruthy();
  });
});
