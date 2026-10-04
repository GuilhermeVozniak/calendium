import { configureApi } from '@/lib/api';
import { configureAuthClient, getAuthClient, type AuthClient } from '@/lib/auth-client';
import { setDemoMode } from '@/lib/mock';
import { fetchInstance, type InstanceFeatures, type InstanceMode } from '@calendium/shared';
import AsyncStorage from '@react-native-async-storage/async-storage';
import * as React from 'react';

/**
 * Runtime server configuration (open-core self-host vs cloud).
 *
 * The user connects to a Calendium server by URL; we fetch its public
 * `/v1/instance` descriptor and persist the resulting config to AsyncStorage.
 * The Better Auth + API clients are rebuilt from this config at runtime, so a
 * single build can point at any server — a self-hosted instance or Calendium
 * Cloud.
 */

const STORAGE_KEY = 'calendium.serverConfig';

/**
 * Resolves a build-time EXPO_PUBLIC_* host override: blank/unset falls back,
 * anything else is normalized like a user-entered URL (scheme added,
 * trailing slash dropped).
 */
export function envUrl(value: string | undefined, fallback: string): string {
  const normalized = normalizeServerUrl(value ?? '');
  return normalized || fallback;
}

/**
 * Calendium Cloud — our managed, paid server (docs/payments.md). This is only
 * the bootstrap URL: after discovery the client follows `authBaseUrl` and
 * `webUrl` from /v1/instance. White-label hosts override it at build time.
 */
export const CLOUD_PRESET = {
  serverUrl: envUrl(process.env.EXPO_PUBLIC_CLOUD_API_URL, 'https://api.calendium.app'),
} as const;

export interface ServerConfig {
  /** Base URL of the Calendium backend the client talks to. */
  serverUrl: string;
  /** Better Auth base URL (e.g. https://host/api/auth) discovered from /v1/instance. */
  authBaseUrl: string;
  mode: InstanceMode;
  /** Human-readable instance name (INSTANCE_NAME on the server). */
  name: string;
  /**
   * Enabled sign-in methods (e.g. ["email","google","apple"]), from
   * /v1/instance. Clients hide social buttons the server didn't configure.
   */
  authProviders: string[];
  /** Feature flags from /v1/instance so the UI hides what the server can't do. */
  features: InstanceFeatures;
  /**
   * Public web app origin from /v1/instance. Empty on pre-webUrl servers. The
   * iOS/Android app never builds purchase or billing links from it (App Store
   * 3.1.1/3.1.3).
   */
  webUrl: string;
  /**
   * Explicit "Try the demo" mode: the app runs on deterministic mock data with
   * no backend. Only ever set by the demo button on the connect screen; absent
   * for every real self-host / Cloud connection.
   */
  demoMode?: boolean;
}

/** Demo label only — demo never dials out (lib/mock); configurable for white-label hosts. */
const DEMO_SERVER_URL = envUrl(process.env.EXPO_PUBLIC_DEMO_SERVER_URL, 'https://demo.calendium.app');

/**
 * Ready-made config for the offline "Try the demo" experience. It points at no
 * real server — every screen reads the mock data in lib/mock and auth is
 * short-circuited to a demo user. This is the ONLY place mock data is enabled.
 */
export const DEMO_CONFIG: ServerConfig = {
  serverUrl: DEMO_SERVER_URL,
  authBaseUrl: `${DEMO_SERVER_URL}/api/auth`,
  mode: 'cloud',
  name: 'Calendium Demo',
  authProviders: ['email', 'google', 'apple'],
  features: { billing: true, google: true, microsoft: true, ai: true, push: false, email: false },
  webUrl: '',
  demoMode: true,
};

/** Normalizes user-entered URLs: trims, drops trailing slash, adds https://. */
export function normalizeServerUrl(url: string): string {
  const trimmed = url.trim().replace(/\/+$/, '');
  if (!trimmed) return trimmed;
  return /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
}

/** The server's web origin: the advertised webUrl, else authBaseUrl without /api/auth. */
function webBase(config: ServerConfig | null): string | null {
  const fromWebUrl = config?.webUrl?.replace(/\/+$/, '');
  if (fromWebUrl) return fromWebUrl;
  const fromAuth = config?.authBaseUrl?.replace(/\/+$/, '').replace(/\/api\/auth$/, '');
  return fromAuth || null;
}

/**
 * The server's public web origin (advertised webUrl, else the Better Auth
 * origin), or null until a server is configured. Used for account-page
 * link-outs (Settings → Account → "Download my data"); never for purchase or
 * billing links (App Store 3.1.1/3.1.3).
 */
export function webOrigin(config: ServerConfig | null): string | null {
  return webBase(config);
}

/** "Forgot password?" opens the web app's reset page in the system browser; null until a server is configured. */
export function forgotPasswordUrl(config: ServerConfig | null): string | null {
  const base = webBase(config);
  return base ? `${base}/forgot-password` : null;
}

/**
 * Absolute callbackURL for email sign-up. The @better-auth/expo client
 * rewrites any RELATIVE callbackURL into a calendium:// deep link, which the
 * app has no verify-email screen for; an absolute web URL lands the emailed
 * link on the web /verify-email page instead (trusted via PUBLIC_WEB_URL /
 * BETTER_AUTH_URL).
 */
export function verifyEmailCallbackUrl(config: ServerConfig): string {
  return `${webBase(config) ?? ''}/verify-email`;
}

export async function getStoredServerConfig(): Promise<ServerConfig | null> {
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ServerConfig>;
    return { webUrl: '', ...parsed } as ServerConfig;
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
    authBaseUrl: info.authBaseUrl,
    mode: info.mode,
    name: info.name,
    authProviders: info.authProviders,
    features: info.features,
    webUrl: info.webUrl ?? '',
  };
}

/** Applies (or clears) a config on the runtime Better Auth + API clients. */
function applyConfig(config: ServerConfig | null): AuthClient | null {
  // Gate mock data on the config's explicit demo flag (never on live configs).
  setDemoMode(config?.demoMode ?? false);
  if (config) {
    configureApi(config.serverUrl);
    return configureAuthClient(config.authBaseUrl);
  }
  return configureAuthClient('');
}

interface ServerConfigContextValue {
  config: ServerConfig | null;
  isConfigured: boolean;
  isLoading: boolean;
  /** Better Auth client bound to the configured server (null when unconfigured). */
  authClient: AuthClient | null;
  save: (config: ServerConfig) => Promise<void>;
  clear: () => Promise<void>;
}

const ServerConfigContext = React.createContext<ServerConfigContextValue | undefined>(undefined);
ServerConfigContext.displayName = 'ServerConfigContext';

export function ServerConfigProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = React.useState<ServerConfig | null>(null);
  const [isLoading, setIsLoading] = React.useState(true);
  const [authClient, setAuthClient] = React.useState<AuthClient | null>(() => getAuthClient());

  // Rehydrate persisted config on boot and rebuild the runtime clients from it.
  React.useEffect(() => {
    let active = true;
    getStoredServerConfig().then((stored) => {
      if (!active) return;
      if (stored) {
        setAuthClient(applyConfig(stored));
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
    setAuthClient(applyConfig(next));
    setConfig(next);
  }, []);

  const clear = React.useCallback(async () => {
    await clearStoredServerConfig();
    setAuthClient(applyConfig(null));
    setConfig(null);
  }, []);

  const value = React.useMemo<ServerConfigContextValue>(
    () => ({ config, isConfigured: config !== null, isLoading, authClient, save, clear }),
    [config, isLoading, authClient, save, clear]
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
