'use client';

import * as React from 'react';
import Link from 'next/link';
import { CalendarRange, CheckCircle2, Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { getApiClient } from '@/lib/api';
import { authClient } from '@/lib/auth-client';

type Status = 'polling' | 'active' | 'timeout';

/**
 * Paddle redirects here after a completed checkout. The webhook activates
 * the subscription asynchronously, so with a session we poll
 * GET /v1/billing/subscription every 2 s for up to 60 s.
 */
export function CheckoutSuccessClient({
  pollMs = 2000,
  maxAttempts = 30,
}: {
  pollMs?: number;
  maxAttempts?: number;
}) {
  const { data: session, isPending } = authClient.useSession();
  const [status, setStatus] = React.useState<Status>('polling');

  React.useEffect(() => {
    if (isPending || !session) return;
    let attempts = 0;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      attempts += 1;
      try {
        const sub = await getApiClient().getSubscription();
        if (sub.status === 'active') {
          if (!stopped) setStatus('active');
          return;
        }
      } catch {
        // Transient; keep polling.
      }
      if (stopped) return;
      if (attempts >= maxAttempts) {
        setStatus('timeout');
        return;
      }
      timer = setTimeout(tick, pollMs);
    };
    timer = setTimeout(tick, 0);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [isPending, session, pollMs, maxAttempts]);

  const signedOut = !isPending && !session;

  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-6 text-center">
      <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl">
        <CalendarRange className="size-6" />
      </div>
      <h1 className="mt-6 text-2xl font-semibold tracking-tight">
        {status === 'active' ? "You're all set" : 'Payment received'}
      </h1>
      {signedOut ? (
        <p className="text-muted-foreground mt-2 max-w-sm text-sm">
          Return to the app to continue — your subscription activates within a minute.
        </p>
      ) : status === 'active' ? (
        <p className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <CheckCircle2 className="size-4 text-emerald-500" /> Your Calendium Annual plan is active.
        </p>
      ) : status === 'timeout' ? (
        <p className="text-muted-foreground mt-2 max-w-sm text-sm">
          Activation is taking longer than usual. It will finish in the background — open the app and
          refresh in a minute.
        </p>
      ) : (
        <p className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Activating your subscription…
        </p>
      )}
      {(signedOut || status !== 'polling') && (
        <Button asChild className="mt-6">
          <Link href="/mail">Open Calendium</Link>
        </Button>
      )}
    </main>
  );
}
