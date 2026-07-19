import type {
  AiClassifier,
  ClassifierInput,
  ConnectedAccount,
  InboxSplit,
  Provider,
  SubscriptionStatus,
} from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { format } from 'date-fns';
import {
  ChevronDown,
  ChevronRight,
  CreditCard,
  ExternalLink,
  Keyboard,
  Loader2,
  LogOut,
  Mail,
  Plus,
  RefreshCw,
  Server,
  Trash2,
} from 'lucide-react';
import { type ReactNode, useEffect, useState } from 'react';

import { api, apiConfigured, orMock } from '@/lib/api';
import { clearStoredToken, signOut } from '@/lib/auth';
import { htmlToText, toHtml } from '@/lib/compose';
import {
  createMockClassifier,
  deleteMockClassifier,
  listMockClassifiers,
  mockAccounts,
  mockSubscription,
  mockUser,
  updateMockAutoBcc,
  updateMockClassifier,
  updateMockSignature,
} from '@/lib/mock';
import { useServerConfig, webOrigin } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import {
  desktop,
  globalShortcutsEnabled,
  isDesktop,
  onDeepLink,
  setGlobalShortcutsEnabled,
} from '@/lib/wails';
import { Badge } from '@/ui/badge';
import { Button } from '@/ui/button';
import { Input } from '@/ui/input';

const SPLIT_OPTIONS: InboxSplit[] = ['important', 'vip', 'team', 'calendar', 'news', 'social', 'other'];
const MAX_CLASSIFIERS = 20;

// Task 9: shortcut labels only — the actual chord is chosen by the Go host.
const isMac = typeof navigator !== 'undefined' && /Mac/.test(navigator.userAgent);

function validateClassifier(form: { name: string; prompt: string; targetSplit: string; labelName: string }): string | null {
  if (!form.name.trim()) return 'Give the classifier a name.';
  if (!form.prompt.trim()) return 'Describe what this classifier should match.';
  if (!form.targetSplit && !form.labelName.trim()) {
    return 'Choose a target split or a label — at least one is required.';
  }
  return null;
}

function ClassifiersSection() {
  const queryClient = useQueryClient();
  const { data: classifiers = [] } = useQuery({
    queryKey: ['classifiers'],
    queryFn: () => orMock(() => api.listClassifiers(), () => listMockClassifiers()),
  });

  const [formOpen, setFormOpen] = useState(false);
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [targetSplit, setTargetSplit] = useState('');
  const [labelName, setLabelName] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: (input: ClassifierInput) =>
      orMock(() => api.createClassifier(input), () => createMockClassifier(input)),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['classifiers'] });
      toast({ title: 'Classifier created' });
      setFormOpen(false);
      setName('');
      setPrompt('');
      setTargetSplit('');
      setLabelName('');
    },
    onError: (e) => toast({ title: 'Could not create the classifier', description: errorMessage(e), variant: 'destructive' }),
  });

  const remove = useMutation({
    mutationFn: (id: string) => orMock(() => api.deleteClassifier(id), () => deleteMockClassifier(id)),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<AiClassifier[]>(['classifiers'], (prev) => prev?.filter((c) => c.id !== id));
      toast({ title: 'Classifier deleted' });
    },
    onError: (e) => toast({ title: 'Could not delete the classifier', description: errorMessage(e), variant: 'destructive' }),
  });

  const toggle = useMutation({
    mutationFn: (classifier: AiClassifier) => {
      const input: ClassifierInput = {
        name: classifier.name,
        prompt: classifier.prompt,
        targetSplit: classifier.targetSplit,
        labelName: classifier.labelName,
        enabled: !classifier.enabled,
      };
      return orMock(
        () => api.updateClassifier(classifier.id, input),
        () => updateMockClassifier(classifier.id, input)
      );
    },
    onSuccess: (updated) => {
      queryClient.setQueryData<AiClassifier[]>(['classifiers'], (prev) =>
        prev?.map((c) => (c.id === updated.id ? updated : c))
      );
    },
    onError: (e) => toast({ title: 'Could not update the classifier', description: errorMessage(e), variant: 'destructive' }),
  });

  function submit() {
    const error = validateClassifier({ name, prompt, targetSplit, labelName });
    if (error) {
      setFormError(error);
      return;
    }
    setFormError(null);
    create.mutate({
      name: name.trim(),
      prompt: prompt.trim(),
      targetSplit: (targetSplit || undefined) as InboxSplit | undefined,
      labelName: labelName.trim() || undefined,
      enabled: true,
    });
  }

  const atCap = classifiers.length >= MAX_CLASSIFIERS;
  const fieldClass =
    'flex h-8 w-full rounded-md border border-input bg-transparent px-2.5 py-1 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';

  return (
    <Section title="AI classifiers">
      {classifiers.length > 0 && (
        <ul className="divide-y">
          {classifiers.map((classifier) => (
            <li key={classifier.id} className="flex items-center gap-2 p-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <span className="truncate text-sm font-medium">{classifier.name}</span>
                  {classifier.targetSplit && (
                    <Badge variant="outline" className="capitalize">
                      {classifier.targetSplit}
                    </Badge>
                  )}
                  {classifier.labelName && <Badge variant="secondary">{classifier.labelName}</Badge>}
                </div>
                <p className="truncate text-xs text-muted-foreground">{classifier.prompt}</p>
              </div>
              <input
                type="checkbox"
                checked={classifier.enabled}
                onChange={() => toggle.mutate(classifier)}
                aria-label={`${classifier.enabled ? 'Disable' : 'Enable'} ${classifier.name}`}
              />
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground"
                aria-label={`Delete ${classifier.name}`}
                onClick={() => remove.mutate(classifier.id)}
              >
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-col gap-2 border-t p-3">
        {formOpen ? (
          <>
            <Input placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
            <textarea
              placeholder="Describe what to match, e.g. cold recruiter outreach."
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={2}
              className="w-full resize-none rounded-md border border-input bg-transparent px-2.5 py-1.5 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
            <div className="flex gap-2">
              <select
                value={targetSplit}
                onChange={(e) => setTargetSplit(e.target.value)}
                className={fieldClass}
              >
                <option value="">No target split</option>
                {SPLIT_OPTIONS.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
              <Input
                placeholder="Label name"
                value={labelName}
                onChange={(e) => setLabelName(e.target.value)}
              />
            </div>
            {formError && <p className="text-xs text-destructive">{formError}</p>}
            <div className="flex gap-2">
              <Button size="sm" disabled={create.isPending} onClick={submit}>
                {create.isPending ? <Loader2 className="animate-spin" /> : null}
                Create
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setFormOpen(false)}>
                Cancel
              </Button>
            </div>
          </>
        ) : (
          <Button variant="outline" size="sm" className="self-start" disabled={atCap} onClick={() => setFormOpen(true)}>
            <Plus /> New classifier
          </Button>
        )}
        {atCap && (
          <p className="text-xs text-muted-foreground">
            You've reached the limit of {MAX_CLASSIFIERS} classifiers.
          </p>
        )}
      </div>
    </Section>
  );
}

/**
 * Per-account signature (HTML, appended at send) and auto-BCC list (M2.5).
 * The signature is edited as plain text (mirroring ComposeView's body field)
 * and converted with lib/compose's toHtml/htmlToText, the same round-trip
 * ComposeView uses to auto-apply it.
 */
function AccountPreferences({ account }: { account: ConnectedAccount }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [signature, setSignature] = useState(() => htmlToText(account.signatureHtml));
  const [autoBcc, setAutoBcc] = useState(() => account.autoBcc.join(', '));

  useEffect(() => {
    setSignature(htmlToText(account.signatureHtml));
    setAutoBcc(account.autoBcc.join(', '));
  }, [account.signatureHtml, account.autoBcc]);

  function applyUpdate(updated: ConnectedAccount) {
    queryClient.setQueryData<ConnectedAccount[]>(['accounts'], (prev) =>
      prev?.map((a) => (a.id === updated.id ? updated : a))
    );
  }

  const saveSignature = useMutation({
    mutationFn: (value: string) =>
      orMock(
        () => api.setSignature(account.id, value),
        () => updateMockSignature(account.id, value)
      ),
    onSuccess: (updated) => {
      applyUpdate(updated);
      toast({ title: 'Signature saved' });
    },
    onError: (e) =>
      toast({ title: 'Could not save the signature', description: errorMessage(e), variant: 'destructive' }),
  });

  const saveAutoBcc = useMutation({
    mutationFn: (list: string[]) =>
      orMock(
        () => api.setAutoBcc(account.id, list),
        () => updateMockAutoBcc(account.id, list)
      ),
    onSuccess: (updated) => {
      applyUpdate(updated);
      toast({ title: 'Auto-BCC saved' });
    },
    onError: (e) =>
      toast({ title: 'Could not save auto-BCC', description: errorMessage(e), variant: 'destructive' }),
  });

  const saving = saveSignature.isPending || saveAutoBcc.isPending;

  function submit() {
    saveSignature.mutate(toHtml(signature));
    const list = autoBcc
      .split(/[,;\n]/)
      .map((s) => s.trim())
      .filter(Boolean);
    saveAutoBcc.mutate(list);
  }

  return (
    <div className="border-t">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-xs text-muted-foreground hover:text-foreground"
      >
        {open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
        Signature &amp; auto-BCC
      </button>
      {open && (
        <div className="flex flex-col gap-2 px-3 pb-3">
          <label className="text-xs font-medium text-muted-foreground" htmlFor={`sig-${account.id}`}>
            Signature
          </label>
          <textarea
            id={`sig-${account.id}`}
            value={signature}
            onChange={(e) => setSignature(e.target.value)}
            rows={3}
            placeholder="Appended to every message you send from this account"
            className="w-full resize-none rounded-md border border-input bg-transparent px-2.5 py-1.5 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />
          <label className="text-xs font-medium text-muted-foreground" htmlFor={`bcc-${account.id}`}>
            Auto-BCC
          </label>
          <Input
            id={`bcc-${account.id}`}
            value={autoBcc}
            onChange={(e) => setAutoBcc(e.target.value)}
            placeholder="name@example.com, name2@example.com"
          />
          <Button size="sm" className="self-start" disabled={saving} onClick={submit}>
            {saving ? <Loader2 className="animate-spin" /> : null}
            Save
          </Button>
        </div>
      )}
    </div>
  );
}

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

  // --- Task 9: global shortcuts toggle (persisted; flips the host live) ---
  const [globalShortcuts, setGlobalShortcuts] = useState(() => globalShortcutsEnabled());
  function handleGlobalShortcutsChange(enabled: boolean) {
    setGlobalShortcuts(enabled);
    void setGlobalShortcutsEnabled(enabled);
  }
  // --- end Task 9 ---

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
                <li key={account.id}>
                  <div className="flex items-center gap-2 p-3">
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
                  </div>
                  <AccountPreferences account={account} />
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

        {config?.features?.ai && <ClassifiersSection />}

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

        {/* --- Task 9: global shortcuts toggle --- */}
        <Section title="Shortcuts">
          <div className="flex items-center gap-3 p-3">
            <Keyboard className="size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">Global shortcuts</div>
              <p className="text-xs text-muted-foreground">
                {isMac ? '⌘⇧C composes, ⌘⇧K searches' : 'Ctrl+Shift+C composes, Ctrl+Shift+K searches'}{' '}
                — system-wide, even while Calendium is in the background.
              </p>
            </div>
            <input
              type="checkbox"
              checked={globalShortcuts}
              onChange={(e) => handleGlobalShortcutsChange(e.target.checked)}
              aria-label="Enable global shortcuts"
            />
          </div>
        </Section>
        {/* --- end Task 9 --- */}

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
