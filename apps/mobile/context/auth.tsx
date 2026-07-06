import React, { createContext, useCallback, useEffect, useState } from 'react';
import { unregisterPushDevice } from '@/hooks/use-push-registration';
import type { AuthUser } from '@/lib/auth-client';
import { useServerConfig } from '@/lib/server-config';
import { Alert } from 'react-native';
import { useAssertedContext } from './use-aserted-context';

type SocialProvider = 'google' | 'apple';

interface AuthContextType {
  user: AuthUser | null;
  loading: boolean;
  isAuthenticated: boolean;
  signInWithOAuth: (provider: SocialProvider) => Promise<void>;
  signInWithEmail: (email: string, password: string) => Promise<void>;
  signUpWithEmail: (name: string, email: string, password: string) => Promise<void>;
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
  const { authClient } = useServerConfig();
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);

  // Pull the current session from Better Auth and mirror it into local state.
  const refresh = useCallback(async () => {
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
  }, [authClient]);

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
    const { error } = await authClient.signIn.email({ email, password });
    if (error) {
      Alert.alert('Error', error.message ?? 'Sign in failed.');
      throw new Error(error.message ?? 'Sign in failed.');
    }
    await refresh();
  };

  const signUpWithEmail = async (name: string, email: string, password: string) => {
    if (!authClient) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return;
    }
    const { error } = await authClient.signUp.email({ name, email, password });
    if (error) {
      Alert.alert('Error', error.message ?? 'Sign up failed.');
      throw new Error(error.message ?? 'Sign up failed.');
    }
    await refresh();
  };

  const signOut = async () => {
    try {
      // Remove this device's push token before dropping the session.
      await unregisterPushDevice();
      if (authClient) {
        await authClient.signOut();
      }
    } catch (error) {
      console.error('Sign out error:', error);
      throw error;
    } finally {
      setUser(null);
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
