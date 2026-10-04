import type { Subscription } from '@calendium/shared';
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const session: { data: { user: { id: string } } | null; isPending: boolean } = { data: { user: { id: 'u1' } }, isPending: false };
vi.mock('@/lib/auth-client', () => ({ authClient: { useSession: () => session } }));

const getSubscriptionMock = vi.fn();
vi.mock('@/lib/api', () => ({ getApiClient: () => ({ getSubscription: (...a: unknown[]) => getSubscriptionMock(...a) }) }));

import { CheckoutSuccessClient } from './success-client';

function sub(status: Subscription['status']): Subscription {
  return { status, plan: 'annual', priceUsd: 50, currentPeriodEnd: null, cancelAtPeriodEnd: false, trialEndsAt: null };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  session.data = { user: { id: 'u1' } };
  session.isPending = false;
});
afterEach(() => vi.useRealTimers());

describe('/checkout/success', () => {
  it('polls every 2s until the subscription is active, then links to the app', async () => {
    getSubscriptionMock.mockResolvedValueOnce(sub('trialing')).mockResolvedValueOnce(sub('active'));
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(screen.getByText(/Activating/)).toBeInTheDocument();
    expect(getSubscriptionMock).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('heading', { name: "You're all set" })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open Calendium/ })).toHaveAttribute('href', '/mail');
  });

  it('announces status changes in a persistent polite live region', async () => {
    getSubscriptionMock.mockResolvedValueOnce(sub('trialing')).mockResolvedValueOnce(sub('active'));
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    const region = screen.getByText(/Activating/).closest('[aria-live="polite"]');
    expect(region).not.toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(region).toBeInTheDocument();
    expect(region).toHaveTextContent(/Annual plan is active/);
  });

  // A session refetch yields a new object for the same user: polling must
  // not restart (attempts reset / resume after "active").
  it('does not restart polling when the session object changes for the same user', async () => {
    getSubscriptionMock.mockResolvedValue(sub('active'));
    const { rerender } = render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(1);
    session.data = { user: { id: 'u1' } };
    rerender(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(4000));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(1);
  });

  it('gives up after maxAttempts and still links to the app', async () => {
    getSubscriptionMock.mockResolvedValue(sub('trialing'));
    render(<CheckoutSuccessClient pollMs={2000} maxAttempts={3} />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(getSubscriptionMock).toHaveBeenCalledTimes(3);
    expect(screen.getByText(/taking longer than usual/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Open Calendium/ })).toHaveAttribute('href', '/mail');
  });

  it('tolerates transient polling errors', async () => {
    getSubscriptionMock.mockRejectedValueOnce(new Error('net')).mockResolvedValueOnce(sub('active'));
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(screen.getByRole('heading', { name: "You're all set" })).toBeInTheDocument();
  });

  it('without a session tells the user to return to the app and never polls', async () => {
    session.data = null;
    render(<CheckoutSuccessClient />);
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.getByText(/Return to the app/)).toBeInTheDocument();
    expect(getSubscriptionMock).not.toHaveBeenCalled();
  });
});
