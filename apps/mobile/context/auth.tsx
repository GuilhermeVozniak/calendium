import React, { createContext, useEffect, useState } from 'react';
import { unregisterPushDevice } from '@/hooks/use-push-registration';
import { useServerConfig } from '@/lib/server-config';
import type { Session, User } from '@supabase/supabase-js';
import { Alert } from 'react-native';
import { makeRedirectUri } from 'expo-auth-session';
import * as QueryParams from 'expo-auth-session/build/QueryParams';
import * as WebBrowser from 'expo-web-browser';
import Constants from 'expo-constants';
import { useAssertedContext } from './use-aserted-context';

WebBrowser.maybeCompleteAuthSession();

if (!process.env.EXPO_PUBLIC_SCHEME) {
  throw new Error('Missing environment variable: EXPO_PUBLIC_SCHEME');
}

interface AuthContextType {
  user: User | null;
  session: Session | null;
  loading: boolean;
  isAuthenticated: boolean;
  signInWithOAuth: (provider: 'google' | 'apple') => Promise<void>;
  signOut: () => Promise<void>;
}

/**
 * AuthContext
 */
const AuthContext = createContext<AuthContextType | undefined>(undefined);
AuthContext.displayName = 'AuthContext';

/**
 * AuthProvider component
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  // Supabase is built from the runtime-discovered server (lib/server-config);
  // it is null until the user connects to a server. All auth calls guard on it.
  const { supabase } = useServerConfig();
  const [user, setUser] = useState<User | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  // refer to /docs/auth-supabase-deep-linking.md for more info
  const redirectMode = process.env.EXPO_PUBLIC_AUTH_REDIRECT_MODE;
  const isExpoGo = Constants.executionEnvironment === 'standalone';
  const useProxy = redirectMode === 'proxy' || (redirectMode !== 'scheme' && isExpoGo);
  const redirectTo = useProxy
    ? makeRedirectUri()
    : makeRedirectUri({
        scheme: process.env.EXPO_PUBLIC_SCHEME,
      });

  const createSessionFromUrl = async (url: string) => {
    const { params, errorCode } = QueryParams.getQueryParams(url);

    if (errorCode) {
      throw new Error(errorCode);
    }

    const { access_token, refresh_token } = params;

    if (!access_token || !refresh_token) {
      return null;
    }

    if (!supabase) {
      throw new Error('No Calendium server configured.');
    }

    const { data, error } = await supabase.auth.setSession({
      access_token,
      refresh_token,
    });

    if (error) {
      throw error;
    }

    return data.session;
  };

  useEffect(() => {
    // No server yet — nothing to authenticate against.
    if (!supabase) {
      setSession(null);
      setUser(null);
      setLoading(false);
      return;
    }

    let active = true;
    setLoading(true);

    // Get initial session
    supabase.auth.getSession().then(({ data: { session } }) => {
      if (!active) return;
      setSession(session);
      setUser(session?.user ?? null);
      setLoading(false);
    });

    // Listen for auth changes
    const {
      data: { subscription },
    } = supabase.auth.onAuthStateChange((_event, session) => {
      setSession(session);
      setUser(session?.user ?? null);
      setLoading(false);
    });

    return () => {
      active = false;
      subscription.unsubscribe();
    };
    // Re-subscribe whenever the configured server (and thus client) changes.
  }, [supabase]);

  const signInWithOAuth = async (provider: 'google' | 'apple') => {
    if (!supabase) {
      Alert.alert('Connect a server', 'Choose a Calendium server before signing in.');
      return;
    }
    try {
      const { data, error } = await supabase.auth.signInWithOAuth({
        provider,
        options: {
          redirectTo,
          skipBrowserRedirect: true,
        },
      });

      if (error) {
        Alert.alert('Error', error.message);
        throw error;
      }

      if (!data?.url) {
        throw new Error('No URL returned from sign in.');
      }

      const result = await WebBrowser.openAuthSessionAsync(data.url, redirectTo);

      if (result.type === 'success' && result.url) {
        await createSessionFromUrl(result.url);
      }
    } catch (error) {
      console.error('OAuth sign in error:', error);
      const message = error instanceof Error ? error.message : 'Unexpected error during sign in.';
      Alert.alert('Error', message);
      throw error;
    }
  };

  const signOut = async () => {
    try {
      // Remove this device's push token before dropping the session.
      await unregisterPushDevice();
      if (!supabase) return;
      const { error } = await supabase.auth.signOut();
      if (error) {
        Alert.alert('Error', error.message);
        throw error;
      }
    } catch (error) {
      console.error('Sign out error:', error);
      throw error;
    }
  };

  const value = {
    user,
    session,
    loading,
    isAuthenticated: !!user,
    signInWithOAuth,
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
