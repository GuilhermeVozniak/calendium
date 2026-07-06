import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { api } from '@/lib/api';
import { formatDate } from '@/lib/format';
import { useServerConfig } from '@/lib/server-config';
import { isApiUnreachable, mockAccounts, mockSubscription, withMockFallback } from '@/lib/mock';
import type { ConnectedAccount, Provider, Subscription } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import * as Linking from 'expo-linking';
import { useRouter } from 'expo-router';
import * as WebBrowser from 'expo-web-browser';
import {
  ExternalLinkIcon,
  LogOutIcon,
  MoonStarIcon,
  PlusIcon,
  RefreshCwIcon,
  ServerIcon,
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
  const { user, signOut } = useAuth();
  const { config, clear: clearServer } = useServerConfig();
  const { colorScheme, toggleColorScheme } = useColorScheme();
  const [connecting, setConnecting] = React.useState<Provider | null>(null);

  const isSelfHost = config?.mode === 'self_host';

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
            </View>
          ))
        )}
        <View className="flex-row gap-2 border-t border-border p-3">
          {(['google', 'microsoft'] as const).map((provider) => (
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
  if (!subscription) {
    return <Text className="text-sm text-muted-foreground">Couldn't load subscription.</Text>;
  }

  const { status } = subscription;
  const unsubscribed = status === 'none' || status === 'expired';

  const statusLine = (() => {
    switch (status) {
      case 'trialing':
        return subscription.trialEndsAt
          ? `Free trial · ends ${formatDate(subscription.trialEndsAt)}`
          : 'Free trial';
      case 'active':
        return subscription.currentPeriodEnd
          ? `Calendium Pro · ${subscription.cancelAtPeriodEnd ? 'ends' : 'renews'} ${formatDate(subscription.currentPeriodEnd)}`
          : 'Calendium Pro · active';
      case 'past_due':
        return 'Payment issue — we are retrying your card';
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
      {unsubscribed ? (
        <>
          <Text className="text-sm text-muted-foreground">
            Calendium Pro is managed on the web — there are no purchases in this app.
          </Text>
          <Button
            variant="outline"
            size="sm"
            className="flex-row gap-2 self-start"
            onPress={() => WebBrowser.openBrowserAsync('https://calendium.app/pricing')}>
            <Icon as={ExternalLinkIcon} className="size-4" />
            <Text>Manage on the web</Text>
          </Button>
        </>
      ) : (
        <Text className="text-xs text-muted-foreground">
          Manage your plan anytime at calendium.app
        </Text>
      )}
    </>
  );
}
