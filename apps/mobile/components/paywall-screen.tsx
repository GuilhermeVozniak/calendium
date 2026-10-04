import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { LockIcon, LogOutIcon, RefreshCwIcon } from 'lucide-react-native';
import { View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

/**
 * Store-compliant paywall (App Store 3.1.1/3.1.3(b), docs/payments.md): the
 * iOS/Android app shows no price, no purchase call to action and no link to
 * any purchase or billing page — only neutral status, a re-check and sign-out.
 * Sync keeps running underneath; only the tabs are replaced.
 */
export function PaywallScreen({
  onRefresh,
  refreshing = false,
}: {
  /** Re-checks the subscription (refetches ['subscription']). */
  onRefresh: () => void;
  refreshing?: boolean;
}) {
  const { signOut } = useAuth();
  const insets = useSafeAreaInsets();
  return (
    <View
      className="flex-1 items-center justify-center gap-4 bg-background px-6"
      style={{ paddingTop: insets.top, paddingBottom: insets.bottom }}>
      <Icon as={LockIcon} className="size-8 text-muted-foreground" />
      <Text accessibilityRole="header" className="text-center text-lg font-semibold">
        This account doesn't have an active subscription.
      </Text>
      <Text className="text-center text-sm text-muted-foreground">
        Your mail and calendar keep syncing. Sign in with an account that has an active
        subscription to continue.
      </Text>
      <Button
        variant="outline"
        size="sm"
        className="flex-row gap-2"
        disabled={refreshing}
        onPress={onRefresh}>
        <Icon as={RefreshCwIcon} className="size-4" />
        <Text>Refresh</Text>
      </Button>
      <Button variant="ghost" size="sm" className="flex-row gap-2" onPress={() => void signOut()}>
        <Icon as={LogOutIcon} className="size-4" />
        <Text>Sign out</Text>
      </Button>
    </View>
  );
}
