import { fetchInstance, type InstanceFeatures, type InstanceMode } from '@calendium/shared';
import * as React from 'react';

/**
 * Runtime server configuration (open-core self-host vs cloud).
 *
 * The user connects to a Calendium server by URL; we fetch its public
 * `/v1/instance` descriptor and persist the resulting config to localStorage.
 * The Better Auth + API clients are built from this config at runtime, so a
 * single build can point at any server — a self-hosted instance or Calendium
 * Cloud.
 */

const STORAGE_KEY = 'calendium.serverConfig';
const DEMO_KEY = 'calendium.demoMode';

/** Feature defaults for pre-features persisted configs and demo mode. */
const DEFAULT_FEATURES: InstanceFeatures = {
  billing: false,
  google: false,
  microsoft: false,
  ai: false,
  push: false,
};

/** Calendium Cloud — our managed, paid server (docs/payments.md). */
export const CLOUD_PRESET = {
  serverUrl: 'https://api.calendium.app',
} as const;

export interface ServerConfig {
  /** Base URL of the Calendium backend the client talks to. */
  serverUrl: string;
  /** Better Auth base URL for this server, e.g. https://…/api/auth. */
  authBaseUrl: string;
  /** Sign-in methods this server enables, e.g. ["email", "google", "apple"]. */
  authProviders: string[];
  mode: InstanceMode;
  /** Human-readable instance name (INSTANCE_NAME on the server). */
  name: string;
  /** Capabilities the server advertises, so the UI can hide what it lacks. */
  features: InstanceFeatures;
  /** Undo-send grace window in seconds (post-send Undo affordance duration). */
  undoSendSeconds: number;
}

/**
 * Synthetic config backing "Try the demo": no real server, but a fully-featured
 * shape so every view renders. Paired with demoMode=true, which makes lib/api's
 * orMock serve local mock data instead of calling a backend.
 */
export const DEMO_CONFIG: ServerConfig = {
  serverUrl: '',
  authBaseUrl: '',
  authProviders: ['email', 'google', 'apple'],
  mode: 'cloud',
  name: 'Calendium Demo',
  features: { billing: true, google: true, microsoft: true, ai: true, push: false },
  undoSendSeconds: 15,
};

/** Web origin (scheme://host) of a server's Better Auth base URL, or null. */
export function webOrigin(config: ServerConfig | null): string | null {
  if (!config?.authBaseUrl) return null;
  try {
    return new URL(config.authBaseUrl).origin;
  } catch {
    return null;
  }
}

/** Normalizes user-entered URLs: trims, drops trailing slash, adds https://. */
export function normalizeServerUrl(url: string): string {
  const trimmed = url.trim().replace(/\/+$/, '');
  if (!trimmed) return trimmed;
  return /^https?:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
}

function readStored(): ServerConfig | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ServerConfig>;
    // Backfill fields added after a config may have been persisted.
    return {
      authProviders: [],
      features: DEFAULT_FEATURES,
      undoSendSeconds: 15,
      ...parsed,
    } as ServerConfig;
  } catch {
    return null;
  }
}

function readDemo(): boolean {
  try {
    return localStorage.getItem(DEMO_KEY) === 'true';
  } catch {
    return false;
  }
}

// Module-level "active" config, read synchronously by the Better Auth + API
// clients (lib/auth, lib/api). Kept in sync with React state below.
//
// There is no env-seeded default anymore: a server's Better Auth base URL is
// only known after fetching its /v1/instance descriptor, so dev always goes
// through the Connect screen (which pre-fills VITE_API_URL). See .env.example.
let activeConfig: ServerConfig | null = readStored();
let demoActive = readDemo();

export function getActiveServerConfig(): ServerConfig | null {
  return activeConfig;
}

/** True only in explicit "Try the demo" mode — the sole state that shows mock data. */
export function isDemoMode(): boolean {
  return demoActive;
}

function setDemoPersisted(on: boolean): void {
  demoActive = on;
  try {
    if (on) localStorage.setItem(DEMO_KEY, 'true');
    else localStorage.removeItem(DEMO_KEY);
  } catch {
    // Ignore write failures (private mode, quota).
  }
}

export function setStoredServerConfig(config: ServerConfig): void {
  activeConfig = config;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(config));
  } catch {
    // Ignore write failures (private mode, quota).
  }
}

export function clearStoredServerConfig(): void {
  activeConfig = null;
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // Ignore.
  }
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
    authProviders: info.authProviders,
    mode: info.mode,
    name: info.name,
    features: info.features,
    undoSendSeconds: info.undoSendSeconds,
  };
}

interface ServerConfigContextValue {
  config: ServerConfig | null;
  isConfigured: boolean;
  /** True in explicit "Try the demo" mode. */
  demoMode: boolean;
  save: (config: ServerConfig) => void;
  clear: () => void;
  enterDemo: () => void;
  exitDemo: () => void;
}

const ServerConfigContext = React.createContext<ServerConfigContextValue | undefined>(undefined);
ServerConfigContext.displayName = 'ServerConfigContext';

export function ServerConfigProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = React.useState<ServerConfig | null>(() => getActiveServerConfig());
  const [demoMode, setDemoMode] = React.useState<boolean>(() => isDemoMode());

  const save = React.useCallback((next: ServerConfig) => {
    setDemoPersisted(false);
    setStoredServerConfig(next);
    setConfig(next);
    setDemoMode(false);
  }, []);

  const clear = React.useCallback(() => {
    setDemoPersisted(false);
    clearStoredServerConfig();
    setConfig(null);
    setDemoMode(false);
  }, []);

  const enterDemo = React.useCallback(() => {
    setStoredServerConfig(DEMO_CONFIG);
    setDemoPersisted(true);
    setConfig(DEMO_CONFIG);
    setDemoMode(true);
  }, []);

  const value = React.useMemo<ServerConfigContextValue>(
    () => ({
      config,
      isConfigured: config !== null,
      demoMode,
      save,
      clear,
      enterDemo,
      exitDemo: clear,
    }),
    [config, demoMode, save, clear, enterDemo]
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
