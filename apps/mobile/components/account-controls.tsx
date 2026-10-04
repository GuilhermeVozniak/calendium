import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { useServerConfig, webOrigin } from '@/lib/server-config';
import * as Linking from 'expo-linking';
import { useRouter } from 'expo-router';
import { DownloadIcon, ExternalLinkIcon, Trash2Icon } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, Pressable, View } from 'react-native';

type DeleteError = {
  status?: number;
  code?: string;
  details?: { teams?: { id: string; name: string }[] };
};

/**
 * Settings → Account (account lifecycle): "Download my data" links out to the
 * web account page; "Delete account" runs in-app (App Store 5.1.1(v)) through
 * Better Auth's deleteUser. Rendered by the Settings tab and by the paywall,
 * so a user without an active subscription can still export and delete.
 * Shows no price and no purchase link.
 */
export function AccountControls({ title = 'Account' }: { title?: string }) {
  const router = useRouter();
  const { signOut, signInWithOAuth } = useAuth();
  const { config, authClient } = useServerConfig();
  const demoMode = config?.demoMode ?? false;

  const [deleting, setDeleting] = React.useState(false);
  const [deletePassword, setDeletePassword] = React.useState('');
  const [hasCredential, setHasCredential] = React.useState<boolean | null>(null);
  const [socialProvider, setSocialProvider] = React.useState<'google' | 'apple' | null>(null);
  const [deleteBusy, setDeleteBusy] = React.useState(false);

  const openDataExport = () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    const origin = webOrigin(config);
    if (!origin) {
      Alert.alert('No web app', 'This server has no web address to open.');
      return;
    }
    void Linking.openURL(`${origin}/settings?tab=account`);
  };

  const startDelete = async () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    if (!authClient) return;
    setDeleting(true);
    setHasCredential(null);
    setDeletePassword('');
    try {
      const { data } = await authClient.listAccounts();
      // Better Auth's list-accounts rows carry the provider as `providerId`
      // ('credential' = email + password).
      const providers = (data ?? []).map((a) => a.providerId);
      setHasCredential(providers.includes('credential'));
      const social = providers.find((p) => p === 'google' || p === 'apple');
      setSocialProvider((social as 'google' | 'apple' | undefined) ?? null);
    } catch {
      setHasCredential(false);
    }
  };

  const runDelete = async () => {
    if (!authClient) return;
    setDeleteBusy(true);
    try {
      const { error } = await authClient.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        const err = error as DeleteError;
        const teams = err.details?.teams ?? [];
        if (err.code === 'owns_teams' || teams.length > 0) {
          Alert.alert('Transfer your teams first', teams.map((t) => `• ${t.name}`).join('\n'));
        } else if (err.code === 'INVALID_PASSWORD' || err.status === 400 || err.status === 401) {
          Alert.alert('Incorrect password', 'Check your password and try again.');
        } else if (err.status === 403) {
          Alert.alert('Sign in again', 'For your security, sign in again and then retry.', [
            { text: 'Cancel', style: 'cancel' },
            {
              text: 'Sign in',
              onPress: () => {
                if (socialProvider) void signInWithOAuth(socialProvider).catch(() => undefined);
              },
            },
          ]);
        } else {
          Alert.alert('Could not delete your account', 'Try again.');
        }
        return;
      }
      // The session is gone server-side; signOut still clears this device's
      // push token, query cache and offline outbox (context/auth.tsx).
      await signOut().catch(() => undefined);
      router.replace('/');
    } catch {
      Alert.alert('Could not delete your account', 'Try again.');
    } finally {
      setDeleteBusy(false);
    }
  };

  const confirmDelete = () => {
    Alert.alert(
      'Delete your account?',
      'This permanently deletes your Calendium account and its data. There is no undo.',
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Delete', style: 'destructive', onPress: () => void runDelete() },
      ]
    );
  };

  return (
    <View className="gap-2">
      <Text className="px-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
        {title}
      </Text>
      <View className="overflow-hidden rounded-lg border border-border bg-card">
        <Pressable
          accessibilityRole="button"
          onPress={openDataExport}
          className="flex-row items-center justify-between p-4 active:bg-accent">
          <View className="flex-row items-center gap-3">
            <Icon as={DownloadIcon} className="size-5 text-muted-foreground" />
            <Text className="text-sm font-medium">Download my data</Text>
          </View>
          <Icon as={ExternalLinkIcon} className="size-4 text-muted-foreground" />
        </Pressable>
        {!deleting ? (
          <Pressable
            accessibilityRole="button"
            onPress={() => void startDelete()}
            className="flex-row items-center gap-3 border-t border-border p-4 active:bg-accent">
            <Icon as={Trash2Icon} className="size-5 text-destructive" />
            <Text className="text-sm font-medium text-destructive">Delete account</Text>
          </Pressable>
        ) : (
          <View className="gap-3 border-t border-border p-4">
            <Text className="text-sm text-muted-foreground">
              This permanently deletes your account: mirrored mail and calendars, drafts, snippets,
              tasks, settings and any team where you are the only member. There is no undo.
            </Text>
            {hasCredential && (
              <Input
                placeholder="Your password"
                secureTextEntry
                autoCapitalize="none"
                value={deletePassword}
                onChangeText={setDeletePassword}
              />
            )}
            <View className="flex-row gap-2">
              <Button
                variant="outline"
                className="flex-1"
                onPress={() => setDeleting(false)}
                disabled={deleteBusy}>
                <Text>Cancel</Text>
              </Button>
              <Button
                variant="destructive"
                className="flex-1"
                onPress={confirmDelete}
                disabled={
                  deleteBusy ||
                  hasCredential === null ||
                  (hasCredential && deletePassword.length === 0)
                }>
                {deleteBusy ? <ActivityIndicator /> : <Text>Delete my account</Text>}
              </Button>
            </View>
          </View>
        )}
      </View>
    </View>
  );
}
