import { PaywallScreen } from '@/components/paywall-screen';
import useAuth from '@/context/auth';
import { usePushRegistration } from '@/hooks/use-push-registration';
import { api, onPaymentRequired } from '@/lib/api';
import { mockSubscription, withMockFallback } from '@/lib/mock';
import { useServerConfig } from '@/lib/server-config';
import { THEME } from '@/lib/theme';
import { subscriptionDenialReason } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Redirect, Tabs, useRouter } from 'expo-router';
import {
  CalendarDaysIcon,
  InboxIcon,
  ListTodoIcon,
  Settings2Icon,
  SparklesIcon,
} from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import { useEffect } from 'react';
import { ActivityIndicator, View } from 'react-native';

export default function TabsLayout() {
  const { user, loading } = useAuth();
  const { config } = useServerConfig();
  const router = useRouter();
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme ?? 'light'];
  const aiEnabled = config?.features?.ai ?? false;

  // Billing gate (docs/payments.md): fetching the subscription first also
  // grants the signup trial server-side, so no gated call can race it.
  const billingEnabled = config?.features?.billing ?? false;
  const subscriptionQuery = useQuery({
    queryKey: ['subscription'],
    queryFn: () =>
      withMockFallback(
        () => api.getSubscription(),
        () => mockSubscription
      ),
    enabled: !!user && billingEnabled,
    retry: 1,
    staleTime: 60_000,
    // Re-check on every foreground (lib/app-focus) even within staleTime, so a
    // user who just subscribed elsewhere is not left paywalled.
    refetchOnWindowFocus: 'always',
  });

  // Any 402 from the API client means the server now denies access (e.g. the
  // trial ended while the app stayed open): re-check right away so the user
  // lands on the paywall instead of per-screen errors. cancelRefetch:false
  // joins an in-flight fetch, so a burst of 402s can never loop.
  const queryClient = useQueryClient();
  useEffect(
    () =>
      onPaymentRequired(() => {
        void queryClient.invalidateQueries({ queryKey: ['subscription'] }, { cancelRefetch: false });
      }),
    [queryClient]
  );

  usePushRegistration();

  if (loading || (billingEnabled && !!user && subscriptionQuery.isPending)) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator size="large" />
      </View>
    );
  }

  if (!user) {
    return <Redirect href="/" />;
  }

  // Fail open on fetch errors: an unreachable billing API never locks a user out.
  const paywallReason =
    billingEnabled && subscriptionQuery.data
      ? subscriptionDenialReason(subscriptionQuery.data)
      : null;
  if (paywallReason) {
    return (
      <PaywallScreen
        onRefresh={() => void subscriptionQuery.refetch()}
        refreshing={subscriptionQuery.isFetching}
      />
    );
  }

  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: theme.foreground,
        tabBarInactiveTintColor: theme.mutedForeground,
        tabBarStyle: {
          backgroundColor: theme.background,
          borderTopColor: theme.border,
        },
      }}>
      <Tabs.Screen
        name="inbox"
        options={{
          title: 'Inbox',
          tabBarIcon: ({ color, size }) => <InboxIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="calendar"
        options={{
          title: 'Calendar',
          tabBarIcon: ({ color, size }) => <CalendarDaysIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="tasks"
        options={{
          title: 'Tasks',
          tabBarIcon: ({ color, size }) => <ListTodoIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: 'Settings',
          tabBarIcon: ({ color, size }) => <Settings2Icon color={color} size={size ?? 22} />,
        }}
      />
      {/* Mobile has no persistent AI sidebar; Ask AI is a modal reached from
          this tab bar button instead (hidden when the server disables AI). */}
      {aiEnabled && (
        <Tabs.Screen
          name="ask-ai"
          options={{
            title: 'Ask AI',
            tabBarIcon: ({ color, size }) => <SparklesIcon color={color} size={size ?? 22} />,
          }}
          listeners={{
            tabPress: (e) => {
              e.preventDefault();
              router.push('/ask-ai');
            },
          }}
        />
      )}
    </Tabs>
  );
}
