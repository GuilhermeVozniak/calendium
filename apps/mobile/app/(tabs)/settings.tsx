import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { api } from '@/lib/api';
import { formatDate } from '@/lib/format';
import { useServerConfig } from '@/lib/server-config';
import {
  isApiUnreachable,
  mockAccounts,
  mockSetAutoBcc,
  mockSetSignature,
  mockSubscription,
  withMockFallback,
} from '@/lib/mock';
import { htmlToPlainText, plainTextToHtml } from '@/lib/mail-extras';
import { applyNamedTheme, THEMES, useNamedTheme } from '@/lib/theme';
import {
  THEME_NAMES,
  type ConnectedAccount,
  hasBillingSubscription,
  type Provider,
  type Subscription,
  type ThemeName,
} from '@calendium/shared';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as Linking from 'expo-linking';
import { useRouter } from 'expo-router';
import * as WebBrowser from 'expo-web-browser';
import {
  ChevronRightIcon,
  ExternalLinkIcon,
  LogOutIcon,
  MoonStarIcon,
  PlusIcon,
  RefreshCwIcon,
  ServerIcon,
  SparklesIcon,
  SunIcon,
} from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import * as React from 'react';
import { ActivityIndicator, Alert, Image, Pressable, ScrollView, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const PROVIDER_LABEL: Record<Provider, string> = {
  google: 'Google',
  microsoft: 'Microsoft',
};

const ACCOUNT_STATUS_LABEL: Record<ConnectedAccount['status'], string> = {
  active: 'Synced',
  syncing: 'Syncing…',
  reauth_required: 'Reconnect needed',
  disconnected: 'Disconnected',
};

export default function SettingsScreen() {
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const queryClient = useQueryClient();
  const { user, signOut } = useAuth();
  const { config, clear: clearServer } = useServerConfig();
  const { colorScheme, toggleColorScheme } = useColorScheme();
  const namedTheme = useNamedTheme();
  const [connecting, setConnecting] = React.useState<Provider | null>(null);
  // Per-account signature/auto-BCC edit buffers (M2.5), keyed by account id.
  // Plain inputs only — mobile has no rich-text editor, so the stored (rich)
  // signatureHtml is shown/edited as plain text (htmlToPlainText) and
  // converted back to simple HTML on save (plainTextToHtml).
  const [drafts, setDrafts] = React.useState<
    Record<string, { signature: string; autoBcc: string }>
  >({});

  const isSelfHost = config?.mode === 'self_host';
  const demoMode = config?.demoMode ?? false;
  const aiEnabled = config?.features?.ai ?? false;
  // Only offer mail providers the server can actually connect (features flags).
  const connectProviders = (['google', 'microsoft'] as const).filter(
    (provider) => config?.features?.[provider]
  );

  const switchServer = async () => {
    await clearServer();
    router.replace('/connect');
  };

  const accountsQuery = useQuery({
    queryKey: ['accounts'],
    queryFn: () =>
      withMockFallback(
        () => api.listAccounts(),
        () => mockAccounts
      ),
  });

  // Seed each account's edit buffer the first time it's seen, so a background
  // refetch never clobbers text the user is actively editing.
  React.useEffect(() => {
    const accounts = accountsQuery.data ?? [];
    if (accounts.length === 0) return;
    setDrafts((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const account of accounts) {
        if (!(account.id in next)) {
          next[account.id] = {
            signature: htmlToPlainText(account.signatureHtml),
            autoBcc: account.autoBcc.join(', '),
          };
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [accountsQuery.data]);

  const updateAccountCache = (updated: ConnectedAccount) => {
    queryClient.setQueryData<ConnectedAccount[]>(['accounts'], (data) =>
      data ? data.map((a) => (a.id === updated.id ? updated : a)) : data
    );
  };

  const saveMailPrefsMutation = useMutation({
    // Sequenced, not Promise.all: the backend's Update is a full-row
    // read-modify-write, so two concurrent PUTs (signature, auto-BCC) each
    // read the row before either write lands and the second write clobbers
    // the first's change (lost update). Awaiting signature first means its
    // write is committed before auto-BCC reads the row, so the final
    // response — cached below — reflects both fields.
    //
    // The payload is computed at the call site (see saveMailPrefs below) and
    // passed in here rather than read from `drafts` inside this closure:
    // react-query applies a fresh mutationFn to its observer via a
    // useEffect, so a mutationFn that reads component state directly can
    // observe a render that is one commit behind the button press that
    // triggered it. Accepting the already-computed values sidesteps that
    // staleness entirely.
    mutationFn: async (input: { accountId: string; signatureHtml: string; autoBcc: string[] }) => {
      await withMockFallback(
        () => api.setSignature(input.accountId, input.signatureHtml),
        () => mockSetSignature(input.accountId, input.signatureHtml)
      );
      return withMockFallback(
        () => api.setAutoBcc(input.accountId, input.autoBcc),
        () => mockSetAutoBcc(input.accountId, input.autoBcc)
      );
    },
    onSuccess: (account) => {
      updateAccountCache(account);
      Alert.alert('Saved', 'Signature and auto-BCC updated.');
    },
    onError: (error) => {
      Alert.alert(
        'Could not save',
        isApiUnreachable(error)
          ? 'Reach the Calendium API to update signature and auto-BCC.'
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      );
    },
  });

  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: () =>
      withMockFallback(
        () => api.getSubscription(),
        () => mockSubscription
      ),
    // Self-hosted instances have billing disabled — no subscription to fetch.
    enabled: !isSelfHost,
  });

  const name = user?.name ?? null;
  const avatarUrl = user?.image ?? null;
  const initials = (name ?? user?.email ?? '?')
    .split(/[\s@.]+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join('');

  // Named palette switch (M2.6 Task 13): applies + persists locally first,
  // then syncs to the server — a failed PUT keeps the local theme (honest
  // offline fallback), and demo mode never talks to a server.
  const choosePalette = (name: ThemeName) => {
    applyNamedTheme(name);
    if (demoMode) return;
    api.updatePreferences({ theme: name }).catch((error) => {
      Alert.alert(
        'Could not save theme',
        isApiUnreachable(error)
          ? 'The theme is applied on this device and can sync once the server is reachable.'
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      );
    });
  };

  const connectAccount = async (provider: Provider) => {
    setConnecting(provider);
    try {
      // The backend runs its own OAuth flow (offline access); we just open it.
      const redirectUrl = Linking.createURL('settings');
      const { url } = await api.connectAccount(provider, redirectUrl);
      await WebBrowser.openAuthSessionAsync(url, redirectUrl);
      accountsQuery.refetch();
    } catch (error) {
      Alert.alert(
        'Connect account',
        isApiUnreachable(error)
          ? 'Connecting accounts needs the Calendium API to be reachable.'
          : error instanceof Error
            ? error.message
            : 'Unknown error'
      );
    } finally {
      setConnecting(null);
    }
  };

  return (
    <ScrollView
      className="flex-1 bg-background"
      style={{ paddingTop: insets.top }}
      contentContainerClassName="gap-6 px-4 pb-12">
      <View className="flex-row items-center justify-between pt-1">
        <Text variant="h3">Settings</Text>
      </View>

      {/* Profile */}
      <View className="flex-row items-center gap-3 rounded-lg border border-border bg-card p-4">
        {avatarUrl ? (
          <Image source={{ uri: avatarUrl }} className="size-12 rounded-full" />
        ) : (
          <View className="size-12 items-center justify-center rounded-full bg-secondary">
            <Text className="font-semibold text-secondary-foreground">{initials}</Text>
          </View>
        )}
        <View className="flex-1">
          <Text className="font-semibold" numberOfLines={1}>
            {name ?? 'Calendium user'}
          </Text>
          <Text className="text-sm text-muted-foreground" numberOfLines={1}>
            {user?.email}
          </Text>
        </View>
      </View>

      {/* Connected accounts */}
      <Section title="Connected accounts">
        {accountsQuery.isLoading ? (
          <View className="items-center p-4">
            <ActivityIndicator />
          </View>
        ) : (
          (accountsQuery.data ?? []).map((account, i) => (
            <View
              key={account.id}
              className={i > 0 ? 'border-t border-border' : undefined}>
              <View className="flex-row items-center gap-3 p-4">
                <View className="flex-1">
                  <Text className="text-sm font-medium" numberOfLines={1}>
                    {account.email}
                  </Text>
                  <Text className="text-xs text-muted-foreground">
                    {PROVIDER_LABEL[account.provider]} · {ACCOUNT_STATUS_LABEL[account.status]}
                  </Text>
                </View>
              </View>
              <View className="gap-2 border-t border-border p-4">
                <View className="gap-1.5">
                  <Text className="text-xs font-medium text-muted-foreground">Signature</Text>
                  <Input
                    value={drafts[account.id]?.signature ?? ''}
                    onChangeText={(signature) =>
                      setDrafts((prev) => ({
                        ...prev,
                        [account.id]: { ...(prev[account.id] ?? { autoBcc: '' }), signature },
                      }))
                    }
                    placeholder={'Best,\nYour name'}
                    multiline
                    className="h-auto py-2.5"
                    style={{ textAlignVertical: 'top' }}
                    testID={`signature-input-${account.id}`}
                  />
                </View>
                <View className="gap-1.5">
                  <Text className="text-xs font-medium text-muted-foreground">
                    Auto-BCC (comma-separated)
                  </Text>
                  <Input
                    value={drafts[account.id]?.autoBcc ?? ''}
                    onChangeText={(autoBcc) =>
                      setDrafts((prev) => ({
                        ...prev,
                        [account.id]: { ...(prev[account.id] ?? { signature: '' }), autoBcc },
                      }))
                    }
                    placeholder="archive@example.com"
                    autoCapitalize="none"
                    autoCorrect={false}
                    keyboardType="email-address"
                    testID={`auto-bcc-input-${account.id}`}
                  />
                </View>
                <Button
                  variant="outline"
                  size="sm"
                  className="flex-row gap-2 self-start"
                  onPress={() => {
                    const draft = drafts[account.id] ?? { signature: '', autoBcc: '' };
                    const autoBcc = draft.autoBcc
                      .split(/[,;\s]+/)
                      .map((email) => email.trim())
                      .filter(Boolean);
                    saveMailPrefsMutation.mutate({
                      accountId: account.id,
                      signatureHtml: plainTextToHtml(draft.signature),
                      autoBcc,
                    });
                  }}
                  disabled={saveMailPrefsMutation.isPending}
                  testID={`save-mail-prefs-${account.id}`}>
                  {saveMailPrefsMutation.isPending ? <ActivityIndicator size="small" /> : null}
                  <Text>Save</Text>
                </Button>
              </View>
            </View>
          ))
        )}
        {connectProviders.length > 0 && (
          <View className="flex-row gap-2 border-t border-border p-3">
            {connectProviders.map((provider) => (
              <Button
                key={provider}
                variant="outline"
                size="sm"
                className="flex-1 flex-row gap-1.5"
                onPress={() => connectAccount(provider)}
                disabled={connecting !== null}>
                {connecting === provider ? (
                  <ActivityIndicator size="small" />
                ) : (
                  <Icon as={PlusIcon} className="size-4" />
                )}
                <Text>{PROVIDER_LABEL[provider]}</Text>
              </Button>
            ))}
          </View>
        )}
      </Section>

      {/* Server (open-core: which Calendium instance this client talks to) */}
      <Section title="Server">
        <View className="gap-3 p-4">
          <View className="flex-row items-center gap-3">
            <Icon as={ServerIcon} className="size-5 text-muted-foreground" />
            <View className="flex-1">
              <Text className="text-sm font-medium" numberOfLines={1}>
                {config?.name ?? 'Calendium'}
              </Text>
              <Text className="text-xs text-muted-foreground" numberOfLines={1}>
                {config?.serverUrl ?? '—'}
              </Text>
            </View>
            <View className="rounded-full bg-secondary px-2 py-0.5">
              <Text className="text-xs text-secondary-foreground">
                {isSelfHost ? 'Self-hosted' : 'Cloud'}
              </Text>
            </View>
          </View>
          <Button
            variant="outline"
            size="sm"
            className="flex-row gap-2 self-start"
            onPress={switchServer}>
            <Icon as={RefreshCwIcon} className="size-4" />
            <Text>Switch server</Text>
          </Button>
        </View>
      </Section>

      {/* Subscription — hidden on self-hosted instances (billing disabled). */}
      {isSelfHost ? (
        <Section title="Plan">
          <View className="gap-1 p-4">
            <Text className="text-sm font-medium">Self-hosted — all features included</Text>
            <Text className="text-xs text-muted-foreground">
              This instance runs Calendium open-source. There is no subscription to manage.
            </Text>
          </View>
        </Section>
      ) : (
        <Section title="Subscription">
          <View className="gap-3 p-4">
            {subscriptionQuery.isLoading ? (
              <ActivityIndicator />
            ) : (
              <SubscriptionCard subscription={subscriptionQuery.data ?? null} />
            )}
          </View>
        </Section>
      )}

      {/* AI — full classifier CRUD lives on its own screen (Task 17), not a
          read-only link out to the web app. */}
      {aiEnabled && (
        <Section title="AI">
          <Pressable
            onPress={() => router.push('/classifiers')}
            className="flex-row items-center justify-between p-4 active:bg-accent">
            <View className="flex-row items-center gap-3">
              <Icon as={SparklesIcon} className="size-5 text-muted-foreground" />
              <Text className="text-sm font-medium">AI classifiers</Text>
            </View>
            <Icon as={ChevronRightIcon} className="size-4 text-muted-foreground" />
          </Pressable>
        </Section>
      )}

      {/* Appearance */}
      <Section title="Appearance">
        <Pressable
          onPress={toggleColorScheme}
          className="flex-row items-center justify-between p-4 active:bg-accent">
          <View className="flex-row items-center gap-3">
            <Icon
              as={colorScheme === 'dark' ? MoonStarIcon : SunIcon}
              className="size-5 text-muted-foreground"
            />
            <Text className="text-sm font-medium">Theme</Text>
          </View>
          <Text className="text-sm capitalize text-muted-foreground">
            {colorScheme ?? 'light'}
          </Text>
        </Pressable>
        <View className="gap-2 border-t border-border p-4">
          <Text className="text-xs font-medium text-muted-foreground">Palette</Text>
          <View className="flex-row flex-wrap gap-2">
            {THEME_NAMES.map((name) => (
              <Pressable
                key={name}
                testID={`theme-${name}`}
                accessibilityState={{ selected: namedTheme === name }}
                onPress={() => choosePalette(name)}
                className={`flex-row items-center gap-2 rounded-md border px-3 py-2 active:bg-accent ${
                  namedTheme === name ? 'border-ring' : 'border-border'
                }`}>
                {/* Swatch derives from the real palette token (THEMES map). */}
                <View
                  className="size-3 rounded-full"
                  style={{ backgroundColor: THEMES[name][colorScheme ?? 'light'].primary }}
                />
                <Text className="text-sm capitalize">{name}</Text>
              </Pressable>
            ))}
          </View>
        </View>
      </Section>

      <Button variant="outline" className="flex-row gap-2" onPress={() => signOut()}>
        <Icon as={LogOutIcon} className="size-4 text-destructive" />
        <Text className="text-destructive">Sign out</Text>
      </Button>
    </ScrollView>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <View className="gap-2">
      <Text className="px-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
        {title}
      </Text>
      <View className="overflow-hidden rounded-lg border border-border bg-card">{children}</View>
    </View>
  );
}

/**
 * Read-only subscription state (docs/payments.md): the app never sells —
 * unsubscribed users get a plain link to manage the plan on the web.
 */
function SubscriptionCard({ subscription }: { subscription: Subscription | null }) {
  const { config } = useServerConfig();
  if (!subscription) {
    return <Text className="text-sm text-muted-foreground">Couldn't load subscription.</Text>;
  }

  const { status } = subscription;
  const live = hasBillingSubscription(subscription);

  const statusLine = (() => {
    switch (status) {
      case 'trialing':
        return subscription.trialEndsAt
          ? `Free trial · ends ${formatDate(subscription.trialEndsAt)}`
          : 'Free trial';
      case 'active':
        return subscription.currentPeriodEnd
          ? `Calendium Annual · ${subscription.cancelAtPeriodEnd ? 'ends' : 'renews'} ${formatDate(subscription.currentPeriodEnd)}`
          : 'Calendium Annual · active';
      case 'past_due':
        return 'Payment issue — we are retrying your card';
      case 'paused':
        return 'Paused — resume or update your payment method on the web';
      case 'canceled':
        return subscription.currentPeriodEnd
          ? `Canceled · access until ${formatDate(subscription.currentPeriodEnd)}`
          : 'Canceled';
      default:
        return 'Not subscribed';
    }
  })();

  return (
    <>
      <View>
        <Text className="text-sm font-medium">{statusLine}</Text>
        <Text className="text-xs text-muted-foreground">
          Calendium Annual · ${subscription.priceUsd}/year
        </Text>
      </View>
      <Text className="text-sm text-muted-foreground">
        {live
          ? 'Manage your plan on the web — there are no purchases in this app.'
          : 'Calendium is managed on the web — there are no purchases in this app.'}
      </Text>
      {config?.webUrl ? (
        <Button
          variant="outline"
          size="sm"
          className="flex-row gap-2 self-start"
          onPress={() =>
            WebBrowser.openBrowserAsync(
              `${config.webUrl}/${live ? 'settings?tab=billing' : 'pricing'}`
            )
          }>
          <Icon as={ExternalLinkIcon} className="size-4" />
          <Text>Manage on the web</Text>
        </Button>
      ) : null}
    </>
  );
}
