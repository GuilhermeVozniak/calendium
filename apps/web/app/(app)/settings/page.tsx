'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { format, formatDistanceToNow } from 'date-fns';
import {
  Bell,
  ChevronDown,
  ChevronUp,
  CreditCard,
  Crown,
  ExternalLink,
  Loader2,
  Monitor,
  Moon,
  Plus,
  Sun,
  Trash2,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import type {
  AccountStatus,
  ConnectedAccount,
  InboxSplit,
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
  setVipSendersApi,
  startConnect,
} from '@/lib/settings-data';
import { DEFAULT_SPLITS, orderSplits } from '@/lib/mail-utils';
import { usePrefs, useUpdatePrefs } from '@/lib/prefs-data';
import { useInstance } from '@/lib/use-instance';
import {
  disableWebPush,
  enableWebPush,
  getPushSubscription,
  isWebPushSupported,
} from '@/lib/web-push';
import { cn } from '@/lib/utils';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type SettingsTab = 'accounts' | 'snippets' | 'appearance' | 'mailbox' | 'notifications' | 'billing';

const KNOWN_TABS: SettingsTab[] = [
  'accounts',
  'snippets',
  'appearance',
  'mailbox',
  'notifications',
  'billing',
];

export default function SettingsPage() {
  const instance = useInstance();
  const billingEnabled = !!instance.data?.features.billing;
  const pushEnabled = !!instance.data?.features.push;
  const vapidPublicKey = instance.data?.vapidPublicKey;

  const availableTabs = React.useMemo<SettingsTab[]>(
    () => [
      'accounts',
      'snippets',
      'appearance',
      'mailbox',
      ...(pushEnabled ? (['notifications'] as SettingsTab[]) : []),
      ...(billingEnabled ? (['billing'] as SettingsTab[]) : []),
    ],
    [pushEnabled, billingEnabled]
  );

  const [tab, setTab] = React.useState<SettingsTab>('accounts');
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: fetchSubscription,
    enabled: billingEnabled,
  });

  // Deep-link section via ?tab= (primary) or #hash, and surface Stripe Checkout
  // redirect results. Validation against availability happens at render, so a
  // ?tab=billing that arrives before /v1/instance loads still lands correctly.
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const requested = params.get('tab') || window.location.hash.replace('#', '');
    if ((KNOWN_TABS as string[]).includes(requested)) setTab(requested as SettingsTab);
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

  // Fall back to Accounts when the requested tab isn't available on this server.
  const activeTab = availableTabs.includes(tab) ? tab : 'accounts';

  return (
    <div className="h-full flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl px-6 py-8">
        <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Accounts, snippets, appearance, mailbox{pushEnabled ? ', notifications' : ''}
          {billingEnabled ? ', and billing' : ''}.
        </p>

        {billingEnabled && (
          <PaywallBanner subscription={subscriptionQuery.data} className="mt-4" />
        )}

        <Tabs value={activeTab} onValueChange={changeTab} className="mt-6">
          <TabsList>
            <TabsTrigger value="accounts">Accounts</TabsTrigger>
            <TabsTrigger value="snippets">Snippets</TabsTrigger>
            <TabsTrigger value="appearance">Appearance</TabsTrigger>
            <TabsTrigger value="mailbox">Mailbox</TabsTrigger>
            {pushEnabled && <TabsTrigger value="notifications">Notifications</TabsTrigger>}
            {billingEnabled && <TabsTrigger value="billing">Billing</TabsTrigger>}
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
          <TabsContent value="mailbox" className="mt-4">
            <MailboxSection />
          </TabsContent>
          {pushEnabled && (
            <TabsContent value="notifications" className="mt-4">
              <NotificationsSection vapidPublicKey={vapidPublicKey} />
            </TabsContent>
          )}
          {billingEnabled && (
            <TabsContent value="billing" className="mt-4">
              <BillingSection
                subscription={subscriptionQuery.data}
                loading={subscriptionQuery.isLoading}
              />
            </TabsContent>
          )}
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
  const [vipAccount, setVipAccount] = React.useState<ConnectedAccount | null>(null);

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
    <>
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
                className="text-muted-foreground gap-1.5"
                onClick={() => setVipAccount(account)}
              >
                <Crown className="size-3.5" />
                VIP{account.vipSenders.length > 0 ? ` · ${account.vipSenders.length}` : ''}
              </Button>
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
    {vipAccount && (
      <VipSendersDialog
        account={vipAccount}
        onOpenChange={(open) => {
          if (!open) setVipAccount(null);
        }}
      />
    )}
    </>
  );
}

// ---------------------------------------------------------------------------
// VIP senders editor (routes matching senders into the "vip" split)
// ---------------------------------------------------------------------------

function VipSendersDialog({
  account,
  onOpenChange,
}: {
  account: ConnectedAccount;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [senders, setSenders] = React.useState<string[]>(account.vipSenders);
  const [draft, setDraft] = React.useState('');

  const save = useMutation({
    mutationFn: () => setVipSendersApi(account.id, senders),
    onSuccess: (updated) => {
      queryClient.setQueryData<ConnectedAccount[]>(['accounts'], (prev) =>
        prev?.map((a) => (a.id === updated.id ? updated : a))
      );
      toast.success('VIP senders updated');
      onOpenChange(false);
    },
    onError: () => toast.error('Could not update VIP senders'),
  });

  const add = () => {
    const email = draft.trim().replace(/,+$/, '').toLowerCase();
    if (!email) return;
    if (!EMAIL_RE.test(email)) {
      toast.error(`"${email}" is not a valid email address`);
      return;
    }
    if (!senders.includes(email)) setSenders((prev) => [...prev, email]);
    setDraft('');
  };

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>VIP senders</DialogTitle>
          <DialogDescription>
            Mail from these addresses is routed to your VIP split - {account.email}.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <div className="border-input dark:bg-input/30 focus-within:border-ring focus-within:ring-ring/50 flex min-h-9 flex-wrap items-center gap-1 rounded-md border bg-transparent px-2 py-1 shadow-xs transition-[color,box-shadow] focus-within:ring-[3px]">
            {senders.map((email) => (
              <Badge key={email} variant="secondary" className="gap-1 font-normal">
                {email}
                <button
                  type="button"
                  aria-label={`Remove ${email}`}
                  className="opacity-60 hover:opacity-100"
                  onClick={() => setSenders((prev) => prev.filter((s) => s !== email))}
                >
                  <X className="size-3" />
                </button>
              </Badge>
            ))}
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ',') {
                  e.preventDefault();
                  add();
                } else if (e.key === 'Backspace' && !draft && senders.length > 0) {
                  setSenders((prev) => prev.slice(0, -1));
                }
              }}
              onBlur={add}
              placeholder={senders.length === 0 ? 'name@example.com (Enter)' : ''}
              aria-label="Add VIP sender"
              className="placeholder:text-muted-foreground h-6 min-w-40 flex-1 bg-transparent text-sm outline-none"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending && <Loader2 className="animate-spin" />}
            Save VIP senders
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
// Mailbox (Split order)
// ---------------------------------------------------------------------------

function MailboxSection() {
  const prefsQuery = usePrefs();
  const updatePrefsAsync = useUpdatePrefs();
  const queryClient = useQueryClient();

  // Derive splits from prefs, matching the pattern in mail/page.tsx
  const splits = React.useMemo(
    () => orderSplits(DEFAULT_SPLITS, prefsQuery.data?.prefs.splitOrder ?? []),
    [prefsQuery.data]
  );

  const updatePrefs = useMutation({
    mutationFn: async (order: InboxSplit[]) => {
      await updatePrefsAsync({ splitOrder: order });
    },
    onSuccess: () => {
      toast.success('Split order saved');
    },
    onError: () => {
      // Error toast is already handled by the hook
    },
  });

  const moveUp = (index: number) => {
    if (index <= 0) return;
    const newOrder = [...splits];
    [newOrder[index], newOrder[index - 1]] = [newOrder[index - 1]!, newOrder[index]!];
    const nextOrder = newOrder.map((s) => s.value);
    updatePrefs.mutate(nextOrder);
  };

  const moveDown = (index: number) => {
    if (index >= splits.length - 1) return;
    const newOrder = [...splits];
    [newOrder[index], newOrder[index + 1]] = [newOrder[index + 1]!, newOrder[index]!];
    const nextOrder = newOrder.map((s) => s.value);
    updatePrefs.mutate(nextOrder);
  };

  if (prefsQuery.isLoading) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Split order</CardTitle>
          <CardDescription>Reorder your inbox splits — the first split is your landing tab.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Split order</CardTitle>
        <CardDescription>Reorder your inbox splits — the first split is your landing tab.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {splits.map((split, index) => (
          <div key={split.value} className="flex items-center gap-2 rounded-lg border p-3">
            <span className="text-sm font-medium">{split.label}</span>
            <div className="ml-auto flex gap-1">
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={() => moveUp(index)}
                disabled={index === 0 || updatePrefs.isPending}
                aria-label={`Move ${split.label} up`}
              >
                <ChevronUp className="size-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={() => moveDown(index)}
                disabled={index === splits.length - 1 || updatePrefs.isPending}
                aria-label={`Move ${split.label} down`}
              >
                <ChevronDown className="size-4" />
              </Button>
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Notifications (Web Push)
// ---------------------------------------------------------------------------

const PUSH_DEVICE_KEY = 'calendium.web-push.device-id';

function NotificationsSection({ vapidPublicKey }: { vapidPublicKey?: string }) {
  const [supported, setSupported] = React.useState(false);
  const [enabled, setEnabled] = React.useState(false);
  const [permission, setPermission] = React.useState<NotificationPermission>('default');
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    const ok = isWebPushSupported();
    setSupported(ok);
    if (!ok) return;
    setPermission(Notification.permission);
    void getPushSubscription().then((sub) => setEnabled(!!sub));
  }, []);

  async function enable() {
    if (!vapidPublicKey) return;
    setBusy(true);
    try {
      const deviceId = await enableWebPush(vapidPublicKey);
      window.localStorage.setItem(PUSH_DEVICE_KEY, deviceId);
      setEnabled(true);
      setPermission(Notification.permission);
      toast.success('Push notifications enabled on this device');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Could not enable push notifications');
    } finally {
      setBusy(false);
    }
  }

  async function disable() {
    setBusy(true);
    try {
      await disableWebPush(window.localStorage.getItem(PUSH_DEVICE_KEY));
      window.localStorage.removeItem(PUSH_DEVICE_KEY);
      setEnabled(false);
      toast.success('Push notifications disabled on this device');
    } catch {
      toast.error('Could not disable push notifications');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Bell className="size-4" />
          Push notifications
        </CardTitle>
        <CardDescription>
          Get notified on this device for important and VIP mail - even when Calendium is closed.
        </CardDescription>
      </CardHeader>
      <CardContent className="text-sm">
        {!supported ? (
          <p className="text-muted-foreground">
            This browser doesn&apos;t support web push notifications.
          </p>
        ) : !vapidPublicKey ? (
          <p className="text-muted-foreground">Web push isn&apos;t configured on this server.</p>
        ) : permission === 'denied' ? (
          <p className="text-muted-foreground">
            Notifications are blocked for this site in your browser settings. Re-enable them there
            to receive push.
          </p>
        ) : enabled ? (
          <p>Push notifications are on for this device.</p>
        ) : (
          <p>Turn on push to get a heads-up the moment important mail arrives.</p>
        )}
      </CardContent>
      <CardFooter className="border-t pt-6">
        {enabled ? (
          <Button variant="outline" onClick={() => void disable()} disabled={busy}>
            {busy && <Loader2 className="animate-spin" />}
            Disable notifications
          </Button>
        ) : (
          <Button
            onClick={() => void enable()}
            disabled={busy || !supported || !vapidPublicKey || permission === 'denied'}
          >
            {busy ? <Loader2 className="animate-spin" /> : <Bell />}
            Enable notifications
          </Button>
        )}
      </CardFooter>
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
