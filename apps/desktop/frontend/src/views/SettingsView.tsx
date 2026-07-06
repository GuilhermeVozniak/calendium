import type { Provider, SubscriptionStatus } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import {
  CreditCard,
  ExternalLink,
  Loader2,
  LogOut,
  Mail,
  Plus,
  RefreshCw,
  Server,
} from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';

import { api, apiConfigured, orMock } from '@/lib/api';
import { clearStoredToken, signOut } from '@/lib/auth';
import { mockAccounts, mockSubscription, mockUser } from '@/lib/mock';
import { useServerConfig, webOrigin } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import { desktop, isDesktop, onDeepLink } from '@/lib/wails';
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
  const queryClient = useQueryClient();
  const { config, clear: clearServer, demoMode, exitDemo } = useServerConfig();

  // Capabilities gate the UI (contract item 12): billing only when the server
  // advertises it, mailbox-connect only for enabled providers.
  const billingEnabled = config?.features?.billing ?? false;
  const [connecting, setConnecting] = useState<Provider | null>(null);

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
    // Servers without billing (self-host) advertise no subscription to fetch.
    enabled: billingEnabled,
  });
  const { data: version } = useQuery({
    queryKey: ['app-version'],
    queryFn: () => desktop.GetAppVersion(),
  });

  // Mailbox OAuth returns via a calendium://accounts/connected deep link
  // (backend callback 302 -> client). Refresh the account list on return.
  useEffect(
    () =>
      onDeepLink((url) => {
        if (!url.startsWith('calendium://accounts')) return;
        let status = '';
        try {
          status = new URL(url).searchParams.get('status') ?? '';
        } catch {
          // Ignore malformed deep links.
        }
        if (status === 'error') {
          toast({
            title: 'Connection failed',
            description: 'The mailbox was not connected.',
            variant: 'destructive',
          });
        } else {
          toast({ title: 'Mailbox connected' });
        }
        void queryClient.invalidateQueries({ queryKey: ['accounts'] });
      }),
    [queryClient]
  );

  async function connect(provider: Provider) {
    if (demoMode) {
      toast({ title: 'Demo mode', description: 'Connect a mailbox from a real server.' });
      return;
    }
    setConnecting(provider);
    try {
      const { url } = await api.connectAccount(provider, 'calendium://accounts/connected');
      desktop.OpenExternal(url);
      toast({ title: 'Continue in your browser', description: 'Authorize access, then return to Calendium.' });
    } catch (e) {
      toast({ title: 'Could not start connect', description: errorMessage(e), variant: 'destructive' });
    } finally {
      setConnecting(null);
    }
  }

  async function disconnect(id: string) {
    if (demoMode) {
      toast({ title: 'Demo mode', description: 'This mailbox is part of the demo.' });
      return;
    }
    try {
      await api.disconnectAccount(id);
      toast({ title: 'Account disconnected' });
      void queryClient.invalidateQueries({ queryKey: ['accounts'] });
    } catch (e) {
      toast({ title: 'Could not disconnect', description: errorMessage(e), variant: 'destructive' });
    }
  }

  // Never bill in-app (docs/payments.md): hand billing to the web app.
  function openBilling() {
    if (demoMode) {
      toast({ title: 'Demo mode', description: 'Billing happens on the web in a real workspace.' });
      return;
    }
    const origin = webOrigin(config);
    if (!origin) {
      toast({ title: 'Billing unavailable', description: 'No web URL for this server.', variant: 'destructive' });
      return;
    }
    desktop.OpenExternal(`${origin}/settings?tab=billing`);
  }

  function handleSignOut() {
    if (demoMode) exitDemo();
    else void signOut();
  }

  function handleSwitchServer() {
    if (demoMode) {
      exitDemo();
      return;
    }
    // Drop the session token too — it's scoped to the old server.
    clearStoredToken();
    clearServer();
  }

  const subscribed = subscription?.status === 'active' || subscription?.status === 'trialing';
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
            <Button variant="outline" size="sm" className="ml-auto" onClick={handleSignOut}>
              <LogOut /> {demoMode ? 'Exit demo' : 'Sign out'}
            </Button>
          </div>
        </Section>

        <Section title="Connected accounts">
          {accounts.length > 0 && (
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
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    onClick={() => void disconnect(account.id)}
                  >
                    Disconnect
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <div className="flex flex-wrap items-center gap-2 border-t p-3">
            {config?.features?.google && (
              <Button
                variant="outline"
                size="sm"
                disabled={connecting !== null}
                onClick={() => void connect('google')}
              >
                {connecting === 'google' ? <Loader2 className="animate-spin" /> : <Plus />} Connect Google
              </Button>
            )}
            {config?.features?.microsoft && (
              <Button
                variant="outline"
                size="sm"
                disabled={connecting !== null}
                onClick={() => void connect('microsoft')}
              >
                {connecting === 'microsoft' ? <Loader2 className="animate-spin" /> : <Plus />} Connect
                Microsoft
              </Button>
            )}
            {!config?.features?.google && !config?.features?.microsoft && (
              <p className="text-xs text-muted-foreground">
                No mailbox providers are enabled on this server.
              </p>
            )}
          </div>
        </Section>

        <Section title="Server">
          <div className="flex flex-col gap-3 p-3">
            <div className="flex items-center gap-3">
              <Server className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{config?.name ?? 'Calendium'}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {demoMode ? 'Demo — no server' : config?.serverUrl ?? '—'}
                </div>
              </div>
              <Badge variant="secondary">
                {demoMode ? 'Demo' : config?.mode === 'self_host' ? 'Self-hosted' : 'Cloud'}
              </Badge>
            </div>
            <Button variant="outline" size="sm" className="self-start" onClick={handleSwitchServer}>
              <RefreshCw /> {demoMode ? 'Leave demo' : 'Switch server'}
            </Button>
          </div>
        </Section>

        {billingEnabled ? (
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
              <Button variant="outline" size="sm" className="self-start" onClick={openBilling}>
                <ExternalLink /> {subscribed ? 'Manage billing on the web' : 'Subscribe on the web'}
              </Button>
            </div>
          </Section>
        ) : (
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
              <span className="text-foreground">
                {demoMode ? 'Demo data' : apiConfigured() ? 'Calendium API' : 'Mock data'}
              </span>
            </div>
          </div>
        </Section>
      </div>
    </div>
  );
}
