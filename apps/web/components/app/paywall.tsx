'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import { Lock, LogOut, X } from 'lucide-react';
import { toast } from 'sonner';

import type { PaymentRequiredReason, Subscription } from '@calendium/shared';
import { ApiRequestError, subscriptionDenialReason, TRIAL_BANNER_DAYS, trialDaysLeft } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { onPaymentRequired } from '@/lib/api';
import { fetchSubscription, openBillingPortal, startCheckout } from '@/lib/settings-data';
import { performSignOut } from '@/lib/sign-out';
import { useInstance } from '@/lib/use-instance';
import { cn } from '@/lib/utils';

/**
 * Billing UI (docs/payments.md — Spotify model on Paddle). The web app is
 * the only purchase surface: checkout is a Paddle overlay on /checkout and
 * cancel/update-card happen in Paddle's portal; this module owns every
 * redirect into those.
 */

export type PortalTarget = 'overview' | 'cancel' | 'updatePayment';

/** Starts a checkout; a 409 already_subscribed falls through to the portal. */
export function useCheckoutMutation() {
  return useMutation({
    mutationFn: async () => {
      try {
        const { url } = await startCheckout();
        return url;
      } catch (err) {
        if (err instanceof ApiRequestError && err.code === 'already_subscribed') {
          const urls = await openBillingPortal();
          return urls.overviewUrl;
        }
        throw err;
      }
    },
    onSuccess: (url) => window.location.assign(url),
    onError: () => toast.error('Could not start checkout — the billing service is unavailable.'),
  });
}

/** Opens one of the portal links, falling back to the overview when that link is empty. */
export function useBillingPortalMutation(target: PortalTarget = 'overview') {
  return useMutation({
    mutationFn: async () => {
      const urls = await openBillingPortal();
      const picked =
        target === 'cancel' ? urls.cancelUrl : target === 'updatePayment' ? urls.updatePaymentUrl : urls.overviewUrl;
      return picked || urls.overviewUrl;
    },
    onSuccess: (url) => window.location.assign(url),
    onError: () => toast.error('Could not open billing — the billing service is unavailable.'),
  });
}

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: {
    title: 'Your free trial has ended',
    body: 'Subscribe to keep using Calendium on web, desktop, and mobile. Your mail keeps syncing in the meantime.',
  },
  none: {
    title: 'Subscribe to keep using Calendium',
    body: 'Calendium is $50/year — one plan for email + calendar on every platform.',
  },
  canceled: {
    title: 'Your subscription has ended',
    body: 'Resubscribe any time to pick up right where you left off.',
  },
  past_due: {
    title: 'Payment failed',
    body: 'We could not charge your card and the grace period has passed. Update your payment method to restore access.',
  },
  paused: {
    title: 'Your subscription is paused',
    body: 'Update your payment method or resume the plan from billing to restore access.',
  },
};

/** Full-pane paywall rendered by the (app) layout in place of the page. */
export function PaywallScreen({ reason, className }: { reason: PaymentRequiredReason; className?: string }) {
  const router = useRouter();
  const checkout = useCheckoutMutation();
  const updatePayment = useBillingPortalMutation('updatePayment');
  const manage = useBillingPortalMutation('overview');
  const needsPayment = reason === 'past_due' || reason === 'paused';
  const copy = COPY[reason];
  // The paywall can replace the whole shell mid-session; move focus to its
  // heading so it is announced instead of focus falling back to <body>.
  const headingRef = React.useRef<HTMLHeadingElement>(null);
  React.useEffect(() => {
    headingRef.current?.focus();
  }, []);

  return (
    <section aria-label="Subscription required" className={cn('flex h-full items-center justify-center p-6', className)}>
      <div className="flex w-full max-w-md flex-col items-center gap-4 rounded-lg border p-8 text-center">
        <div className="bg-background flex size-10 items-center justify-center rounded-md border">
          <Lock className="size-5" />
        </div>
        <h1 ref={headingRef} tabIndex={-1} className="text-lg font-semibold outline-none">
          {copy.title}
        </h1>
        <p aria-live="polite" className="text-muted-foreground text-sm">
          {copy.body}
        </p>
        {needsPayment ? (
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button onClick={() => updatePayment.mutate()} disabled={updatePayment.isPending}>
              Update payment method
            </Button>
            <Button variant="outline" onClick={() => manage.mutate()} disabled={manage.isPending}>
              Manage billing
            </Button>
          </div>
        ) : (
          <Button onClick={() => checkout.mutate()} disabled={checkout.isPending}>
            Subscribe · $50/year
          </Button>
        )}
        <Button
          variant="ghost"
          size="sm"
          onClick={async () => {
            await performSignOut();
            router.replace('/signin');
          }}
        >
          <LogOut /> Sign out
        </Button>
      </div>
    </section>
  );
}

const TRIAL_BANNER_KEY = 'calendium.trial-banner.dismissed';
const todayKey = () => format(new Date(), 'yyyy-MM-dd'); // local day, resets at local midnight

/** Last-3-days trial reminder, dismissible once per calendar day. */
export function TrialBanner({ subscription }: { subscription: Subscription | null | undefined }) {
  const checkout = useCheckoutMutation();
  const [dismissedDay, setDismissedDay] = React.useState<string | null>(() => {
    try {
      return window.localStorage.getItem(TRIAL_BANNER_KEY);
    } catch {
      return null;
    }
  });
  if (!subscription) return null;
  const days = trialDaysLeft(subscription);
  if (days === null || days > TRIAL_BANNER_DAYS || dismissedDay === todayKey()) return null;
  const when = days <= 0 ? 'today' : days === 1 ? 'in 1 day' : `in ${days} days`;

  return (
    <div role="status" className="bg-muted text-muted-foreground flex shrink-0 items-center gap-3 border-b px-4 py-1.5 text-xs">
      <span className="flex-1">
        <span className="text-foreground font-medium">Your free trial ends {when}.</span> Subscribe to keep Calendium on
        every device.
      </span>
      <Button size="sm" variant="outline" onClick={() => checkout.mutate()} disabled={checkout.isPending}>
        Subscribe · $50/year
      </Button>
      <button
        type="button"
        aria-label="Dismiss for today"
        className="hover:text-foreground"
        onClick={() => {
          const key = todayKey();
          try {
            window.localStorage.setItem(TRIAL_BANNER_KEY, key);
          } catch {
            // Best effort.
          }
          setDismissedDay(key);
        }}
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

/** The granted subscription, published by BillingGate for BillingTrialBanner. */
const GrantedSubscriptionContext = React.createContext<Subscription | null>(null);

/** TrialBanner fed by the enclosing BillingGate; the shell places it above the page. */
export function BillingTrialBanner() {
  return <TrialBanner subscription={React.useContext(GrantedSubscriptionContext)} />;
}

/**
 * Gate for the WHOLE authenticated shell (rail, compose, palette, shortcuts,
 * Ask sidebar): when the server bills, fetch the subscription (which also
 * grants the signup trial server-side) before rendering the shell; deny →
 * a full-screen PaywallScreen and nothing else; grant → the shell, with the
 * subscription published for BillingTrialBanner. Fails OPEN when discovery
 * errors or no subscription was ever loaded — an unreachable billing API
 * must never lock a paying user out — but a denial already observed from a
 * successful fetch survives a failed background refetch.
 */
export function BillingGate({ children }: { children: React.ReactNode }) {
  const instance = useInstance();
  const billing = !!instance.data?.features.billing;
  const subscription = useQuery({
    queryKey: ['subscription'],
    queryFn: fetchSubscription,
    enabled: billing,
    retry: 1,
    retryDelay: 300, // short: the gate holds the whole shell's first paint
    staleTime: 60_000,
    refetchInterval: 5 * 60_000,
  });

  // Any 402 from the API client means the server now denies access (e.g. the
  // trial ended mid-session): re-evaluate right away instead of waiting for
  // the poll. cancelRefetch:false joins an in-flight fetch rather than
  // restarting it, so a burst of 402s (or a 402 from the subscription
  // endpoint itself) can never loop.
  const queryClient = useQueryClient();
  React.useEffect(
    () =>
      onPaymentRequired(() => {
        void queryClient.invalidateQueries({ queryKey: ['subscription'] }, { cancelRefetch: false });
      }),
    [queryClient]
  );

  if (instance.isPending || (billing && subscription.isPending)) {
    return (
      <div role="status" aria-label="Loading" className="flex h-svh items-center justify-center">
        <div className="bg-primary size-8 animate-pulse rounded-lg" />
      </div>
    );
  }
  // Fail open only when no subscription was ever loaded: after a failed
  // background refetch React Query keeps the last good `data`, so an
  // observed denial keeps the paywall (and an observed grant keeps the app).
  const granted = billing && subscription.data ? subscription.data : null;
  const reason = granted ? subscriptionDenialReason(granted) : null;
  if (reason) return <PaywallScreen reason={reason} className="h-svh" />;
  // Always the same element type around the shell, so a fail-open → granted
  // transition never remounts it.
  return <GrantedSubscriptionContext.Provider value={granted}>{children}</GrantedSubscriptionContext.Provider>;
}
