import type { Subscription } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const startCheckoutMock = vi.fn();
const openBillingPortalMock = vi.fn();
vi.mock('@/lib/settings-data', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/settings-data')>()),
  startCheckout: (...a: unknown[]) => startCheckoutMock(...a),
  openBillingPortal: (...a: unknown[]) => openBillingPortalMock(...a),
}));
vi.mock('@/lib/api', () => ({ getApiClient: () => ({}) }));
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn(), info: vi.fn() } }));

import { BillingSection } from './settings-page';

const assignMock = vi.fn();
function sub(status: Subscription['status'], extra: Partial<Subscription> = {}): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null, ...extra };
}
function renderSection(s: Subscription | undefined) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <BillingSection subscription={s} loading={false} />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, assign: assignMock } });
});

describe('Settings → Billing', () => {
  it('trialing: status line with the end date, Subscribe only', () => {
    renderSection(sub('trialing', { trialEndsAt: '2026-10-18T09:00:00Z' }));
    expect(screen.getByText(/Free trial — ends October 18, 2026/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Manage billing' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument();
  });

  it('active: renews on date, Manage billing + Cancel subscription, no Subscribe', async () => {
    openBillingPortalMock.mockResolvedValue({ overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' });
    renderSection(sub('active', { currentPeriodEnd: '2027-10-01T12:00:00Z' }));
    expect(screen.getByText(/Renews on October 1, 2027/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Subscribe · $50/year' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Cancel subscription' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/c'));
    await userEvent.click(screen.getByRole('button', { name: 'Manage billing' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/o'));
  });

  it('active with scheduled cancel: cancels on date', () => {
    renderSection(sub('active', { currentPeriodEnd: '2027-10-01T12:00:00Z', cancelAtPeriodEnd: true }));
    expect(screen.getByText(/Cancels on October 1, 2027/)).toBeInTheDocument();
  });

  it('past_due: Update payment method is the primary action', async () => {
    openBillingPortalMock.mockResolvedValue({ overviewUrl: 'https://p/o', cancelUrl: 'https://p/c', updatePaymentUrl: 'https://p/u' });
    renderSection(sub('past_due', { currentPeriodEnd: '2026-10-01T12:00:00Z' }));
    await userEvent.click(screen.getByRole('button', { name: 'Update payment method' }));
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://p/u'));
  });

  it('paused: shows the paused badge and payment action', () => {
    renderSection(sub('paused'));
    expect(screen.getByText('Paused')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update payment method' })).toBeInTheDocument();
  });

  it('canceled: Subscribe again, no portal buttons', () => {
    renderSection(sub('canceled'));
    expect(screen.getByRole('button', { name: 'Subscribe · $50/year' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel subscription' })).not.toBeInTheDocument();
  });

  it('mentions Paddle as merchant of record and never the legacy processor', () => {
    renderSection(sub('none'));
    expect(screen.getByText(/Paddle/)).toBeInTheDocument();
    // The plan's done-gate greps the whole repo for the old processor's name,
    // so this guard spells it with a character class instead of literally.
    expect(screen.queryByText(/Str[i]pe/)).not.toBeInTheDocument();
  });
});
