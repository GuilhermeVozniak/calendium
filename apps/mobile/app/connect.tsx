import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import {
  CLOUD_PRESET,
  DEMO_CONFIG,
  discoverServer,
  useServerConfig,
  type ServerConfig,
} from '@/lib/server-config';
import { Stack, useRouter } from 'expo-router';
import { CheckCircle2Icon, CloudIcon, PlayIcon, ServerIcon } from 'lucide-react-native';
import * as React from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  View,
} from 'react-native';

export default function ConnectScreen() {
  const router = useRouter();
  const { save } = useServerConfig();
  const [url, setUrl] = React.useState(process.env.EXPO_PUBLIC_API_URL ?? '');
  const [busy, setBusy] = React.useState<null | 'server' | 'cloud'>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [connected, setConnected] = React.useState<ServerConfig | null>(null);

  const connect = async (target: string, kind: 'server' | 'cloud') => {
    if (!target.trim()) {
      setError('Enter your Calendium server URL.');
      return;
    }
    setError(null);
    setBusy(kind);
    try {
      const config = await discoverServer(target);
      await save(config);
      setConnected(config);
    } catch (e) {
      setError(
        e instanceof Error
          ? `Couldn't reach a Calendium server there. ${e.message}`
          : "Couldn't reach a Calendium server there."
      );
    } finally {
      setBusy(null);
    }
  };

  // Explicit opt-in to the offline demo (deterministic mock data, no backend).
  const tryDemo = async () => {
    setError(null);
    await save(DEMO_CONFIG);
    setConnected(DEMO_CONFIG);
  };

  if (connected) {
    return (
      <>
        <Stack.Screen options={{ title: '', headerShown: false }} />
        <View className="flex-1 items-center justify-center gap-8 bg-background p-6">
          <View className="items-center gap-4">
            <View className="size-16 items-center justify-center rounded-2xl bg-primary">
              <Icon as={CheckCircle2Icon} className="size-8 text-primary-foreground" />
            </View>
            <View className="items-center gap-1.5">
              <Text variant="h3">{connected.name}</Text>
              <Text className="text-sm text-muted-foreground">
                {connected.mode === 'self_host' ? 'Self-hosted instance' : 'Calendium Cloud'}
              </Text>
              <Text className="text-xs text-muted-foreground">{connected.serverUrl}</Text>
            </View>
          </View>
          <Button className="w-full max-w-xs" onPress={() => router.replace('/')}>
            <Text>Continue to sign in</Text>
          </Button>
        </View>
      </>
    );
  }

  return (
    <>
      <Stack.Screen options={{ title: '', headerShown: false }} />
      <KeyboardAvoidingView
        className="flex-1 bg-background"
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <ScrollView
          contentContainerClassName="flex-grow justify-center gap-10 p-6"
          keyboardShouldPersistTaps="handled">
          {/* Brand mark */}
          <View className="items-center gap-4">
            <View className="size-16 items-center justify-center rounded-2xl bg-primary shadow-sm shadow-black/10">
              <Icon as={ServerIcon} className="size-8 text-primary-foreground" />
            </View>
            <View className="items-center gap-1.5">
              <Text variant="h1">Connect to your server</Text>
              <Text className="text-center text-sm text-muted-foreground">
                Point Calendium at your own server, or use Calendium Cloud.
              </Text>
            </View>
          </View>

          <View className="w-full max-w-sm gap-4 self-center">
            <View className="gap-1.5">
              <Text className="text-sm font-medium">Server URL</Text>
              <Input
                value={url}
                onChangeText={(t) => {
                  setUrl(t);
                  setError(null);
                }}
                placeholder="https://calendium.your-domain.com"
                autoCapitalize="none"
                autoCorrect={false}
                keyboardType="url"
                inputMode="url"
                editable={busy === null}
                onSubmitEditing={() => connect(url, 'server')}
                returnKeyType="go"
              />
              {error ? <Text className="text-sm text-destructive">{error}</Text> : null}
            </View>

            <Button
              className="flex-row gap-2"
              onPress={() => connect(url, 'server')}
              disabled={busy !== null}>
              {busy === 'server' ? (
                <ActivityIndicator size="small" />
              ) : (
                <Icon as={ServerIcon} className="size-4 text-primary-foreground" />
              )}
              <Text>Connect</Text>
            </Button>

            <View className="flex-row items-center gap-3">
              <View className="h-px flex-1 bg-border" />
              <Text className="text-xs uppercase tracking-wider text-muted-foreground">or</Text>
              <View className="h-px flex-1 bg-border" />
            </View>

            <Button
              variant="outline"
              className="flex-row gap-2"
              onPress={() => connect(CLOUD_PRESET.serverUrl, 'cloud')}
              disabled={busy !== null}>
              {busy === 'cloud' ? (
                <ActivityIndicator size="small" />
              ) : (
                <Icon as={CloudIcon} className="size-4" />
              )}
              <Text>Use Calendium Cloud</Text>
            </Button>

            <Button
              variant="ghost"
              size="sm"
              className="flex-row gap-2"
              onPress={tryDemo}
              disabled={busy !== null}>
              <Icon as={PlayIcon} className="size-4 text-muted-foreground" />
              <Text className="text-muted-foreground">Try the demo (sample data)</Text>
            </Button>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </>
  );
}
