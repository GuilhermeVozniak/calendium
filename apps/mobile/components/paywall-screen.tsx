import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import type { PaymentRequiredReason } from '@calendium/shared';
import * as WebBrowser from 'expo-web-browser';
import { ExternalLinkIcon, LockIcon, LogOutIcon } from 'lucide-react-native';
import { View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

const COPY: Record<PaymentRequiredReason, { title: string; body: string }> = {
  trial_ended: {
    title: 'Your free trial has ended',
    body: 'Subscribe on the web to keep using Calendium on every device.',
  },
  none: {
    title: 'Subscribe to keep using Calendium',
    body: 'Calendium is $50/year — one plan for email + calendar everywhere.',
  },
  canceled: {
    title: 'Your subscription has ended',
    body: 'Resubscribe on the web any time to pick up where you left off.',
  },
  past_due: {
    title: 'Payment failed',
    body: 'Update your payment method on the web to restore access.',
  },
  paused: {
    title: 'Your subscription is paused',
    body: 'Resume the plan or update your payment method on the web.',
  },
};

/**
 * Read-only paywall (docs/payments.md, Spotify model): the app never sells.
 * It states why access is denied and links to the web app's pricing page
 * built from the server-advertised webUrl — never a hardcoded domain.
 */
export function PaywallScreen({
  reason,
  webUrl,
}: {
  reason: PaymentRequiredReason;
  webUrl: string | null;
}) {
  const { signOut } = useAuth();
  const insets = useSafeAreaInsets();
  const copy = COPY[reason];
  return (
    <View
      className="flex-1 items-center justify-center gap-4 bg-background px-6"
      style={{ paddingTop: insets.top, paddingBottom: insets.bottom }}>
      <Icon as={LockIcon} className="size-8 text-muted-foreground" />
      <Text className="text-center text-lg font-semibold">{copy.title}</Text>
      <Text className="text-center text-sm text-muted-foreground">{copy.body}</Text>
      <Text className="text-center text-xs text-muted-foreground">
        Calendium is managed on the web — there are no purchases in this app.
      </Text>
      {webUrl ? (
        <Button
          variant="outline"
          size="sm"
          className="flex-row gap-2"
          onPress={() => WebBrowser.openBrowserAsync(`${webUrl}/pricing`)}>
          <Icon as={ExternalLinkIcon} className="size-4" />
          <Text>Manage on the web</Text>
        </Button>
      ) : null}
      <Button variant="ghost" size="sm" className="flex-row gap-2" onPress={() => void signOut()}>
        <Icon as={LogOutIcon} className="size-4" />
        <Text>Sign out</Text>
      </Button>
    </View>
  );
}
