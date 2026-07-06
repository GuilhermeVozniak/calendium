import type { SubscriptionStatus } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { format } from 'date-fns';
import { CreditCard, ExternalLink, Loader2, LogOut, Mail, RefreshCw, Server } from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';

import { api, apiConfigured, CHECKOUT_SUCCESS_URL, orMock, PRICING_URL } from '@/lib/api';
import { clearStoredToken, signOut } from '@/lib/auth';
import { mockAccounts, mockSubscription, mockUser, startMockCheckout } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import { desktop, isDesktop } from '@/lib/wails';
import { Badge } from '@/ui/badge';
import { Button } from '@/ui/button';

const STATUS_BADGE: Record<SubscriptionStatus, { label: string; variant: 'default' | 'secondary' | 'destructive' | 'outline' }> = {
  active: { label: 'Active', variant: 'default' },
  trialing: { label: 'Trial', variant: 'secondary' },
  past_due: { label: 'Past due', variant: 'destructive' },
  canceled: { label: 'Canceled', variant: 'outline' },
  expired: { label: 'Expired', variant: 'outline' },
  none: { label: 'No subscription', variant: 'outline' },
};

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {title}
      </h2>
      <div className="rounded-lg border bg-card shadow-sm">{children}</div>
    </section>
  );
}

export function SettingsView() {
  // Spotify desktop flow: after opening checkout in the system browser we
  // poll GET /v1/billing/subscription until the Stripe webhook lands.
  const [awaitingCheckout, setAwaitingCheckout] = useState(false);

  // Open-core: billing is disabled on self-hosted instances.
  const { config, clear: clearServer } = useServerConfig();
  const isSelfHost = config?.mode === 'self_host';

  const { data: user } = useQuery({
    queryKey: ['me'],
    queryFn: () =>
      orMock(
        () => api.getMe(),
        () => mockUser
      ),
  });
  const { data: accounts = [] } = useQuery({
    queryKey: ['accounts'],
    queryFn: () =>
      orMock(
        () => api.listAccounts(),
        () => mockAccounts
      ),
  });
  const { data: subscription } = useQuery({
    queryKey: ['subscription'],
    queryFn: () =>
      orMock(
        () => api.getSubscription(),
        () => mockSubscription()
      ),
    refetchInterval: awaitingCheckout ? 3_000 : false,
    // Self-hosted instances have billing disabled — no subscription to fetch.
    enabled: !isSelfHost,
  });
  const { data: version } = useQuery({
    queryKey: ['app-version'],
    queryFn: () => desktop.GetAppVersion(),
  });

  const subscribed = subscription?.status === 'active' || subscription?.status === 'trialing';

  useEffect(() => {
    if (awaitingCheckout && subscribed) setAwaitingCheckout(false);
  }, [awaitingCheckout, subscribed]);

  async function subscribeOnTheWeb() {
    // Never bill in-app (docs/payments.md): create the Stripe Checkout
    // session, open it in the default browser, then poll for the webhook.
    let url = PRICING_URL;
    try {
      if (apiConfigured()) {
        const session = await api.createCheckoutSession(CHECKOUT_SUCCESS_URL, PRICING_URL);
        url = session.url;
      } else {
        startMockCheckout(); // standalone demo: mock flips to active in ~8s
      }
    } catch {
      // Checkout session failed — fall back to the public pricing page.
    }
    setAwaitingCheckout(true);
    void desktop.OpenExternal(url);
  }

  async function manageBilling() {
    let url = PRICING_URL;
    try {
      if (apiConfigured()) {
        const session = await api.createBillingPortalSession(PRICING_URL);
        url = session.url;
      }
    } catch {
      // Fall back to the public pricing page.
    }
    void desktop.OpenExternal(url);
  }

  const badge = subscription ? STATUS_BADGE[subscription.status] : null;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-xl flex-col gap-5 p-5">
        <h1 className="text-base font-semibold">Settings</h1>

        <Section title="Account">
          <div className="flex items-center gap-3 p-3">
            <div className="flex size-9 select-none items-center justify-center rounded-full bg-secondary text-sm font-semibold text-secondary-foreground">
              {(user?.name ?? user?.email ?? '?').slice(0, 1).toUpperCase()}
            </div>
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{user?.name ?? 'Signed out'}</div>
              <div className="truncate text-xs text-muted-foreground">{user?.email ?? '—'}</div>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="ml-auto"
              onClick={() => void signOut()}
            >
              <LogOut /> Sign out
            </Button>
          </div>
        </Section>

        <Section title="Connected accounts">
          <ul className="divide-y">
            {accounts.map((account) => (
              <li key={account.id} className="flex items-center gap-2 p-3">
                <Mail className="size-4 text-muted-foreground" />
                <span className="truncate text-sm">{account.email}</span>
                <Badge variant="outline" className="capitalize">
                  {account.provider}
                </Badge>
                <Badge
                  variant={account.status === 'active' ? 'secondary' : 'outline'}
                  className="ml-auto capitalize"
                >
                  {account.status.replace('_', ' ')}
                </Badge>
              </li>
            ))}
          </ul>
        </Section>

        <Section title="Server">
          <div className="flex flex-col gap-3 p-3">
            <div className="flex items-center gap-3">
              <Server className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{config?.name ?? 'Calendium'}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {config?.serverUrl ?? '—'}
                </div>
              </div>
              <Badge variant="secondary">{isSelfHost ? 'Self-hosted' : 'Cloud'}</Badge>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={() => {
                // Drop the session token too — it's scoped to the old server.
                clearStoredToken();
                clearServer();
              }}
            >
              <RefreshCw /> Switch server
            </Button>
          </div>
        </Section>

        {isSelfHost ? (
          <Section title="Plan">
            <div className="flex flex-col gap-1 p-3">
              <div className="flex items-center gap-2">
                <CreditCard className="size-4 text-muted-foreground" />
                <span className="text-sm font-medium">Self-hosted — all features included</span>
              </div>
              <p className="text-xs text-muted-foreground">
                This instance runs Calendium open-source. There is no subscription to manage.
              </p>
            </div>
          </Section>
        ) : (
          <Section title="Billing">
            <div className="flex flex-col gap-3 p-3">
              <div className="flex items-center gap-2">
                <CreditCard className="size-4 text-muted-foreground" />
                <span className="text-sm font-medium">Calendium Annual — $50/year</span>
              {badge && <Badge variant={badge.variant}>{badge.label}</Badge>}
            </div>
            <p className="text-xs text-muted-foreground">
              One subscription unlocks every platform. Billing always happens on the web via
              Stripe — there are no in-app purchases.
            </p>
            {subscription?.trialEndsAt && subscription.status === 'trialing' && (
              <p className="text-xs text-muted-foreground">
                Trial ends {format(new Date(subscription.trialEndsAt), 'MMM d, yyyy')}.
              </p>
            )}
            {subscription?.currentPeriodEnd && subscription.status === 'active' && (
              <p className="text-xs text-muted-foreground">
                {subscription.cancelAtPeriodEnd ? 'Access until' : 'Renews'}{' '}
                {format(new Date(subscription.currentPeriodEnd), 'MMM d, yyyy')}.
              </p>
            )}
            <div className="flex items-center gap-2">
              {subscribed ? (
                <Button variant="outline" size="sm" onClick={manageBilling}>
                  <ExternalLink /> Manage billing on the web
                </Button>
              ) : awaitingCheckout ? (
                <>
                  <Button size="sm" disabled>
                    <Loader2 className="animate-spin" /> Waiting for confirmation…
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setAwaitingCheckout(false)}>
                    Cancel
                  </Button>
                </>
              ) : (
                <Button size="sm" onClick={subscribeOnTheWeb}>
                  <ExternalLink /> Subscribe on the web
                </Button>
              )}
            </div>
            {awaitingCheckout && !subscribed && (
              <p className="text-xs text-muted-foreground">
                Complete checkout in your browser — this app unlocks automatically once Stripe
                confirms the payment.
              </p>
            )}
          </div>
          </Section>
        )}

        <Section title="About">
          <div className="flex flex-col gap-1 p-3 text-xs text-muted-foreground">
            <div className="flex justify-between">
              <span>Version</span>
              <span className="tabular-nums text-foreground">{version ?? '…'}</span>
            </div>
            <div className="flex justify-between">
              <span>Shell</span>
              <span className="text-foreground">{isDesktop ? 'Wails (desktop)' : 'Browser (dev)'}</span>
            </div>
            <div className="flex justify-between">
              <span>Data source</span>
              <span className="text-foreground">{apiConfigured() ? 'Calendium API' : 'Mock data'}</span>
            </div>
          </div>
        </Section>
      </div>
    </div>
  );
}
