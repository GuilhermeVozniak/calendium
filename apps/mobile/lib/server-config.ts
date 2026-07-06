import { configureApi } from '@/lib/api';
import { configureSupabase, getSupabase } from '@/lib/supabase';
import { fetchInstance, type InstanceMode } from '@calendium/shared';
import AsyncStorage from '@react-native-async-storage/async-storage';
import type { SupabaseClient } from '@supabase/supabase-js';
import * as React from 'react';

/**
 * Runtime server configuration (open-core self-host vs cloud).
 *
 * The user connects to a Calendium server by URL; we fetch its public
 * `/v1/instance` descriptor and persist the resulting config to AsyncStorage.
 * The Supabase + API clients are rebuilt from this config at runtime, so a
 * single build can point at any server — a self-hosted instance or Calendium
 * Cloud.
 */

const STORAGE_KEY = 'calendium.serverConfig';

/** Calendium Cloud — our managed, paid server (docs/payments.md). */
export const CLOUD_PRESET = {
  serverUrl: 'https://api.calendium.app',
} as const;

export interface ServerConfig {
  /** Base URL of the Calendium backend the client talks to. */
  serverUrl: string;
  supabaseUrl: string;
  supabaseAnonKey: string;
  mode: InstanceMode;
  /** Human-readable instance name (INSTANCE_NAME on the server). */
  name: string;
}

/** Normalizes user-entered URLs: trims, drops trailing slash, adds https://. */
export function normalizeServerUrl(url: string): string {
  const trimmed = url.trim().replace(/\/+$/, '');
  if (!trimmed) return trimmed;
  return /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
}

export async function getStoredServerConfig(): Promise<ServerConfig | null> {
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as ServerConfig) : null;
  } catch {
    return null;
  }
}

export async function setStoredServerConfig(config: ServerConfig): Promise<void> {
  await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(config));
}

export async function clearStoredServerConfig(): Promise<void> {
  await AsyncStorage.removeItem(STORAGE_KEY);
}

/**
 * Resolves a server base URL into a full ServerConfig by fetching its public
 * `/v1/instance` descriptor. Throws (ApiRequestError / network error) when the
 * URL is not a reachable Calendium server.
 */
export async function discoverServer(serverUrl: string): Promise<ServerConfig> {
  const normalized = normalizeServerUrl(serverUrl);
  const info = await fetchInstance(normalized);
  return {
    serverUrl: normalized,
    supabaseUrl: info.supabaseUrl,
    supabaseAnonKey: info.supabaseAnonKey,
    mode: info.mode,
    name: info.name,
  };
}

/** Applies (or clears) a config on the runtime Supabase + API clients. */
function applyConfig(config: ServerConfig | null): SupabaseClient | null {
  if (config) {
    configureApi(config.serverUrl);
    return configureSupabase(config.supabaseUrl, config.supabaseAnonKey);
  }
  return configureSupabase('', '');
}

interface ServerConfigContextValue {
  config: ServerConfig | null;
  isConfigured: boolean;
  isLoading: boolean;
  /** Supabase client bound to the configured server (null when unconfigured). */
  supabase: SupabaseClient | null;
  save: (config: ServerConfig) => Promise<void>;
  clear: () => Promise<void>;
}

const ServerConfigContext = React.createContext<ServerConfigContextValue | undefined>(undefined);
ServerConfigContext.displayName = 'ServerConfigContext';

export function ServerConfigProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = React.useState<ServerConfig | null>(null);
  const [isLoading, setIsLoading] = React.useState(true);
  const [supabase, setSupabase] = React.useState<SupabaseClient | null>(() => getSupabase());

  // Rehydrate persisted config on boot and rebuild the runtime clients from it.
  React.useEffect(() => {
    let active = true;
    getStoredServerConfig().then((stored) => {
      if (!active) return;
      if (stored) {
        setSupabase(applyConfig(stored));
        setConfig(stored);
      }
      setIsLoading(false);
    });
    return () => {
      active = false;
    };
  }, []);

  const save = React.useCallback(async (next: ServerConfig) => {
    await setStoredServerConfig(next);
    setSupabase(applyConfig(next));
    setConfig(next);
  }, []);

  const clear = React.useCallback(async () => {
    await clearStoredServerConfig();
    setSupabase(applyConfig(null));
    setConfig(null);
  }, []);

  const value = React.useMemo<ServerConfigContextValue>(
    () => ({ config, isConfigured: config !== null, isLoading, supabase, save, clear }),
    [config, isLoading, supabase, save, clear]
  );

  // JSX-free (this is a .ts module): render the provider via createElement.
  return React.createElement(ServerConfigContext.Provider, { value }, children);
}
ServerConfigProvider.displayName = 'ServerConfigProvider';

export function useServerConfig(): ServerConfigContextValue {
  const ctx = React.useContext(ServerConfigContext);
  if (!ctx) {
    throw new Error('useServerConfig must be used within a ServerConfigProvider');
  }
  return ctx;
}
