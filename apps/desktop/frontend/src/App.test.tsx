import '@testing-library/jest-dom/vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// App-level billing gate (docs/payments.md): a fetched subscription that denies
// access swaps <main> for PaywallView; billing off, a fetch error, or a pending
// fetch all fail open to the app. Heavy views and host bridges are stubbed so
// only the gate wiring is under test.

const getSubscription = vi.hoisted(() => vi.fn());
const paymentRequired = vi.hoisted(() => ({ listeners: new Set<() => void>() }));
vi.mock('@/lib/api', () => ({
  api: { getSubscription, search: vi.fn() },
  orMock: (real: () => Promise<unknown>) => real(),
  onPaymentRequired: (listener: () => void) => {
    paymentRequired.listeners.add(listener);
    return () => paymentRequired.listeners.delete(listener);
  },
}));
vi.mock('@/lib/mock', () => ({ mockSearch: vi.fn(), mockSubscription: vi.fn() }));

const serverState = vi.hoisted(() => ({ billing: true, demoMode: false }));
vi.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({
    config: { webUrl: 'https://web.example', features: { billing: serverState.billing } },
    demoMode: serverState.demoMode,
  }),
}));

const SetUpdateChecksEnabled = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('@/lib/wails', () => ({
  isDesktop: false,
  desktop: { SetUpdateChecksEnabled },
  wailsRuntime: { EventsOn: () => () => {}, WindowShow: vi.fn() },
  globalShortcutsEnabled: () => false,
  setGlobalShortcutsEnabled: vi.fn(async () => {}),
  onGlobalShortcut: () => () => {},
}));
vi.mock('@/lib/tray', () => ({
  AUTO_JOINED_EVENT: 'auto-joined',
  TRAY_ACTION_EVENT: 'tray-action',
  pushAutoJoinSettings: vi.fn(),
  useTrayFeed: () => {},
}));
vi.mock('@/lib/compose', () => ({ openCompose: vi.fn() }));
vi.mock('@/views/InboxView', () => ({
  InboxView: () => <div data-testid="inbox-view" />,
  emitFocusThread: vi.fn(),
  emitMailAction: vi.fn(),
}));
vi.mock('@/views/CalendarView', () => ({ CalendarView: () => null, emitFocusDate: vi.fn() }));
vi.mock('@/views/SettingsView', () => ({ SettingsView: () => null }));
vi.mock('@/views/ComposeView', () => ({ ComposeHost: () => null }));
vi.mock('@/views/UpdateBanner', () => ({
  UpdateBanner: () => <div data-testid="update-banner" />,
}));
vi.mock('@/views/PaywallView', () => ({
  PaywallView: ({ reason }: { reason: string }) => <div data-testid="paywall-view">{reason}</div>,
}));

import App from './App';

const SUB = { plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null };

function renderApp() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>
  );
}

/** Waits for the gate's fetch to start, then lets its promise settle. */
async function settle() {
  await waitFor(() => expect(getSubscription).toHaveBeenCalled());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  serverState.billing = true;
  serverState.demoMode = false;
});

describe('App update banner and update-check gate', () => {
  it('mounts the update banner even on the paywall screen', async () => {
    getSubscription.mockResolvedValue({ ...SUB, status: 'canceled' });
    renderApp();
    expect(await screen.findByTestId('paywall-view')).toBeInTheDocument();
    expect(screen.getByTestId('update-banner')).toBeInTheDocument();
  });

  it('enables the host update check outside demo mode', async () => {
    serverState.billing = false;
    renderApp();
    expect(screen.getByTestId('update-banner')).toBeInTheDocument();
    expect(SetUpdateChecksEnabled).toHaveBeenCalledWith(true);
    expect(SetUpdateChecksEnabled).not.toHaveBeenCalledWith(false);
  });

  it('keeps the host update check paused and hides the banner in demo mode', async () => {
    serverState.billing = false;
    serverState.demoMode = true;
    renderApp();
    expect(SetUpdateChecksEnabled).toHaveBeenCalledWith(false);
    expect(SetUpdateChecksEnabled).not.toHaveBeenCalledWith(true);
    expect(screen.queryByTestId('update-banner')).not.toBeInTheDocument();
  });
});

describe('App billing gate', () => {
  it('shows PaywallView instead of the app for a denied subscription', async () => {
    getSubscription.mockResolvedValue({ ...SUB, status: 'canceled' });
    renderApp();
    expect(await screen.findByTestId('paywall-view')).toHaveTextContent('canceled');
    expect(screen.queryByTestId('inbox-view')).not.toBeInTheDocument();
  });

  it('shows the app for an entitled subscription', async () => {
    getSubscription.mockResolvedValue({
      ...SUB,
      status: 'trialing',
      trialEndsAt: new Date(Date.now() + 86_400_000).toISOString(),
    });
    renderApp();
    await settle();
    expect(getSubscription).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('inbox-view')).toBeInTheDocument();
    expect(screen.queryByTestId('paywall-view')).not.toBeInTheDocument();
  });

  it('fails open to the app when the subscription fetch errors', async () => {
    getSubscription.mockRejectedValue(new Error('offline'));
    renderApp();
    await settle();
    expect(getSubscription).toHaveBeenCalled();
    expect(screen.getByTestId('inbox-view')).toBeInTheDocument();
    expect(screen.queryByTestId('paywall-view')).not.toBeInTheDocument();
  });

  it('fails open while the subscription is still loading', async () => {
    getSubscription.mockReturnValue(new Promise(() => {}));
    renderApp();
    await settle();
    expect(screen.getByTestId('inbox-view')).toBeInTheDocument();
    expect(screen.queryByTestId('paywall-view')).not.toBeInTheDocument();
  });

  // Mid-session lapse: any 402 from the API client re-checks the subscription
  // at once instead of waiting for the 5-minute poll.
  it('re-evaluates the subscription when the API client sees a 402', async () => {
    getSubscription.mockResolvedValue({
      ...SUB,
      status: 'trialing',
      trialEndsAt: new Date(Date.now() + 86_400_000).toISOString(),
    });
    renderApp();
    await settle();
    expect(screen.getByTestId('inbox-view')).toBeInTheDocument();

    getSubscription.mockResolvedValue({ ...SUB, status: 'trialing', trialEndsAt: new Date(Date.now() - 1000).toISOString() });
    act(() => {
      for (const listener of paymentRequired.listeners) listener();
    });
    expect(await screen.findByTestId('paywall-view')).toHaveTextContent('trial_ended');
    expect(getSubscription).toHaveBeenCalledTimes(2);
  });

  it('never fetches the subscription when billing is off', async () => {
    serverState.billing = false;
    renderApp();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(getSubscription).not.toHaveBeenCalled();
    expect(screen.getByTestId('inbox-view')).toBeInTheDocument();
  });
});
