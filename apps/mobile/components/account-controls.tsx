import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { unregisterPushDevice } from '@/hooks/use-push-registration';
import { resumeApi, suspendApi } from '@/lib/api';
import { describeExportError, shareDataExport } from '@/lib/data-export';
import { queryClient } from '@/lib/query-client';
import { useServerConfig, webOrigin } from '@/lib/server-config';
import * as Linking from 'expo-linking';
import { useRouter } from 'expo-router';
import { DownloadIcon, ExternalLinkIcon, ShareIcon, Trash2Icon } from 'lucide-react-native';
import * as React from 'react';
import { ActivityIndicator, Alert, Platform, Pressable, View } from 'react-native';

type DeleteError = {
  status?: number;
  code?: string;
  message?: string;
  details?: { teams?: { id: string; name: string }[] };
};

type SocialProvider = 'google' | 'apple';
const PROVIDER_NAMES: Record<SocialProvider, string> = { google: 'Google', apple: 'Apple' };

/**
 * Settings → Account (account lifecycle). "Download my data" fetches the
 * export zip through the API and hands it to the OS share sheet on iOS/Android
 * (the web build links to the web account page). "Delete account" runs in-app
 * (App Store 5.1.1(v)) through Better Auth's deleteUser. Rendered by the
 * Settings tab and by the paywall, so a user without an active subscription
 * can still export and delete. Shows no price and no purchase link.
 */
export function AccountControls({ title = 'Account' }: { title?: string }) {
  const router = useRouter();
  const { signInWithOAuth, signOutLocally } = useAuth();
  const { config, authClient } = useServerConfig();
  const demoMode = config?.demoMode ?? false;
  const isNative = Platform.OS === 'ios' || Platform.OS === 'android';

  const [exporting, setExporting] = React.useState(false);
  const [deleting, setDeleting] = React.useState(false);
  const [deletePassword, setDeletePassword] = React.useState('');
  // Better Auth list-accounts provider ids; null while loading or after a failure.
  const [providers, setProviders] = React.useState<string[] | null>(null);
  const [accountsError, setAccountsError] = React.useState(false);
  const [deleteBusy, setDeleteBusy] = React.useState(false);

  const hasCredential = providers?.includes('credential') ?? false;
  const socialProvider =
    (providers?.find((p) => p === 'google' || p === 'apple') as SocialProvider | undefined) ?? null;

  const downloadData = async () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    if (!isNative) {
      const origin = webOrigin(config);
      if (!origin) {
        Alert.alert('No web app', 'This server has no web address to open.');
        return;
      }
      void Linking.openURL(`${origin}/settings?tab=account`);
      return;
    }
    setExporting(true);
    try {
      await shareDataExport();
    } catch (err) {
      Alert.alert('Could not export your data', describeExportError(err));
    } finally {
      setExporting(false);
    }
  };

  const loadProviders = async () => {
    if (!authClient) return;
    setProviders(null);
    setAccountsError(false);
    try {
      const { data, error } = await authClient.listAccounts();
      if (error || !data) throw new Error('listAccounts failed');
      // Better Auth's list-accounts rows carry the provider as `providerId`
      // ('credential' = email + password).
      setProviders(data.map((a) => a.providerId));
    } catch {
      // Never guess social-only: a credential user would get no password
      // field and a misleading error. Offer a retry instead.
      setAccountsError(true);
    }
  };

  const startDelete = async () => {
    if (demoMode) {
      Alert.alert('Not available in demo');
      return;
    }
    if (!authClient) {
      Alert.alert('Not connected', 'Connect to your Calendium server to delete your account.');
      return;
    }
    setDeleting(true);
    setDeletePassword('');
    await loadProviders();
  };

  /** Better Auth 400 SESSION_EXPIRED (or 403): the session is too old to delete without re-authenticating. */
  const requireReauth = (afterReauth: boolean) => {
    if (hasCredential) {
      setDeletePassword('');
      Alert.alert('Enter your password', 'For your security, enter your password to delete your account.');
      return;
    }
    if (socialProvider && !afterReauth) {
      const provider = socialProvider;
      Alert.alert(
        'Confirm it’s you',
        `For your security, sign in with ${PROVIDER_NAMES[provider]} again to delete your account.`,
        [
          { text: 'Cancel', style: 'cancel' },
          { text: 'Continue', onPress: () => void reauthAndRetry(provider) },
        ]
      );
      return;
    }
    Alert.alert('Sign in again', 'For your security, sign out, sign back in, and then delete your account.');
  };

  const reauthAndRetry = async (provider: SocialProvider) => {
    try {
      await signInWithOAuth(provider);
    } catch {
      return; // signInWithOAuth already explained the failure.
    }
    await runDelete(true);
  };

  const showDeleteError = (err: DeleteError, afterReauth: boolean) => {
    const teams = err.details?.teams ?? [];
    if (err.code === 'owns_teams' || teams.length > 0) {
      Alert.alert('Transfer your teams first', teams.map((t) => `• ${t.name}`).join('\n'));
    } else if (err.code === 'SESSION_EXPIRED' || err.status === 403) {
      requireReauth(afterReauth);
    } else if (
      err.code === 'INVALID_PASSWORD' ||
      (hasCredential && (err.status === 400 || err.status === 401))
    ) {
      Alert.alert('Incorrect password', 'Check your password and try again.');
    } else if ((err.status ?? 0) >= 500 && err.message) {
      Alert.alert('Could not delete your account', err.message);
    } else {
      Alert.alert('Could not delete your account', 'Try again.');
    }
  };

  const runDelete = async (afterReauth = false) => {
    if (!authClient) return;
    setDeleteBusy(true);
    try {
      // Stop this device's own API use while the session is still valid: drop
      // its push device, then pause every Go API request. After the delete no
      // request may carry a JWT minted for the deleted user (requireAuth →
      // EnsureUser would re-create the purged users row).
      await unregisterPushDevice();
      suspendApi();
      await queryClient.cancelQueries();
      const { error } = await authClient.deleteUser(hasCredential ? { password: deletePassword } : {});
      if (error) {
        resumeApi();
        showDeleteError(error as DeleteError, afterReauth);
        return;
      }
    } catch {
      resumeApi();
      Alert.alert('Could not delete your account', 'Try again.');
      return;
    } finally {
      setDeleteBusy(false);
    }
    // The account is gone: forget it locally with no further request. The API
    // stays paused until the next sign-in.
    await signOutLocally().catch(() => undefined);
    router.replace('/');
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
          onPress={() => void downloadData()}
          disabled={exporting}
          className="flex-row items-center justify-between p-4 active:bg-accent">
          <View className="flex-row items-center gap-3">
            <Icon as={DownloadIcon} className="size-5 text-muted-foreground" />
            <Text className="text-sm font-medium">Download my data</Text>
          </View>
          {exporting ? (
            <ActivityIndicator />
          ) : (
            <Icon
              as={isNative ? ShareIcon : ExternalLinkIcon}
              className="size-4 text-muted-foreground"
            />
          )}
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
            {accountsError && (
              <View className="gap-2">
                <Text className="text-sm text-destructive">
                  Couldn’t check how you sign in. Check your connection and try again.
                </Text>
                <Button variant="outline" size="sm" onPress={() => void loadProviders()}>
                  <Text>Retry</Text>
                </Button>
              </View>
            )}
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
                  providers === null ||
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
