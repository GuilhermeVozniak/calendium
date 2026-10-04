'use client';

import * as React from 'react';
import Link from 'next/link';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { format, formatDistanceToNow } from 'date-fns';
import {
  Bell,
  Bold,
  ChevronDown,
  ChevronUp,
  CreditCard,
  Crown,
  ExternalLink,
  Italic,
  Layers,
  LayoutTemplate,
  Link2,
  Loader2,
  Monitor,
  Moon,
  PenLine,
  Plus,
  Sparkles,
  Sun,
  Trash2,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import type {
  AccountStatus,
  AvailabilityWindow,
  CalendarSubscription,
  ConnectedAccount,
  EmailAddress,
  InboxSplit,
  Provider,
  Snippet,
  Subscription,
  UserSettings,
} from '@calendium/shared';
import { ApiRequestError, hasBillingSubscription, trialDaysLeft } from '@calendium/shared';

import { BookingLinks, localTimeZone, WindowsEditor } from '@/components/app/booking-links';
import { ChipsRow } from '@/components/app/chips-row';
import { DelegationSection } from '@/components/app/delegation';
import { IntegrationsSection } from '@/components/app/integrations-section';
import { SetSwitcher } from '@/components/app/calendar/set-switcher';
import { TemplateManager } from '@/components/app/calendar/template-manager';
import { MeetingPolls } from '@/components/app/meeting-polls';
import { useBillingPortalMutation, useCheckoutMutation } from '@/components/app/paywall';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import {
  createCalendarSubscriptionApi,
  createSnippetApi,
  deleteCalendarSubscriptionApi,
  deleteSnippetApi,
  disconnectAccountApi,
  fetchAccounts,
  fetchCalendarSubscriptions,
  fetchSnippets,
  fetchSubscription,
  setAutoBccApi,
  setSignatureApi,
  setVipSendersApi,
  startConnect,
  updateCalendarSubscriptionApi,
} from '@/lib/settings-data';
import { subscriptionStatus } from '@/lib/subscription-utils';
import { Switch } from '@/components/ui/switch';
import { fetchSettings, updateSettingsApi } from '@/lib/scheduling-data';
import { CalendarAutomationSection } from './calendar-automation';
import { AccountSection } from './settings-account';
import { getApiClient } from '@/lib/api';
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

type SettingsTab =
  | 'accounts'
  | 'account'
  | 'integrations'
  | 'snippets'
  | 'templates'
  | 'sets'
  | 'feeds'
  | 'scheduling'
  | 'delegation'
  | 'appearance'
  | 'mailbox'
  | 'ai'
  | 'notifications'
  | 'billing';

const KNOWN_TABS: SettingsTab[] = [
  'accounts',
  'account',
  'integrations',
  'snippets',
  'templates',
  'sets',
  'feeds',
  'scheduling',
  'delegation',
  'appearance',
  'mailbox',
  'ai',
  'notifications',
  'billing',
];

export default function SettingsPage() {
  const instance = useInstance();
  const billingEnabled = !!instance.data?.features.billing;
  const pushEnabled = !!instance.data?.features.push;
  const aiEnabled = !!instance.data?.features.ai;
  const vapidPublicKey = instance.data?.vapidPublicKey;
  const capabilities = instance.data?.capabilities;
  const integrationsEnabled = !!(capabilities?.todoist || capabilities?.hubspot);

  const availableTabs = React.useMemo<SettingsTab[]>(
    () => [
      'accounts',
      'account',
      ...(integrationsEnabled ? (['integrations'] as SettingsTab[]) : []),
      'snippets',
      'templates',
      'sets',
      'feeds',
      'scheduling',
      'delegation',
      'appearance',
      'mailbox',
      ...(aiEnabled ? (['ai'] as SettingsTab[]) : []),
      ...(pushEnabled ? (['notifications'] as SettingsTab[]) : []),
      ...(billingEnabled ? (['billing'] as SettingsTab[]) : []),
    ],
    [integrationsEnabled, aiEnabled, pushEnabled, billingEnabled]
  );

  const [tab, setTab] = React.useState<SettingsTab>('accounts');
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: fetchSubscription,
    enabled: billingEnabled,
  });

  // Deep-link section via ?tab= (primary) or #hash. Validation against
  // availability happens at render, so a ?tab=billing that arrives before
  // /v1/instance loads still lands correctly.
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const requested = params.get('tab') || window.location.hash.replace('#', '');
    if ((KNOWN_TABS as string[]).includes(requested)) setTab(requested as SettingsTab);
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
          Accounts, account security, snippets, templates, sets, scheduling, delegation, appearance, mailbox
          {pushEnabled ? ', notifications' : ''}
          {billingEnabled ? ', and billing' : ''}.
        </p>

        <Tabs value={activeTab} onValueChange={changeTab} className="mt-6">
          <TabsList>
            <TabsTrigger value="accounts">Accounts</TabsTrigger>
            <TabsTrigger value="account">Account</TabsTrigger>
            {integrationsEnabled && (
              <TabsTrigger value="integrations">Integrations</TabsTrigger>
            )}
            <TabsTrigger value="snippets">Snippets</TabsTrigger>
            <TabsTrigger value="templates">Templates</TabsTrigger>
            <TabsTrigger value="sets">Sets</TabsTrigger>
            <TabsTrigger value="feeds">Feeds</TabsTrigger>
            <TabsTrigger value="scheduling">Scheduling</TabsTrigger>
            <TabsTrigger value="delegation">Delegation</TabsTrigger>
            <TabsTrigger value="appearance">Appearance</TabsTrigger>
            <TabsTrigger value="mailbox">Mailbox</TabsTrigger>
            {aiEnabled && <TabsTrigger value="ai">AI</TabsTrigger>}
            {pushEnabled && <TabsTrigger value="notifications">Notifications</TabsTrigger>}
            {billingEnabled && <TabsTrigger value="billing">Billing</TabsTrigger>}
          </TabsList>
          <TabsContent value="accounts" className="mt-4">
            <AccountsSection />
          </TabsContent>
          <TabsContent value="account" className="mt-4">
            <AccountSection />
          </TabsContent>
          {integrationsEnabled && (
            <TabsContent value="integrations" className="mt-4">
              <IntegrationsSection capabilities={capabilities} />
            </TabsContent>
          )}
          <TabsContent value="snippets" className="mt-4">
            <SnippetsSection />
          </TabsContent>
          <TabsContent value="templates" className="mt-4">
            <TemplatesSection />
          </TabsContent>
          <TabsContent value="sets" className="mt-4">
            <SetsSection />
          </TabsContent>
          <TabsContent value="feeds" className="mt-4">
            <SubscriptionsSection />
          </TabsContent>
          <TabsContent value="scheduling" className="mt-4">
            <SchedulingSection />
          </TabsContent>
          <TabsContent value="delegation" className="mt-4">
            <DelegationSection />
          </TabsContent>
          <TabsContent value="appearance" className="mt-4">
            <AppearanceSection />
          </TabsContent>
          <TabsContent value="mailbox" className="mt-4">
            <MailboxSection />
          </TabsContent>
          {aiEnabled && (
            <TabsContent value="ai" className="mt-4">
              <AiSection />
            </TabsContent>
          )}
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

export function AccountsSection() {
  const queryClient = useQueryClient();
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const [vipAccount, setVipAccount] = React.useState<ConnectedAccount | null>(null);
  const [composeAccount, setComposeAccount] = React.useState<ConnectedAccount | null>(null);

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
                className="text-muted-foreground gap-1.5"
                onClick={() => setComposeAccount(account)}
              >
                <PenLine className="size-3.5" />
                Compose
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
    {composeAccount && (
      <ComposeSettingsDialog
        account={composeAccount}
        onOpenChange={(open) => {
          if (!open) setComposeAccount(null);
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
// Compose settings (per-account signature + auto-BCC)
// ---------------------------------------------------------------------------

/**
 * Lightweight rich-text signature editor: a contentEditable div with a
 * Bold/Italic/Link toolbar driven by document.execCommand (same approach as
 * the rest of the app's compose surface — no new rich-text dependency). The
 * div's innerHTML is only set once on mount; subsequent keystrokes flow
 * through onInput -> onChange so we never stomp the live DOM (and the
 * caret position) by re-applying dangerouslySetInnerHTML while typing.
 */
function SignatureEditor({
  signatureHtml,
  onChange,
}: {
  signatureHtml: string;
  onChange: (html: string) => void;
}) {
  const editorRef = React.useRef<HTMLDivElement>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: intentional mount-only hydration — re-running on signatureHtml changes would stomp the live DOM (and caret position) while the user is typing; see the editor's own doc comment above.
  React.useEffect(() => {
    if (editorRef.current) editorRef.current.innerHTML = signatureHtml;
  }, []);

  function exec(command: string, arg?: string) {
    editorRef.current?.focus();
    document.execCommand(command, false, arg);
    onChange(editorRef.current?.innerHTML ?? '');
  }

  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-1">
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="size-7"
          onClick={() => exec('bold')}
          aria-label="Bold"
        >
          <Bold className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="size-7"
          onClick={() => exec('italic')}
          aria-label="Italic"
        >
          <Italic className="size-3.5" />
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon"
          className="size-7"
          onClick={() => {
            const url = window.prompt('Link URL');
            if (url) exec('createLink', url);
          }}
          aria-label="Insert link"
        >
          <Link2 className="size-3.5" />
        </Button>
      </div>
      {/* biome-ignore lint/a11y/useSemanticElements: needs real rich-text formatting (Bold/Italic/Link via execCommand) that a plain <textarea> can't render — contentEditable + role="textbox" is the standard pattern for this. */}
      <div
        ref={editorRef}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        tabIndex={0}
        aria-label="Signature"
        aria-multiline="true"
        onInput={() => onChange(editorRef.current?.innerHTML ?? '')}
        className="min-h-24 rounded-md border px-3 py-2 text-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
      />
      <div className="grid gap-1">
        <span className="text-muted-foreground text-xs">Preview</span>
        <div
          className="rounded-md border border-dashed p-3 text-sm text-muted-foreground empty:text-muted-foreground/60"
          // biome-ignore lint/security/noDangerouslySetInnerHtml: signatureHtml is sanitized server-side by handleSetSignature (backend/internal/adapter/in/httpapi/sanitize.go's sanitizeSignatureHTML — strips script/style/iframe/object/embed, on* attributes, and javascript: URLs) before it is ever persisted, so what's fetched back and rendered here is already scrubbed. There is no client-side re-sanitization step; this preview trusts the server boundary.
          dangerouslySetInnerHTML={{ __html: signatureHtml || '<span>No signature yet.</span>' }}
        />
      </div>
    </div>
  );
}

function ComposeSettingsDialog({
  account,
  onOpenChange,
}: {
  account: ConnectedAccount;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [signatureHtml, setSignatureHtml] = React.useState(account.signatureHtml);
  const [autoBcc, setAutoBcc] = React.useState<string[]>(account.autoBcc);
  const [bccError, setBccError] = React.useState<string | null>(null);

  function applyAccountUpdate(updated: ConnectedAccount) {
    queryClient.setQueryData<ConnectedAccount[]>(['accounts'], (prev) =>
      prev?.map((a) => (a.id === updated.id ? updated : a))
    );
  }

  const saveSignature = useMutation({
    mutationFn: () => setSignatureApi(account.id, signatureHtml),
    onSuccess: (updated) => {
      applyAccountUpdate(updated);
      toast.success('Signature saved');
    },
    onError: (err) => {
      toast.error(
        err instanceof ApiRequestError ? err.message : 'Could not save the signature'
      );
    },
  });

  const saveAutoBcc = useMutation({
    mutationFn: () => setAutoBccApi(account.id, autoBcc),
    onSuccess: (updated) => {
      applyAccountUpdate(updated);
      toast.success('Auto-BCC saved');
      setBccError(null);
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.status === 400) {
        setBccError(err.message);
        return;
      }
      toast.error('Could not save auto-BCC');
    },
  });

  const bccChips: EmailAddress[] = autoBcc.map((email) => ({ name: null, email }));

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Compose settings</DialogTitle>
          <DialogDescription>
            Signature and auto-BCC applied when sending from {account.email}.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-5">
          <div className="grid gap-1.5">
            <div className="flex items-center justify-between">
              <Label>Signature</Label>
              <Button size="sm" onClick={() => saveSignature.mutate()} disabled={saveSignature.isPending}>
                {saveSignature.isPending && <Loader2 className="animate-spin" />}
                Save signature
              </Button>
            </div>
            <SignatureEditor signatureHtml={signatureHtml} onChange={setSignatureHtml} />
          </div>
          <div className="grid gap-1.5">
            <div className="flex items-center justify-between">
              <Label>Auto-BCC</Label>
              <Button size="sm" onClick={() => saveAutoBcc.mutate()} disabled={saveAutoBcc.isPending}>
                {saveAutoBcc.isPending && <Loader2 className="animate-spin" />}
                Save auto-BCC
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              Every address here is silently BCC'd on messages sent from this account.
            </p>
            <div className="rounded-md border">
              <ChipsRow
                label="Bcc"
                chips={bccChips}
                onChange={(chips) => {
                  setAutoBcc(chips.map((c) => c.email));
                  setBccError(null);
                }}
              />
            </div>
            {bccError && <p className="text-destructive text-xs">{bccError}</p>}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
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

export function SnippetsSection() {
  const queryClient = useQueryClient();
  const snippetsQuery = useQuery({ queryKey: ['snippets'], queryFn: fetchSnippets });

  // Teams for the create dialog's scope selector and the team badge on
  // team snippets (M2.7 Task 11).
  const teamsQuery = useQuery({ queryKey: ['teams'], queryFn: () => getApiClient().listTeams() });
  const teams = React.useMemo(() => teamsQuery.data ?? [], [teamsQuery.data]);

  const [createOpen, setCreateOpen] = React.useState(false);
  const [name, setName] = React.useState('');
  const [shortcut, setShortcut] = React.useState('');
  const [body, setBody] = React.useState('');
  const [teamId, setTeamId] = React.useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      createSnippetApi({
        name: name.trim(),
        shortcut: shortcut.trim() || null,
        bodyHtml: textToHtml(body.trim()),
        teamId,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['snippets'] });
      toast.success('Snippet created');
      setCreateOpen(false);
      setName('');
      setShortcut('');
      setBody('');
      setTeamId(null);
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
  // Team snippets (M2.7) are grouped after personal ones and badged.
  const personalSnippets = snippets.filter((s) => !s.teamId);
  const teamSnippets = snippets.filter((s) => Boolean(s.teamId));

  // The server computes canDelete per request with exact author/role parity
  // (F2): author, or admin+ on team snippets. The UI only hides controls the
  // server would reject; an absent field (older server) fails open and lets
  // the server decide.
  const canDelete = (snippet: Snippet): boolean => snippet.canDelete ?? true;

  const renderSnippetRow = (snippet: Snippet) => (
    <div key={snippet.id} className="flex items-center gap-3 rounded-lg border p-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{snippet.name}</span>
          {snippet.shortcut && <Kbd className="normal-case">{snippet.shortcut}</Kbd>}
          {snippet.teamId && (
            <Badge variant="secondary">
              {teams.find((t) => t.id === snippet.teamId)?.name ?? 'Team'}
            </Badge>
          )}
        </div>
        <p className="mt-0.5 truncate text-xs text-muted-foreground">
          {htmlToText(snippet.bodyHtml)}
        </p>
      </div>
      <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
        used {snippet.usageCount}x
      </span>
      {canDelete(snippet) && (
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
      )}
    </div>
  );

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
          {personalSnippets.map(renderSnippetRow)}
          {teamSnippets.length > 0 && (
            <p className="mt-2 text-xs font-medium text-muted-foreground">
              Team snippets — shared with your teams
            </p>
          )}
          {teamSnippets.map(renderSnippetRow)}
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
            {teams.length > 0 && (
              <div className="grid gap-1.5">
                <Label htmlFor="snippet-team">Share with</Label>
                <Select
                  value={teamId ?? 'personal'}
                  onValueChange={(value) => setTeamId(value === 'personal' ? null : value)}
                >
                  <SelectTrigger id="snippet-team" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="personal">Personal — only you</SelectItem>
                    {teams.map((team) => (
                      <SelectItem key={team.id} value={team.id}>
                        {team.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  Team snippets are shared with every member; the scope is fixed at creation.
                </p>
              </div>
            )}
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
// Templates
// ---------------------------------------------------------------------------

function TemplatesSection() {
  const [managerOpen, setManagerOpen] = React.useState(false);
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Event templates</CardTitle>
          <CardDescription>
            Saved defaults ("1:1", "Focus block") you can apply when creating an event.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setManagerOpen(true)}>
              <LayoutTemplate />
              Manage templates
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      <TemplateManager open={managerOpen} onOpenChange={setManagerOpen} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Calendar sets
// ---------------------------------------------------------------------------

function SetsSection() {
  const [switcherOpen, setSwitcherOpen] = React.useState(false);
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Calendar sets</CardTitle>
          <CardDescription>
            Named groups of calendars ("Work", "Home") you can switch on together.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={() => setSwitcherOpen(true)}>
              <Layers />
              Manage sets
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
      <SetSwitcher open={switcherOpen} onOpenChange={setSwitcherOpen} />
    </>
  );
}

// ---------------------------------------------------------------------------
// Scheduling (working hours, time zone, working location, booking links,
// meeting polls)
// ---------------------------------------------------------------------------

function tzOptions(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [];
  }
}

function SchedulingSection() {
  const queryClient = useQueryClient();
  const settingsQuery = useQuery({ queryKey: ['scheduling-settings'], queryFn: fetchSettings });
  const zones = React.useMemo(tzOptions, []);

  const [timeZone, setTimeZone] = React.useState('');
  const [workingHours, setWorkingHours] = React.useState<AvailabilityWindow[]>([]);
  const [workingLocation, setWorkingLocation] = React.useState('');
  const [tzError, setTzError] = React.useState<string | null>(null);
  const hydrated = React.useRef(false);

  React.useEffect(() => {
    if (settingsQuery.data && !hydrated.current) {
      setTimeZone(settingsQuery.data.timeZone || localTimeZone());
      setWorkingHours(settingsQuery.data.workingHours);
      setWorkingLocation(settingsQuery.data.workingLocation);
      hydrated.current = true;
    }
  }, [settingsQuery.data]);

  const save = useMutation({
    mutationFn: () => {
      // Carry the stored background-AI switch through untouched: this form
      // only edits scheduling fields (an absent field is kept server-side).
      const current = queryClient.getQueryData<UserSettings>(['scheduling-settings']);
      const next = { ...current, timeZone, workingHours, workingLocation } as UserSettings;
      return updateSettingsApi(next);
    },
    onSuccess: (s) => {
      queryClient.setQueryData<UserSettings>(['scheduling-settings'], s);
      toast.success('Scheduling settings saved');
      setTzError(null);
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.status === 400) {
        setTzError('That does not look like a valid time zone.');
        return;
      }
      toast.error('Could not save scheduling settings');
    },
  });

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Working hours</CardTitle>
          <CardDescription>
            Your time zone and weekly availability - used by booking links and polls that
            respect working hours.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {settingsQuery.isLoading ? (
            <>
              <Skeleton className="h-9 w-48" />
              <Skeleton className="h-24 w-full" />
            </>
          ) : (
            <>
              <div className="grid gap-1.5 sm:max-w-xs">
                <Label htmlFor="scheduling-tz">Time zone</Label>
                <Input
                  id="scheduling-tz"
                  value={timeZone}
                  onChange={(e) => {
                    setTimeZone(e.target.value);
                    setTzError(null);
                  }}
                  list="scheduling-tz-options"
                  placeholder="America/New_York"
                />
                <datalist id="scheduling-tz-options">
                  {zones.map((z) => (
                    <option key={z} value={z} />
                  ))}
                </datalist>
                {tzError && <p className="text-xs text-destructive">{tzError}</p>}
              </div>
              <div className="grid gap-1.5">
                <Label>Weekly availability</Label>
                <WindowsEditor
                  windows={workingHours}
                  onChange={setWorkingHours}
                  addLabel="Add working hours"
                  minRows={0}
                />
                {workingHours.length === 0 && (
                  <p className="text-xs text-muted-foreground">
                    No constraint set - booking links and polls that respect working hours will
                    treat every day as open.
                  </p>
                )}
              </div>
              <div className="grid gap-1.5 sm:max-w-xs">
                <Label htmlFor="scheduling-location">Working location</Label>
                <Input
                  id="scheduling-location"
                  value={workingLocation}
                  onChange={(e) => setWorkingLocation(e.target.value)}
                  placeholder="Office, home, ..."
                />
              </div>
            </>
          )}
        </CardContent>
        <CardFooter className="border-t pt-6">
          <Button onClick={() => save.mutate()} disabled={settingsQuery.isLoading || save.isPending}>
            {save.isPending && <Loader2 className="animate-spin" />}
            Save
          </Button>
        </CardFooter>
      </Card>

      <CalendarAutomationSection />

      <BookingLinks />
      <MeetingPolls />
    </div>
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
// AI
// ---------------------------------------------------------------------------

function AiSection() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>AI classifiers</CardTitle>
        <CardDescription>
          Natural-language rules that route matching mail to a split and/or tag it with a label.
        </CardDescription>
        <CardAction>
          <Button size="sm" asChild>
            <Link href="/settings/classifiers">
              <Sparkles />
              Manage classifiers
            </Link>
          </Button>
        </CardAction>
      </CardHeader>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Mailbox (Split order)
// ---------------------------------------------------------------------------

function MailboxSection() {
  const prefsQuery = usePrefs();
  const updatePrefsAsync = useUpdatePrefs();

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
// Billing (docs/payments.md — $50/yr on Paddle, Spotify model)
// ---------------------------------------------------------------------------

export function BillingSection({
  subscription,
  loading,
}: {
  subscription: Subscription | undefined;
  loading: boolean;
}) {
  const checkout = useCheckoutMutation();
  const manage = useBillingPortalMutation('overview');
  const cancel = useBillingPortalMutation('cancel');
  const updatePayment = useBillingPortalMutation('updatePayment');

  if (loading) return <Skeleton className="h-56 w-full" />;

  const sub = subscription;
  const fmt = (iso: string) => format(new Date(iso), 'MMMM d, yyyy');
  const live = !!sub && hasBillingSubscription(sub);
  const daysLeft = sub ? trialDaysLeft(sub) : null;

  const badge = (() => {
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
        return <Badge variant="destructive">Payment failed</Badge>;
      case 'paused':
        return <Badge variant="outline">Paused</Badge>;
      case 'canceled':
        return <Badge variant="outline">Canceled</Badge>;
      default:
        return <Badge variant="outline">No subscription</Badge>;
    }
  })();

  const statusLine = (() => {
    if (!sub) return 'No subscription yet.';
    switch (sub.status) {
      case 'trialing':
        return sub.trialEndsAt
          ? `Free trial — ends ${fmt(sub.trialEndsAt)}${daysLeft !== null ? ` (${daysLeft} day${daysLeft === 1 ? '' : 's'} left)` : ''}. Subscribe any time; billing starts only when you do.`
          : 'Free trial.';
      case 'active':
        if (!sub.currentPeriodEnd) return 'Active.';
        return sub.cancelAtPeriodEnd
          ? `Cancels on ${fmt(sub.currentPeriodEnd)} — access continues until then.`
          : `Renews on ${fmt(sub.currentPeriodEnd)} for $50.`;
      case 'past_due':
        return 'We could not charge your card. Update your payment method to keep access.';
      case 'paused':
        return 'Your plan is paused. Update your payment method or resume it from billing.';
      case 'canceled':
        return 'Your subscription has ended. Resubscribe any time.';
      default:
        return 'You do not have an active subscription.';
    }
  })();

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
          <CardAction>{badge}</CardAction>
        </CardHeader>
        <CardContent className="text-sm">
          <p>{statusLine}</p>
        </CardContent>
        <CardFooter className="gap-2 border-t pt-6">
          {live ? (
            <>
              {(sub?.status === 'past_due' || sub?.status === 'paused') && (
                <Button onClick={() => updatePayment.mutate()} disabled={updatePayment.isPending}>
                  Update payment method
                </Button>
              )}
              <Button variant="outline" onClick={() => manage.mutate()} disabled={manage.isPending}>
                Manage billing
                <ExternalLink />
              </Button>
              <Button variant="ghost" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                Cancel subscription
              </Button>
            </>
          ) : (
            <Button onClick={() => checkout.mutate()} disabled={checkout.isPending}>
              Subscribe · $50/year
            </Button>
          )}
        </CardFooter>
      </Card>
      <p className="text-xs text-muted-foreground">
        Billing runs through Paddle, our merchant of record: invoices, receipts, and sales tax or VAT
        are handled by Paddle. Checkout always happens on the web - the mobile and desktop apps never
        charge you directly.
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Calendar feeds (M2.8 Task 15: interesting-calendar ICS subscriptions)
// ---------------------------------------------------------------------------

const SUBSCRIPTION_COLORS = ['#8b5cf6', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#ec4899'];

export function SubscriptionsSection() {
  const queryClient = useQueryClient();
  const subsQuery = useQuery({
    queryKey: ['calendar-subscriptions'],
    queryFn: fetchCalendarSubscriptions,
  });
  const [url, setUrl] = React.useState('');
  const [name, setName] = React.useState('');
  const [color, setColor] = React.useState(SUBSCRIPTION_COLORS[0]!);
  const [formError, setFormError] = React.useState<string | null>(null);

  const refreshEvents = () => {
    queryClient.invalidateQueries({ queryKey: ['calendar-subscriptions'] });
    queryClient.invalidateQueries({ queryKey: ['events'] });
  };

  const create = useMutation({
    mutationFn: () =>
      createCalendarSubscriptionApi({
        url: url.trim(),
        name: name.trim() || undefined,
        color,
      }),
    onSuccess: (sub) => {
      refreshEvents();
      setUrl('');
      setName('');
      setFormError(null);
      toast.success(`Subscribed to ${sub.name}`);
    },
    onError: (err) => {
      // The server fetches the feed synchronously, so 400 (bad URL), 422
      // (unreachable/unparseable feed), and 409 (already subscribed) each
      // carry a stable message worth surfacing inline.
      setFormError(
        err instanceof ApiRequestError ? err.message : 'Could not subscribe to the feed.'
      );
    },
  });

  const update = useMutation({
    mutationFn: ({ id, isVisible }: { id: string; isVisible: boolean }) =>
      updateCalendarSubscriptionApi(id, { isVisible }),
    onSuccess: (updated) => {
      queryClient.setQueryData<CalendarSubscription[]>(['calendar-subscriptions'], (prev) =>
        prev?.map((s) => (s.id === updated.id ? updated : s))
      );
      queryClient.invalidateQueries({ queryKey: ['events'] });
    },
    onError: () => toast.error('Could not update the feed'),
  });

  const remove = useMutation({
    mutationFn: deleteCalendarSubscriptionApi,
    onSuccess: (_data, id) => {
      queryClient.setQueryData<CalendarSubscription[]>(['calendar-subscriptions'], (prev) =>
        prev?.filter((s) => s.id !== id)
      );
      queryClient.invalidateQueries({ queryKey: ['events'] });
      toast.success('Feed removed');
    },
    onError: () => toast.error('Could not remove the feed'),
  });

  const subs = subsQuery.data ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Calendar feeds</CardTitle>
        <CardDescription>
          Subscribe to public ICS calendars (holidays, team schedules). Feed events show on
          your calendar read-only and never count as busy time.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {subsQuery.isLoading && (
          <>
            <Skeleton className="h-14 w-full" />
            <Skeleton className="h-14 w-full" />
          </>
        )}
        {!subsQuery.isLoading && subs.length === 0 && (
          <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
            No calendar feeds yet. Paste an https ICS link below to subscribe.
          </p>
        )}
        {subs.map((sub) => {
          const status = subscriptionStatus(sub);
          return (
            <div key={sub.id} className="flex items-center gap-3 rounded-lg border p-3">
              <span
                className="size-3 shrink-0 rounded-full"
                style={{ backgroundColor: sub.color }}
                aria-hidden
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium">{sub.name}</span>
                  {status.kind === 'error' && (
                    <Badge variant="destructive" className="font-normal" title={status.label}>
                      Fetch failed
                    </Badge>
                  )}
                </div>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  {sub.url}
                  {' · '}
                  {status.label}
                </p>
              </div>
              <Switch
                checked={sub.isVisible}
                onCheckedChange={(checked) => update.mutate({ id: sub.id, isVisible: checked })}
                aria-label={`Show ${sub.name} on the calendar`}
              />
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground hover:text-destructive"
                onClick={() => remove.mutate(sub.id)}
                disabled={remove.isPending}
                aria-label={`Remove feed ${sub.name}`}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
          );
        })}
      </CardContent>
      <CardFooter className="flex-col items-stretch gap-3 border-t pt-6">
        <div className="grid gap-2 sm:grid-cols-[1fr_12rem]">
          <div className="grid gap-1.5">
            <Label htmlFor="feed-url">ICS feed URL</Label>
            <Input
              id="feed-url"
              placeholder="https://example.com/holidays.ics"
              value={url}
              onChange={(e) => {
                setUrl(e.target.value);
                setFormError(null);
              }}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="feed-name">Name (optional)</Label>
            <Input
              id="feed-name"
              placeholder="From the feed"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">Color</span>
          {SUBSCRIPTION_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              aria-label={`Color ${c}`}
              aria-pressed={color === c}
              className={cn(
                'size-5 rounded-full border-2',
                color === c ? 'border-foreground' : 'border-transparent'
              )}
              style={{ backgroundColor: c }}
              onClick={() => setColor(c)}
            />
          ))}
          <div className="flex-1" />
          <Button
            onClick={() => create.mutate()}
            disabled={!url.trim() || create.isPending}
          >
            {create.isPending ? <Loader2 className="animate-spin" /> : <Plus />}
            Add feed
          </Button>
        </div>
        {formError && (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        )}
      </CardFooter>
    </Card>
  );
}
