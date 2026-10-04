import type { BillingPortalUrls, InstanceInfo, Subscription } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const fetchSubscriptionMock = vi.fn();
const startCheckoutMock = vi.fn();
const openBillingPortalMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchSubscription: (...args: unknown[]) => fetchSubscriptionMock(...args),
  startCheckout: (...args: unknown[]) => startCheckoutMock(...args),
  openBillingPortal: (...args: unknown[]) => openBillingPortalMock(...args),
}));

const instanceState: { data: InstanceInfo | undefined; isPending: boolean } = { data: undefined, isPending: false };
vi.mock('@/lib/use-instance', () => ({ useInstance: () => instanceState }));

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));

const signOutMock = vi.fn(async () => {});
vi.mock('@/lib/sign-out', () => ({ performSignOut: () => signOutMock() }));

const paymentRequiredListeners = new Set<() => void>();
vi.mock('@/lib/api', () => ({
  onPaymentRequired: (listener: () => void) => {
    paymentRequiredListeners.add(listener);
    return () => paymentRequiredListeners.delete(listener);
  },
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn(), info: vi.fn() } }));

import { BillingGate, BillingTrialBanner, PaywallScreen, TrialBanner } from './paywall';

const assignMock = vi.fn();

function instance(billing: boolean): InstanceInfo {
  return {
    name: 'Calendium', mode: 'cloud', version: 't', authBaseUrl: 'http://localhost/api/auth', authProviders: ['email'],
    undoSendSeconds: 15, webUrl: 'http://localhost:3000',
    features: { billing, google: false, microsoft: false, ai: false, push: false },
  };
}

function sub(status: Subscription['status'], extra: Partial<Subscription> = {}): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null, ...extra };
}

const PORTAL: BillingPortalUrls = { overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' };

function renderWithQuery(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return { qc, ...render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>) };
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  instanceState.data = instance(true);
  instanceState.isPending = false;
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, assign: assignMock, origin: 'http://localhost:3000' },
  });
});

describe('PaywallScreen', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended', 'Subscribe · $50/year'],
    ['none', 'Subscribe to keep using Calendium', 'Subscribe · $50/year'],
    ['canceled', 'Your subscription has ended', 'Subscribe · $50/year'],
    ['past_due', 'Payment failed', 'Update payment method'],
    ['paused', 'Your subscription is paused', 'Update payment method'],
  ] as const)('renders %s copy and primary action', (reason, heading, action) => {
    renderWithQuery(<PaywallScreen reason={reason} />);
    expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: action })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Sign out/ })).toBeInTheDocument();
  });

  it('moves focus to the heading on mount and announces the status politely', () => {
    renderWithQuery(<PaywallScreen reason="canceled" />);
    expect(screen.getByRole('heading', { name: 'Your subscription has ended' })).toHaveFocus();
    expect(screen.getByText(/Resubscribe any time/).closest('[aria-live="polite"]')).not.toBeNull();
  });

  it('past_due shows a secondary Manage billing action', () => {
    renderWithQuery(<PaywallScreen reason="past_due" />);
    expect(screen.getByRole('button', { name: 'Manage billing' })).toBeInTheDocument();
  });

  it('Subscribe starts a checkout and navigates to the returned url', async () => {
    startCheckoutMock.mockResolvedValue({ url: 'http://localhost:3000/checkout?_ptxn=txn_1' });
    renderWithQuery(<PaywallScreen reason="trial_ended" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('http://localhost:3000/checkout?_ptxn=txn_1'));
  });

  it('a 409 already_subscribed falls through to the portal overview', async () => {
    startCheckoutMock.mockRejectedValue(new ApiRequestError(409, 'already_subscribed', 'x'));
    openBillingPortalMock.mockResolvedValue(PORTAL);
    renderWithQuery(<PaywallScreen reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
    expect(toastError).not.toHaveBeenCalled();
  });

  it('Update payment method opens the portal update link', async () => {
    openBillingPortalMock.mockResolvedValue(PORTAL);
    renderWithQuery(<PaywallScreen reason="past_due" />);
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/u'));
  });

  it('falls back to the overview when the update link is empty', async () => {
    openBillingPortalMock.mockResolvedValue({ ...PORTAL, updatePaymentUrl: '' });
    renderWithQuery(<PaywallScreen reason="paused" />);
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
  });

  it('Sign out ends the session and goes to /signin', async () => {
    renderWithQuery(<PaywallScreen reason="canceled" />);
    await userEvent.click(screen.getByRole('button', { name: /Sign out/ }));
    await waitFor(() => expect(signOutMock).toHaveBeenCalled());
    expect(replaceMock).toHaveBeenCalledWith('/signin');
  });

  it('surfaces a toast when the billing API fails', async () => {
    startCheckoutMock.mockRejectedValue(new ApiRequestError(502, 'billing_unavailable', 'x'));
    renderWithQuery(<PaywallScreen reason="none" />);
    await userEvent.click(screen.getByRole('button', { name: 'Subscribe · $50/year' }));
    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(assignMock).not.toHaveBeenCalled();
  });
});

describe('TrialBanner', () => {
  const twoDays = new Date(Date.now() + 2 * 24 * 3600_000).toISOString();
  const tenDays = new Date(Date.now() + 10 * 24 * 3600_000).toISOString();

  it('shows only during the last 3 trial days', () => {
    const { rerender } = renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: tenDays })} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />
      </QueryClientProvider>
    );
    expect(screen.getByRole('status')).toHaveTextContent(/Your free trial ends in 2 days/);
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
  });

  it('dismiss hides it for the rest of the day and persists', async () => {
    renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />);
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss for today' }));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(window.localStorage.getItem('calendium.trial-banner.dismissed')).toBe(new Date().toISOString().slice(0, 10));
    renderWithQuery(<TrialBanner subscription={sub('trialing', { trialEndsAt: twoDays })} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('renders nothing for non-trial subscriptions', () => {
    renderWithQuery(<TrialBanner subscription={sub('active')} />);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});

describe('BillingGate', () => {
  const child = <div data-testid="app">inbox</div>;

  it('renders children immediately when billing is off', () => {
    instanceState.data = instance(false);
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(screen.getByTestId('app')).toBeInTheDocument();
    expect(fetchSubscriptionMock).not.toHaveBeenCalled();
  });

  it('waits for instance discovery before deciding', () => {
    instanceState.data = undefined;
    instanceState.isPending = true;
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument();
  });

  it('renders children for an entitled subscription', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('trialing', { trialEndsAt: new Date(Date.now() + 10 * 24 * 3600_000).toISOString() }));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByTestId('app')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Subscription required' })).not.toBeInTheDocument();
  });

  it('replaces children with the paywall for a denied subscription', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('canceled'));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByRole('heading', { name: 'Your subscription has ended' })).toBeInTheDocument();
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
  });

  // Review Focus: fail open when the subscription cannot be fetched.
  it('fails open when the subscription request errors', async () => {
    fetchSubscriptionMock.mockRejectedValue(new Error('network down'));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    // The gate retries once (React Query's default ~1s backoff) before
    // settling into the error state, so allow more than findBy's 1s default.
    expect(await screen.findByTestId('app', {}, { timeout: 4000 })).toBeInTheDocument();
    expect(fetchSubscriptionMock).toHaveBeenCalledTimes(2);
  });

  // Fail open only when no subscription was ever loaded: a failed background
  // refetch must not lift a paywall that a successful fetch already showed.
  it('keeps the paywall when a background refetch fails after a denial', async () => {
    fetchSubscriptionMock.mockResolvedValueOnce(sub('canceled'));
    const { qc } = renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByRole('heading', { name: 'Your subscription has ended' })).toBeInTheDocument();
    fetchSubscriptionMock.mockRejectedValue(new Error('network down'));
    await act(() => qc.refetchQueries({ queryKey: ['subscription'] }));
    await waitFor(() => expect(qc.getQueryState(['subscription'])?.status).toBe('error'));
    expect(screen.getByRole('heading', { name: 'Your subscription has ended' })).toBeInTheDocument();
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
  });

  // Mid-session lapse: any 402 from the API client re-evaluates the gate
  // immediately instead of waiting for the 5-minute poll.
  it('re-evaluates the subscription when the API client sees a 402', async () => {
    fetchSubscriptionMock.mockResolvedValueOnce(sub('active'));
    renderWithQuery(<BillingGate>{child}</BillingGate>);
    expect(await screen.findByTestId('app')).toBeInTheDocument();
    fetchSubscriptionMock.mockResolvedValue(sub('canceled'));
    act(() => {
      for (const listener of paymentRequiredListeners) listener();
    });
    expect(await screen.findByRole('heading', { name: 'Your subscription has ended' })).toBeInTheDocument();
    expect(screen.queryByTestId('app')).not.toBeInTheDocument();
  });

  it('shows the trial banner above children in the last 3 days', async () => {
    fetchSubscriptionMock.mockResolvedValue(sub('trialing', { trialEndsAt: new Date(Date.now() + 2 * 24 * 3600_000).toISOString() }));
    renderWithQuery(
      <BillingGate>
        <BillingTrialBanner />
        {child}
      </BillingGate>
    );
    expect(await screen.findByTestId('app')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent(/trial ends/);
  });
});
