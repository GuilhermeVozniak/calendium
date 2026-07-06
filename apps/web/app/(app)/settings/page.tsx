'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { format, formatDistanceToNow } from 'date-fns';
import { CreditCard, ExternalLink, Monitor, Moon, Plus, Sun, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import type {
  AccountStatus,
  ConnectedAccount,
  Provider,
  Snippet,
  Subscription,
} from '@calendium/shared';

import {
  PaywallBanner,
  useBillingPortalMutation,
  useCheckoutMutation,
} from '@/components/app/paywall';
import { useTheme } from '@/components/theme-provider';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import {
  createSnippetApi,
  deleteSnippetApi,
  disconnectAccountApi,
  fetchAccounts,
  fetchSnippets,
  fetchSubscription,
  startConnect,
} from '@/lib/settings-data';
import { cn } from '@/lib/utils';

type SettingsTab = 'accounts' | 'snippets' | 'appearance' | 'billing';

const TABS: SettingsTab[] = ['accounts', 'snippets', 'appearance', 'billing'];

export default function SettingsPage() {
  const [tab, setTab] = React.useState<SettingsTab>('accounts');
  const subscriptionQuery = useQuery({ queryKey: ['subscription'], queryFn: fetchSubscription });

  // Deep-link section via #hash, and surface Stripe Checkout redirect results.
  React.useEffect(() => {
    const hash = window.location.hash.replace('#', '');
    if ((TABS as string[]).includes(hash)) setTab(hash as SettingsTab);
    const params = new URLSearchParams(window.location.search);
    const checkout = params.get('checkout');
    if (checkout === 'success') {
      toast.success('Subscription active - welcome to Calendium!');
      setTab('billing');
    } else if (checkout === 'canceled') {
      toast.info('Checkout canceled - you can subscribe any time.');
      setTab('billing');
    }
    if (checkout) window.history.replaceState(null, '', window.location.pathname);
  }, []);

  const changeTab = (value: string) => {
    setTab(value as SettingsTab);
    window.history.replaceState(null, '', `#${value}`);
  };

  return (
    <div className="h-full flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl px-6 py-8">
        <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Accounts, snippets, appearance, and billing.
        </p>

        <PaywallBanner subscription={subscriptionQuery.data} className="mt-4" />

        <Tabs value={tab} onValueChange={changeTab} className="mt-6">
          <TabsList>
            <TabsTrigger value="accounts">Accounts</TabsTrigger>
            <TabsTrigger value="snippets">Snippets</TabsTrigger>
            <TabsTrigger value="appearance">Appearance</TabsTrigger>
            <TabsTrigger value="billing">Billing</TabsTrigger>
          </TabsList>
          <TabsContent value="accounts" className="mt-4">
            <AccountsSection />
          </TabsContent>
          <TabsContent value="snippets" className="mt-4">
            <SnippetsSection />
          </TabsContent>
          <TabsContent value="appearance" className="mt-4">
            <AppearanceSection />
          </TabsContent>
          <TabsContent value="billing" className="mt-4">
            <BillingSection
              subscription={subscriptionQuery.data}
              loading={subscriptionQuery.isLoading}
            />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

const STATUS_META: Record<AccountStatus, { label: string; dot: string }> = {
  active: { label: 'Connected', dot: 'bg-emerald-500' },
  syncing: { label: 'Syncing', dot: 'bg-sky-500' },
  reauth_required: { label: 'Re-auth required', dot: 'bg-amber-500' },
  disconnected: { label: 'Disconnected', dot: 'bg-muted-foreground' },
};

function AccountsSection() {
  const queryClient = useQueryClient();
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  const connect = useMutation({
    mutationFn: (provider: Provider) =>
      startConnect(provider, `${window.location.origin}/settings`),
    onSuccess: ({ url }) => window.location.assign(url),
    onError: () => toast.error('Could not start the connect flow - is the API server running?'),
  });

  const disconnect = useMutation({
    mutationFn: disconnectAccountApi,
    onSuccess: (_data, id) => {
      queryClient.setQueryData<ConnectedAccount[]>(['accounts'], (prev) =>
        prev?.filter((a) => a.id !== id)
      );
      toast.success('Account disconnected');
    },
    onError: () => toast.error('Could not disconnect the account'),
  });

  const accounts = accountsQuery.data ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connected accounts</CardTitle>
        <CardDescription>
          Google and Microsoft accounts that provide your mail and calendars.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {accountsQuery.isLoading && (
          <>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </>
        )}
        {!accountsQuery.isLoading && accounts.length === 0 && (
          <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
            No accounts connected yet. Connect one below to start syncing.
          </p>
        )}
        {accounts.map((account) => {
          const meta = STATUS_META[account.status];
          return (
            <div key={account.id} className="flex items-center gap-3 rounded-lg border p-3">
              <div className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-sm font-bold">
                {account.provider === 'google' ? 'G' : 'M'}
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium">{account.email}</span>
                  <Badge variant="outline" className="gap-1.5 font-normal">
                    <span className={cn('size-1.5 rounded-full', meta.dot)} />
                    {meta.label}
                  </Badge>
                </div>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {account.provider === 'google' ? 'Google' : 'Microsoft'}
                  {account.lastSyncedAt
                    ? ` · synced ${formatDistanceToNow(new Date(account.lastSyncedAt), { addSuffix: true })}`
                    : ' · never synced'}
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground hover:text-destructive"
                onClick={() => disconnect.mutate(account.id)}
                disabled={disconnect.isPending}
              >
                Disconnect
              </Button>
            </div>
          );
        })}
      </CardContent>
      <CardFooter className="gap-2 border-t pt-6">
        <Button
          variant="outline"
          onClick={() => connect.mutate('google')}
          disabled={connect.isPending}
        >
          <Plus />
          Connect Google
        </Button>
        <Button
          variant="outline"
          onClick={() => connect.mutate('microsoft')}
          disabled={connect.isPending}
        >
          <Plus />
          Connect Microsoft
        </Button>
      </CardFooter>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Snippets
// ---------------------------------------------------------------------------

function textToHtml(text: string): string {
  const escaped = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return escaped
    .split(/\n{2,}/)
    .map((paragraph) => `<p>${paragraph.replace(/\n/g, '<br />')}</p>`)
    .join('');
}

function htmlToText(html: string): string {
  return html
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/p>\s*<p>/gi, '\n\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .trim();
}

function SnippetsSection() {
  const queryClient = useQueryClient();
  const snippetsQuery = useQuery({ queryKey: ['snippets'], queryFn: fetchSnippets });

  const [createOpen, setCreateOpen] = React.useState(false);
  const [name, setName] = React.useState('');
  const [shortcut, setShortcut] = React.useState('');
  const [body, setBody] = React.useState('');

  const create = useMutation({
    mutationFn: () =>
      createSnippetApi({
        name: name.trim(),
        shortcut: shortcut.trim() || null,
        bodyHtml: textToHtml(body.trim()),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['snippets'] });
      toast.success('Snippet created');
      setCreateOpen(false);
      setName('');
      setShortcut('');
      setBody('');
    },
    onError: () => toast.error('Could not create the snippet'),
  });

  const remove = useMutation({
    mutationFn: deleteSnippetApi,
    onSuccess: (_data, id) => {
      queryClient.setQueryData<Snippet[]>(['snippets'], (prev) =>
        prev?.filter((s) => s.id !== id)
      );
      toast.success('Snippet deleted');
    },
    onError: () => toast.error('Could not delete the snippet'),
  });

  const snippets = snippetsQuery.data ?? [];

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Snippets</CardTitle>
          <CardDescription>
            Reusable canned responses - type the shortcut in the composer to expand.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus />
              New snippet
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {snippetsQuery.isLoading && (
            <>
              <Skeleton className="h-14 w-full" />
              <Skeleton className="h-14 w-full" />
            </>
          )}
          {!snippetsQuery.isLoading && snippets.length === 0 && (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              No snippets yet. Create one to reply faster.
            </p>
          )}
          {snippets.map((snippet) => (
            <div key={snippet.id} className="flex items-center gap-3 rounded-lg border p-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{snippet.name}</span>
                  {snippet.shortcut && <Kbd className="normal-case">{snippet.shortcut}</Kbd>}
                </div>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  {htmlToText(snippet.bodyHtml)}
                </p>
              </div>
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                used {snippet.usageCount}x
              </span>
              <Button
                variant="ghost"
                size="icon"
                className="size-8 text-muted-foreground hover:text-destructive"
                onClick={() => remove.mutate(snippet.id)}
                disabled={remove.isPending}
                aria-label={`Delete snippet ${snippet.name}`}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </CardContent>
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>New snippet</DialogTitle>
            <DialogDescription>
              Give it a short shortcut like ";intro" to expand it while composing.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="snippet-name">Name</Label>
              <Input
                id="snippet-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Intro"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="snippet-shortcut">Shortcut (optional)</Label>
              <Input
                id="snippet-shortcut"
                value={shortcut}
                onChange={(e) => setShortcut(e.target.value)}
                placeholder=";intro"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="snippet-body">Body</Label>
              <Textarea
                id="snippet-body"
                value={body}
                onChange={(e) => setBody(e.target.value)}
                rows={5}
                placeholder="Hi - great to meet you!"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => create.mutate()}
              disabled={!name.trim() || !body.trim() || create.isPending}
            >
              Create snippet
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// ---------------------------------------------------------------------------
// Appearance
// ---------------------------------------------------------------------------

const THEME_OPTIONS = [
  { value: 'light' as const, label: 'Light', description: 'Bright and crisp', icon: Sun },
  { value: 'dark' as const, label: 'Dark', description: 'Easy on the eyes', icon: Moon },
  { value: 'system' as const, label: 'System', description: 'Match your OS', icon: Monitor },
];

function AppearanceSection() {
  const { theme, setTheme } = useTheme();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription>Theme preference is stored per device.</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="grid gap-3 sm:grid-cols-3">
          {THEME_OPTIONS.map(({ value, label, description, icon: Icon }) => (
            <button
              key={value}
              type="button"
              onClick={() => setTheme(value)}
              aria-pressed={theme === value}
              className={cn(
                'flex flex-col items-start gap-1 rounded-lg border p-4 text-left transition-colors hover:bg-accent',
                theme === value && 'border-primary ring-1 ring-primary'
              )}
            >
              <Icon className="mb-1 size-4 text-muted-foreground" />
              <span className="text-sm font-medium">{label}</span>
              <span className="text-xs text-muted-foreground">{description}</span>
            </button>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Billing (docs/payments.md - $50/yr, Stripe only, Spotify model)
// ---------------------------------------------------------------------------

function BillingSection({
  subscription,
  loading,
}: {
  subscription: Subscription | undefined;
  loading: boolean;
}) {
  const checkout = useCheckoutMutation();
  const portal = useBillingPortalMutation();

  if (loading) return <Skeleton className="h-56 w-full" />;

  const sub = subscription;
  const periodEnd = sub?.currentPeriodEnd
    ? format(new Date(sub.currentPeriodEnd), 'MMMM d, yyyy')
    : null;
  const trialDaysLeft =
    sub?.status === 'trialing' && sub.trialEndsAt
      ? Math.max(0, Math.ceil((new Date(sub.trialEndsAt).getTime() - Date.now()) / 86_400_000))
      : null;

  const statusBadge = (() => {
    switch (sub?.status) {
      case 'trialing':
        return <Badge variant="secondary">Free trial</Badge>;
      case 'active':
        return (
          <Badge variant="outline" className="gap-1.5">
            <span className="size-1.5 rounded-full bg-emerald-500" />
            Active
          </Badge>
        );
      case 'past_due':
        return <Badge variant="destructive">Past due</Badge>;
      case 'canceled':
        return <Badge variant="outline">Canceled</Badge>;
      default:
        return <Badge variant="outline">No subscription</Badge>;
    }
  })();

  const showSubscribe =
    !sub || ['trialing', 'none', 'expired', 'canceled'].includes(sub.status);
  const showPortal = !!sub && ['active', 'past_due', 'canceled'].includes(sub.status);

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <CreditCard className="size-4" />
            Calendium Annual
          </CardTitle>
          <CardDescription>
            One plan - $50/year after a 14-day free trial. Unlocks web, desktop, and mobile.
          </CardDescription>
          <CardAction>{statusBadge}</CardAction>
        </CardHeader>
        <CardContent className="text-sm">
          {sub?.status === 'trialing' && (
            <p>
              Your free trial ends{' '}
              {sub.trialEndsAt ? format(new Date(sub.trialEndsAt), 'MMMM d, yyyy') : 'soon'}
              {trialDaysLeft !== null && (
                <span className="text-muted-foreground"> ({trialDaysLeft} days left)</span>
              )}
              . Subscribe now - billing only starts when the trial ends.
            </p>
          )}
          {sub?.status === 'active' && (
            <p>
              {sub.cancelAtPeriodEnd
                ? `Your plan is set to cancel - access ends ${periodEnd ?? 'at the end of the period'}.`
                : `Renews ${periodEnd ?? 'at the end of the period'} for $50.`}
            </p>
          )}
          {sub?.status === 'past_due' && (
            <p>
              Your last payment failed. Access continues during the 7-day grace period while
              Stripe retries - update your payment method to keep your account active.
            </p>
          )}
          {sub?.status === 'canceled' && (
            <p>
              Subscription canceled{periodEnd ? ` - access until ${periodEnd}` : ''}. Resubscribe
              any time.
            </p>
          )}
          {(!sub || sub.status === 'none' || sub.status === 'expired') && (
            <p>
              You do not have an active subscription. Subscribe to unlock the split inbox,
              calendar sync, AI compose, and more on every platform.
            </p>
          )}
        </CardContent>
        <CardFooter className="gap-2 border-t pt-6">
          {showSubscribe && (
            <Button onClick={() => checkout.mutate()} disabled={checkout.isPending}>
              Subscribe - $50/year
            </Button>
          )}
          {showPortal && (
            <Button
              variant={sub?.status === 'past_due' ? 'default' : 'outline'}
              onClick={() => portal.mutate()}
              disabled={portal.isPending}
            >
              {sub?.status === 'past_due' ? 'Update payment method' : 'Manage billing'}
              <ExternalLink />
            </Button>
          )}
        </CardFooter>
      </Card>
      <p className="text-xs text-muted-foreground">
        Billing runs through Stripe on the web for every platform (the Spotify model) - the
        mobile and desktop apps never charge you directly.
      </p>
    </div>
  );
}
