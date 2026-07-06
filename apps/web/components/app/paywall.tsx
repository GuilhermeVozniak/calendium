'use client';

import { useMutation } from '@tanstack/react-query';
import { Lock } from 'lucide-react';
import { toast } from 'sonner';

import type { Subscription } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { getApiClient } from '@/lib/api';
import { cn } from '@/lib/utils';

/**
 * Billing helpers + paywall banner (docs/payments.md - Spotify model).
 * Stripe Checkout / Billing Portal always run on the web; this module is the
 * single place the web app starts those redirects from.
 */

export function useCheckoutMutation() {
  return useMutation({
    mutationFn: async () => {
      const origin = window.location.origin;
      const { url } = await getApiClient().createCheckoutSession(
        `${origin}/settings?checkout=success`,
        `${origin}/settings?checkout=canceled`
      );
      return url;
    },
    onSuccess: (url) => {
      window.location.assign(url);
    },
    onError: () => toast.error('Could not start checkout - the billing API is unreachable.'),
  });
}

export function useBillingPortalMutation() {
  return useMutation({
    mutationFn: async () => {
      const { url } = await getApiClient().createBillingPortalSession(
        `${window.location.origin}/settings`
      );
      return url;
    },
    onSuccess: (url) => {
      window.location.assign(url);
    },
    onError: () => toast.error('Could not open the billing portal - the billing API is unreachable.'),
  });
}

/**
 * Whether the subscription still grants product access.
 * `past_due` keeps access through Stripe's retry grace window; `canceled`
 * keeps access until the paid period ends.
 */
export function subscriptionHasAccess(subscription: Subscription | null | undefined): boolean {
  if (!subscription) return true; // unknown -> do not block
  switch (subscription.status) {
    case 'trialing':
    case 'active':
    case 'past_due':
      return true;
    case 'canceled':
      return subscription.currentPeriodEnd
        ? new Date(subscription.currentPeriodEnd).getTime() > Date.now()
        : false;
    default:
      return false;
  }
}

/**
 * Banner shown when the subscription lapsed (blocking) or a payment failed
 * (warning). Renders nothing while access is healthy, so it is safe to drop
 * anywhere: settings, mail list header, calendar header, etc.
 */
export function PaywallBanner({
  subscription,
  className,
}: {
  subscription: Subscription | null | undefined;
  className?: string;
}) {
  const checkout = useCheckoutMutation();
  const portal = useBillingPortalMutation();

  if (!subscription) return null;
  const pastDue = subscription.status === 'past_due';
  const lapsed = !subscriptionHasAccess(subscription);
  if (!lapsed && !pastDue) return null;

  const headline = pastDue
    ? 'Payment failed'
    : subscription.status === 'expired' || subscription.status === 'canceled'
      ? 'Your subscription has ended'
      : subscription.status === 'none' && subscription.trialEndsAt
        ? 'Your free trial has ended'
        : 'Subscribe to keep using Calendium';

  const detail = pastDue
    ? 'We could not charge your card. Access pauses after the 7-day grace period - update your payment method to stay active.'
    : 'Calendium is $50/year - one plan for email + calendar on web, desktop, and mobile.';

  return (
    <div
      role="status"
      className={cn(
        'flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center',
        pastDue ? 'border-destructive/40 bg-destructive/5' : 'bg-muted/50',
        className
      )}
    >
      <div
        className={cn(
          'flex size-9 shrink-0 items-center justify-center rounded-md',
          pastDue ? 'bg-destructive/10 text-destructive' : 'bg-background text-foreground border'
        )}
      >
        <Lock className="size-4" />
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{headline}</p>
        <p className="text-sm text-muted-foreground">{detail}</p>
      </div>
      {pastDue ? (
        <Button size="sm" onClick={() => portal.mutate()} disabled={portal.isPending}>
          Update payment method
        </Button>
      ) : (
        <Button size="sm" onClick={() => checkout.mutate()} disabled={checkout.isPending}>
          Subscribe - $50/year
        </Button>
      )}
    </div>
  );
}
