import { fetchInstance, type InstanceMode } from '@calendium/shared';
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
    return raw ? (JSON.parse(raw) as ServerConfig) : null;
  } catch {
    return null;
  }
}

// Module-level "active" config, read synchronously by the Better Auth + API
// clients (lib/auth, lib/api). Kept in sync with React state below.
//
// There is no env-seeded default anymore: a server's Better Auth base URL is
// only known after fetching its /v1/instance descriptor, so dev always goes
// through the Connect screen (which pre-fills VITE_API_URL). See .env.example.
let activeConfig: ServerConfig | null = readStored();

export function getActiveServerConfig(): ServerConfig | null {
  return activeConfig;
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
  };
}

interface ServerConfigContextValue {
  config: ServerConfig | null;
  isConfigured: boolean;
  save: (config: ServerConfig) => void;
  clear: () => void;
}

const ServerConfigContext = React.createContext<ServerConfigContextValue | undefined>(undefined);
ServerConfigContext.displayName = 'ServerConfigContext';

export function ServerConfigProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = React.useState<ServerConfig | null>(() => getActiveServerConfig());

  const save = React.useCallback((next: ServerConfig) => {
    setStoredServerConfig(next);
    setConfig(next);
  }, []);

  const clear = React.useCallback(() => {
    clearStoredServerConfig();
    setConfig(null);
  }, []);

  const value = React.useMemo<ServerConfigContextValue>(
    () => ({ config, isConfigured: config !== null, save, clear }),
    [config, save, clear]
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
