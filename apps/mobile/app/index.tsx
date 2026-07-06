import AppleIcon from '@/assets/icons/apple.svg';
import GoogleIcon from '@/assets/icons/google.svg';
import { Button } from '@/components/ui/button';
import { Icon } from '@/components/ui/icon';
import { Input } from '@/components/ui/input';
import { Text } from '@/components/ui/text';
import useAuth from '@/context/auth';
import { useServerConfig } from '@/lib/server-config';
import { Redirect, Stack } from 'expo-router';
import { CalendarRangeIcon, MoonStarIcon, SunIcon } from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import * as React from 'react';
import { ActivityIndicator, KeyboardAvoidingView, Platform, ScrollView, View } from 'react-native';

const SCREEN_OPTIONS = {
  title: '',
  headerTransparent: true,
  headerRight: () => <ThemeToggle />,
};

export default function SignInScreen() {
  const { isConfigured, isLoading: serverLoading, config } = useServerConfig();
  const { user, loading: authLoading, signInWithOAuth, signInWithEmail, signUpWithEmail } =
    useAuth();
  // Only offer the social providers the connected server actually configured.
  const authProviders = config?.authProviders ?? [];
  const showGoogle = authProviders.includes('google');
  const showApple = authProviders.includes('apple');
  const showSocial = showGoogle || showApple;
  const [loading, setLoading] = React.useState(false);
  const [mode, setMode] = React.useState<'sign-in' | 'sign-up'>('sign-in');
  const [name, setName] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');

  const handleSocialLogin = async (provider: 'google' | 'apple') => {
    try {
      setLoading(true);
      await signInWithOAuth(provider);
    } catch {
      // Error already surfaced by the auth context.
    } finally {
      setLoading(false);
    }
  };

  const handleEmailAuth = async () => {
    try {
      setLoading(true);
      if (mode === 'sign-up') {
        await signUpWithEmail(name.trim(), email.trim(), password);
      } else {
        await signInWithEmail(email.trim(), password);
      }
    } catch {
      // Error already surfaced by the auth context.
    } finally {
      setLoading(false);
    }
  };

  if (serverLoading || authLoading) {
    return (
      <>
        <Stack.Screen options={SCREEN_OPTIONS} />
        <View className="flex-1 items-center justify-center bg-background">
          <ActivityIndicator size="large" />
        </View>
      </>
    );
  }

  // Server discovery gates sign-in: pick a server before authenticating.
  if (!isConfigured) {
    return <Redirect href="/connect" />;
  }

  if (user) {
    return <Redirect href="/(tabs)/inbox" />;
  }

  return (
    <>
      <Stack.Screen options={SCREEN_OPTIONS} />
      <KeyboardAvoidingView
        className="flex-1 bg-background"
        behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <ScrollView
          contentContainerClassName="flex-grow items-center justify-center gap-8 p-6"
          keyboardShouldPersistTaps="handled">
          {/* Calendium brand mark */}
          <View className="items-center gap-4">
            <View className="size-16 items-center justify-center rounded-2xl bg-primary shadow-sm shadow-black/10">
              <Icon as={CalendarRangeIcon} className="size-8 text-primary-foreground" />
            </View>
            <View className="items-center gap-1.5">
              <Text variant="h1">Calendium</Text>
              <Text className="text-center text-sm text-muted-foreground">
                Email and calendar, at the speed of thought.
              </Text>
            </View>
          </View>

          {/* Email + password */}
          <View className="w-full max-w-xs gap-3">
            {mode === 'sign-up' && (
              <Input
                value={name}
                onChangeText={setName}
                placeholder="Name"
                autoCapitalize="words"
                autoComplete="name"
                editable={!loading}
              />
            )}
            <Input
              value={email}
              onChangeText={setEmail}
              placeholder="Email"
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType="email-address"
              inputMode="email"
              autoComplete="email"
              editable={!loading}
            />
            <Input
              value={password}
              onChangeText={setPassword}
              placeholder="Password"
              secureTextEntry
              autoCapitalize="none"
              autoComplete={mode === 'sign-up' ? 'new-password' : 'current-password'}
              editable={!loading}
              onSubmitEditing={handleEmailAuth}
              returnKeyType="go"
            />
            <Button onPress={handleEmailAuth} disabled={loading}>
              <Text>{mode === 'sign-up' ? 'Create account' : 'Sign in'}</Text>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={loading}
              onPress={() => setMode((m) => (m === 'sign-in' ? 'sign-up' : 'sign-in'))}>
              <Text className="text-sm text-muted-foreground">
                {mode === 'sign-in'
                  ? "Don't have an account? Create one"
                  : 'Already have an account? Sign in'}
              </Text>
            </Button>
          </View>

          {/* Divider — only when the server advertises a social provider. */}
          {showSocial && (
            <View className="w-full max-w-xs flex-row items-center gap-3">
              <View className="h-px flex-1 bg-border" />
              <Text className="text-xs uppercase tracking-wider text-muted-foreground">or</Text>
              <View className="h-px flex-1 bg-border" />
            </View>
          )}

          {/* Social sign-in — each button gated on the server's authProviders. */}
          {showSocial && (
            <View className="w-full max-w-xs gap-3">
              {showGoogle && (
                <Button
                  onPress={() => handleSocialLogin('google')}
                  disabled={loading}
                  variant="outline"
                  className="flex-row items-center gap-3">
                  <GoogleIcon width={20} height={20} />
                  <Text>Continue with Google</Text>
                </Button>
              )}

              {showApple && (
                <Button
                  onPress={() => handleSocialLogin('apple')}
                  disabled={loading}
                  variant="outline"
                  className="flex-row items-center gap-3">
                  <AppleIcon width={20} height={20} />
                  <Text>Continue with Apple</Text>
                </Button>
              )}

              {loading && <ActivityIndicator className="mt-2" />}
            </View>
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    </>
  );
}

const THEME_ICONS = {
  light: SunIcon,
  dark: MoonStarIcon,
};

function ThemeToggle() {
  const { colorScheme, toggleColorScheme } = useColorScheme();

  return (
    <Button
      onPressIn={toggleColorScheme}
      size="icon"
      variant="ghost"
      className="ios:size-9 rounded-full web:mx-4">
      <Icon as={THEME_ICONS[colorScheme ?? 'light']} className="size-5" />
    </Button>
  );
}
