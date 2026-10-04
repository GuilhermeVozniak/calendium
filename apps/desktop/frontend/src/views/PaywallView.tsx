import type { PaymentRequiredReason } from '@calendium/shared';
import { ExternalLink, Lock, LogOut, UserCog } from 'lucide-react';
import { useState } from 'react';

import { signOut } from '@/lib/auth';
import { clearOfflineState } from '@/lib/offline';
import { billingWebOrigin, useServerConfig } from '@/lib/server-config';
import { desktop } from '@/lib/wails';
import { Button } from '@/ui/button';
import { AccountControls } from '@/views/AccountControls';

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: { title: 'Your free trial has ended', body: 'Subscribe in your browser to keep using Calendium on every device.' },
  none: { title: 'Subscribe to keep using Calendium', body: 'Calendium is $50/year — one plan for email + calendar everywhere.' },
  canceled: { title: 'Your subscription has ended', body: 'Resubscribe in your browser any time to pick up where you left off.' },
  past_due: { title: 'Payment failed', body: 'Update your payment method in your browser to restore access.' },
  paused: { title: 'Your subscription is paused', body: 'Resume the plan or update your payment method in your browser.' },
};

/**
 * Read-only paywall (docs/payments.md): billing always happens on the web.
 * "Account & data" keeps export and account deletion reachable without an
 * active subscription.
 */
export function PaywallView({ reason }: { reason: PaymentRequiredReason }) {
  const { config, demoMode, exitDemo } = useServerConfig();
  const [showAccount, setShowAccount] = useState(false);
  const copy = COPY[reason];
  const origin = billingWebOrigin(config);

  return (
    <section aria-label="Subscription required" className="flex h-full items-center justify-center p-6">
      <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-lg border p-8 text-center">
        <Lock className="size-6 text-muted-foreground" />
        <h1 className="text-lg font-semibold">{copy.title}</h1>
        <p className="text-sm text-muted-foreground">{copy.body}</p>
        <Button onClick={() => origin && desktop.OpenExternal(`${origin}/settings?tab=billing`)} disabled={!origin}>
          <ExternalLink /> Open billing in your browser
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            if (demoMode) exitDemo();
            else void signOut().finally(() => void clearOfflineState());
          }}
        >
          <LogOut /> Sign out
        </Button>
        {showAccount ? (
          <div className="w-full rounded-lg border text-left">
            <div className="p-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Account &amp; data
            </div>
            <AccountControls />
          </div>
        ) : (
          <Button variant="ghost" size="sm" onClick={() => setShowAccount(true)}>
            <UserCog /> Account &amp; data
          </Button>
        )}
      </div>
    </section>
  );
}
