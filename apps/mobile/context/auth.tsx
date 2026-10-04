import type React from 'react';
import { createContext, useCallback, useEffect, useState } from 'react';
import { unregisterPushDevice } from '@/hooks/use-push-registration';
import { api } from '@/lib/api';
import type { AuthUser } from '@/lib/auth-client';
import { clearOfflineState } from '@/lib/offline';
import { queryClient } from '@/lib/query-client';
import { useServerConfig, verifyEmailCallbackUrl } from '@/lib/server-config';
import { Alert } from 'react-native';
import { useAssertedContext } from './use-aserted-context';

type SocialProvider = 'google' | 'apple';

/** Synthetic identity for the offline "Try the demo" experience (no real auth). */
const DEMO_USER: AuthUser = {
  id: 'demo-user',
  email: 'you@calendium.app',
  name: 'Calendium Demo',
  image: null,
};

const VERIFY_FIRST_MESSAGE = 'Verify your email first — we sent a new link.';

/** Shared wording for Better Auth client errors (mirrors the web app and desktop). */
function describeAuthError(
  error: { status?: number; code?: string; message?: string },
  fallback: string,
  retryAfter: string | null
): string {
  if (error.status === 429) {
    const n = Number(retryAfter);
    return `Too many attempts, try again in ${Number.isFinite(n) && n > 0 ? Math.ceil(n) : 60} s`;
  }
  if (error.code === 'EMAIL_NOT_VERIFIED') return VERIFY_FIRST_MESSAGE;
  return error.message ?? fallback;
}

/** Captures X-Retry-After from a Better Auth client call's onError hook. */
function retryAfterCapture() {
  let value: string | null = null;
  return {
    fetchOptions: {
      onError: (ctx: { response: Response }) => {
        value = ctx.response.headers.get('x-retry-after');
      },
    },
    get value() {
      return value;
    },
  };
}

interface AuthContextType {
  user: AuthUser | null;
  loading: boolean;
  isAuthenticated: boolean;
  signInWithOAuth: (provider: SocialProvider) => Promise<void>;
  signInWithEmail: (email: string, password: string) => Promise<void>;
  signUpWithEmail: (name: string, email: string, password: string) => Promise<{ verificationRequired: boolean }>;
  signOut: () => Promise<void>;
}

/**
 * AuthContext
 */
const AuthContext = createContext<AuthContextType | undefined>(undefined);
AuthContext.displayName = 'AuthContext';

/**
 * AuthProvider component
 *
 * Better Auth is the identity provider (docs/architecture.md). The client is
 * built from the runtime-discovered server (lib/server-config) and is null
 * until the user connects to a server; every auth call guards on it. Session
 * state is derived by asking Better Auth for the current session and re-reading
 * it after each auth action (the @better-auth/expo adapter persists the session
 * in expo-secure-store, so it survives restarts).
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  const { authClient, config, clear: clearServer } = useServerConfig();
  const demoMode = config?.demoMode ?? false;
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);

  // Pull the current session from Better Auth and mirror it into local state.
  const refresh = useCallback(async () => {
    if (demoMode) {
      // Demo mode has no real backend/session — surface the synthetic user.
      setUser(DEMO_USER);
      setLoading(false);
      return;
    }
    if (!authClient) {
      setUser(null);
      setLoading(false);
      return;
    }
    try {
      const { data } = await authClient.getSession();
      const u = data?.user;
      setUser(
        u ? { id: u.id, email: u.email, name: u.name ?? null, image: u.image ?? null } : null
      );
    } catch {
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, [authClient, demoMode]);

  useEffect(() => {
    setLoading(true);
    refresh();
    // Re-derive session whenever the configured server (and thus client) changes.
  }, [refresh]);

  const signInWithOAuth = async (provider: SocialProvider) => {
    if (!authClient) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return;
    }
    try {
      // @better-auth/expo opens the system browser and completes the deep-link
      // callback (scheme "calendium://"); the relative callbackURL is rewritten
      // into a deep link automatically.
      const { error } = await authClient.signIn.social({ provider, callbackURL: '/' });
      if (error) {
        Alert.alert('Error', error.message ?? 'Sign in failed.');
        throw new Error(error.message ?? 'Sign in failed.');
      }
      await refresh();
    } catch (error) {
      console.error('OAuth sign in error:', error);
      const message = error instanceof Error ? error.message : 'Unexpected error during sign in.';
      Alert.alert('Error', message);
      throw error;
    }
  };

  const signInWithEmail = async (email: string, password: string) => {
    if (!authClient) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return;
    }
    const retry = retryAfterCapture();
    const { error } = await authClient.signIn.email({ email, password }, retry.fetchOptions);
    if (error) {
      const message = describeAuthError(error, 'Sign in failed.', retry.value);
      Alert.alert(error.code === 'EMAIL_NOT_VERIFIED' ? 'Verify your email' : 'Error', message);
      throw new Error(message);
    }
    await refresh();
  };

  const signUpWithEmail = async (name: string, email: string, password: string) => {
    if (!authClient || !config) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return { verificationRequired: false };
    }
    const retry = retryAfterCapture();
    const { data, error } = await authClient.signUp.email(
      { name, email, password, callbackURL: verifyEmailCallbackUrl(config) },
      retry.fetchOptions
    );
    if (error) {
      const message = describeAuthError(error, 'Sign up failed.', retry.value);
      Alert.alert('Error', message);
      throw new Error(message);
    }
    // With SMTP configured the server never signs a new account in (and
    // answers the same for an existing address): token === null means the
    // user must open the emailed link before signing in.
    if (data && data.token === null) return { verificationRequired: true };
    await refresh();
    return { verificationRequired: false };
  };

  const signOut = async () => {
    try {
      // Remove this device's push token before dropping the session.
      await unregisterPushDevice();
      if (demoMode) {
        // Leaving the demo drops the (fake) config and returns to server pick.
        await clearServer();
      } else if (authClient) {
        await authClient.signOut();
      }
    } catch (error) {
      console.error('Sign out error:', error);
      throw error;
    } finally {
      setUser(null);
      // Drop the previous account's cached mail/calendar so the next sign-in
      // on this device never briefly renders someone else's data — and the
      // durable offline outbox, so queued actions never replay as the next user.
      // …and the cached API JWT, so the next account never rides this one's token.
      api.invalidateAccessToken();
      queryClient.clear();
      await clearOfflineState();
    }
  };

  const value = {
    user,
    loading,
    isAuthenticated: !!user,
    signInWithOAuth,
    signInWithEmail,
    signUpWithEmail,
    signOut,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
AuthProvider.displayName = 'AuthProvider';

/**
 * useAuth hook
 */
const useAuth = () => useAssertedContext(AuthContext);
useAuth.displayName = 'useAuth';

export default useAuth;
