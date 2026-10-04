import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { PADDLE_JS_SRC } from '@/lib/paddle';

import { CheckoutClient } from './checkout-client';

const envSet = vi.fn();
const initialize = vi.fn();
const checkoutOpen = vi.fn();

function stubPaddle() {
  (window as unknown as { Paddle?: unknown }).Paddle = {
    Environment: { set: envSet },
    Initialize: initialize,
    Checkout: { open: checkoutOpen },
  };
}

/** The eventCallback CheckoutClient handed to Paddle.Initialize. */
function paddleEvent(name: string) {
  const opts = initialize.mock.calls[0][0] as { eventCallback: (e: { name: string }) => void };
  act(() => opts.eventCallback({ name }));
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubEnv('NEXT_PUBLIC_PADDLE_CLIENT_TOKEN', 'test_tok');
  vi.stubEnv('NEXT_PUBLIC_PADDLE_ENV', 'sandbox');
  stubPaddle();
});

afterEach(() => {
  vi.unstubAllEnvs();
  delete (window as unknown as { Paddle?: unknown }).Paddle;
  document.getElementById('paddle-js')?.remove();
  window.history.replaceState(null, '', '/checkout');
});

describe('/checkout', () => {
  it('initialises Paddle.js in sandbox with the fixed success url when _ptxn is present', async () => {
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(1));
    expect(envSet).toHaveBeenCalledWith('sandbox');
    expect(initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        token: 'test_tok',
        checkout: { settings: expect.objectContaining({ successUrl: `${window.location.origin}/checkout/success`, displayMode: 'overlay' }) },
      })
    );
    expect(screen.getByText(/Opening secure checkout/)).toBeInTheDocument();
  });

  it('does not call Environment.set outside sandbox', async () => {
    vi.stubEnv('NEXT_PUBLIC_PADDLE_ENV', 'production');
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalled());
    expect(envSet).not.toHaveBeenCalled();
  });

  it('shows "nothing to pay" without _ptxn and never initialises', async () => {
    render(<CheckoutClient />);
    expect(await screen.findByRole('heading', { name: 'Nothing to pay' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Back to Calendium/ })).toHaveAttribute('href', '/mail');
    expect(initialize).not.toHaveBeenCalled();
  });

  it('shows a "Checkout closed" state when the overlay is dismissed — no endless spinner', async () => {
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(1));
    paddleEvent('checkout.closed');
    expect(screen.getByRole('heading', { name: 'Checkout closed' })).toBeInTheDocument();
    expect(screen.queryByText(/Opening secure checkout/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Back to billing/ })).toHaveAttribute('href', '/settings?tab=billing');
  });

  it('Retry reopens the overlay for the same transaction', async () => {
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(1));
    paddleEvent('checkout.closed');
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(checkoutOpen).toHaveBeenCalledWith({ transactionId: 'txn_123' });
    expect(screen.getByText(/Opening secure checkout/)).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Checkout closed' })).not.toBeInTheDocument();
  });

  it('a close after a completed payment does not show the closed state', async () => {
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    await waitFor(() => expect(initialize).toHaveBeenCalledTimes(1));
    paddleEvent('checkout.completed');
    paddleEvent('checkout.closed');
    expect(screen.queryByRole('heading', { name: 'Checkout closed' })).not.toBeInTheDocument();
  });

  it('injects Paddle.js from the CDN and reports a load error', async () => {
    delete (window as unknown as { Paddle?: unknown }).Paddle;
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    const script = await waitFor(() => {
      const el = document.getElementById('paddle-js') as HTMLScriptElement | null;
      expect(el).not.toBeNull();
      return el as HTMLScriptElement;
    });
    expect(script.src).toBe(PADDLE_JS_SRC);
    act(() => {
      script.dispatchEvent(new Event('error'));
    });
    expect(await screen.findByRole('alert')).toHaveTextContent(/Could not load the payment form/);
    expect(initialize).not.toHaveBeenCalled();
  });

  // Review Focus: a missing client token must be loud, not a blank page.
  it('reports a configuration error when the client token is unset', async () => {
    vi.stubEnv('NEXT_PUBLIC_PADDLE_CLIENT_TOKEN', '');
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/NEXT_PUBLIC_PADDLE_CLIENT_TOKEN/);
    expect(initialize).not.toHaveBeenCalled();
  });
});
