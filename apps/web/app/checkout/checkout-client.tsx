'use client';

import * as React from 'react';
import Link from 'next/link';
import { CalendarRange, Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { initPaddle, loadPaddleJs } from '@/lib/paddle';

type State =
  | { kind: 'loading' }
  | { kind: 'ready' }
  | { kind: 'no-transaction' }
  | { kind: 'error'; message: string };

/**
 * Paddle's default payment link points here. Paddle.js opens the overlay
 * for the `_ptxn` transaction created by POST /v1/billing/checkout; the
 * success URL is fixed to /checkout/success (never client-supplied).
 */
export function CheckoutClient() {
  const [state, setState] = React.useState<State>({ kind: 'loading' });

  React.useEffect(() => {
    const txn = new URLSearchParams(window.location.search).get('_ptxn');
    if (!txn) {
      setState({ kind: 'no-transaction' });
      return;
    }
    const token = process.env.NEXT_PUBLIC_PADDLE_CLIENT_TOKEN;
    if (!token) {
      setState({
        kind: 'error',
        message: 'Checkout is not configured on this deployment: NEXT_PUBLIC_PADDLE_CLIENT_TOKEN is unset.',
      });
      return;
    }
    let cancelled = false;
    loadPaddleJs()
      .then((paddle) => {
        if (cancelled) return;
        initPaddle(paddle, {
          token,
          env: process.env.NEXT_PUBLIC_PADDLE_ENV,
          successUrl: `${window.location.origin}/checkout/success`,
        });
        setState({ kind: 'ready' });
      })
      .catch(() => {
        if (!cancelled) {
          setState({
            kind: 'error',
            message: 'Could not load the payment form. Check your connection and reload the page.',
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-6 text-center">
      <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl">
        <CalendarRange className="size-6" />
      </div>
      {state.kind === 'no-transaction' && (
        <>
          <h1 className="mt-6 text-2xl font-semibold tracking-tight">Nothing to pay</h1>
          <p className="text-muted-foreground mt-2 max-w-sm text-sm">
            This page opens a checkout started from the app. Start one from Settings → Billing.
          </p>
          <Button asChild className="mt-6">
            <Link href="/mail">Back to Calendium</Link>
          </Button>
        </>
      )}
      {(state.kind === 'loading' || state.kind === 'ready') && (
        <p className="text-muted-foreground mt-6 flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Opening secure checkout…
        </p>
      )}
      {state.kind === 'error' && (
        <p role="alert" className="text-destructive mt-6 max-w-sm text-sm">
          {state.message}
        </p>
      )}
    </main>
  );
}
