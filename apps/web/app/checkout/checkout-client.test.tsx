import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CheckoutClient } from './checkout-client';

const envSet = vi.fn();
const initialize = vi.fn();

function stubPaddle() {
  (window as unknown as { Paddle?: unknown }).Paddle = { Environment: { set: envSet }, Initialize: initialize };
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

  // Review Focus: a missing client token must be loud, not a blank page.
  it('reports a configuration error when the client token is unset', async () => {
    vi.stubEnv('NEXT_PUBLIC_PADDLE_CLIENT_TOKEN', '');
    window.history.replaceState(null, '', '/checkout?_ptxn=txn_123');
    render(<CheckoutClient />);
    expect(await screen.findByRole('alert')).toHaveTextContent(/NEXT_PUBLIC_PADDLE_CLIENT_TOKEN/);
    expect(initialize).not.toHaveBeenCalled();
  });
});
