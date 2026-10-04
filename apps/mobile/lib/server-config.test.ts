import { ApiRequestError } from '@calendium/shared';

// --- Mocks -------------------------------------------------------------
// server-config.ts pulls in the whole runtime client wiring (api, auth-client,
// mock) purely to re-apply config on save/clear; none of that matters for the
// units under test here (normalizeServerUrl / discoverServer / persistence),
// so it's stubbed out rather than exercising Better Auth/fetch for real.
jest.mock('@/lib/api', () => ({ configureApi: jest.fn() }));
jest.mock('@/lib/auth-client', () => ({
  configureAuthClient: jest.fn(),
  getAuthClient: jest.fn(() => null),
}));
jest.mock('@/lib/mock', () => ({ setDemoMode: jest.fn() }));

const mockFetchInstance = jest.fn();
jest.mock('@calendium/shared', () => {
  const actual = jest.requireActual('@calendium/shared');
  return { ...actual, fetchInstance: (...args: unknown[]) => mockFetchInstance(...args) };
});

jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

import AsyncStorage from '@react-native-async-storage/async-storage';
import {
  CLOUD_PRESET,
  DEMO_CONFIG,
  envUrl,
  clearStoredServerConfig,
  discoverServer,
  forgotPasswordUrl,
  getStoredServerConfig,
  normalizeServerUrl,
  setStoredServerConfig,
  type ServerConfig,
  verifyEmailCallbackUrl,
} from './server-config';

beforeEach(() => {
  mockFetchInstance.mockReset();
  jest.clearAllMocks();
});

describe('DEMO_CONFIG', () => {
  it('advertises no web origin, so the demo builds no billing link', () => {
    expect(DEMO_CONFIG.webUrl).toBe('');
  });
});

describe('normalizeServerUrl', () => {
  it('trims whitespace', () => {
    expect(normalizeServerUrl('  example.com  ')).toBe('https://example.com');
  });

  it('drops one or more trailing slashes', () => {
    expect(normalizeServerUrl('https://example.com/')).toBe('https://example.com');
    expect(normalizeServerUrl('https://example.com///')).toBe('https://example.com');
  });

  it('adds https:// when no scheme is present', () => {
    expect(normalizeServerUrl('example.com')).toBe('https://example.com');
  });

  it('leaves an explicit http:// scheme alone', () => {
    expect(normalizeServerUrl('http://localhost:8080')).toBe('http://localhost:8080');
  });

  it('leaves an explicit https:// scheme alone', () => {
    expect(normalizeServerUrl('https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('is case-insensitive when detecting an existing scheme', () => {
    expect(normalizeServerUrl('HTTPS://example.com')).toBe('HTTPS://example.com');
  });

  it('returns an empty string for blank input (no scheme prepended)', () => {
    expect(normalizeServerUrl('   ')).toBe('');
  });
});

describe('discoverServer', () => {
  it('normalizes the URL, fetches /v1/instance, and maps the response into a ServerConfig', async () => {
    mockFetchInstance.mockResolvedValue({
      name: 'My Calendium',
      mode: 'self_host',
      version: '1.0.0',
      authBaseUrl: 'https://example.com/api/auth',
      authProviders: ['email'],
      undoSendSeconds: 15,
      features: { billing: false, google: true, microsoft: false, ai: true, push: false },
      webUrl: 'https://app.example.com',
    });

    const config = await discoverServer('example.com/');

    expect(mockFetchInstance).toHaveBeenCalledWith('https://example.com');
    expect(config).toEqual({
      serverUrl: 'https://example.com',
      authBaseUrl: 'https://example.com/api/auth',
      mode: 'self_host',
      name: 'My Calendium',
      authProviders: ['email'],
      features: { billing: false, google: true, microsoft: false, ai: true, push: false },
      webUrl: 'https://app.example.com',
    });
    // demoMode is intentionally absent for a real discovered server.
    expect(config.demoMode).toBeUndefined();
  });

  it('falls back to an empty webUrl when the server predates instance.webUrl', async () => {
    mockFetchInstance.mockResolvedValue({
      name: 'Old Calendium',
      mode: 'self_host',
      version: '0.9.0',
      authBaseUrl: 'https://old.example.com/api/auth',
      authProviders: ['email'],
      undoSendSeconds: 15,
      features: { billing: false, google: false, microsoft: false, ai: false, push: false },
    });

    const config = await discoverServer('old.example.com');

    expect(config.webUrl).toBe('');
  });

  it('propagates ApiRequestError when the server is unreachable/errors', async () => {
    mockFetchInstance.mockRejectedValue(new ApiRequestError(503, 'unavailable', 'down'));
    await expect(discoverServer('https://down.example.com')).rejects.toBeInstanceOf(
      ApiRequestError
    );
  });
});

describe('AsyncStorage persistence', () => {
  const config: ServerConfig = {
    serverUrl: 'https://example.com',
    authBaseUrl: 'https://example.com/api/auth',
    mode: 'self_host',
    name: 'My Calendium',
    authProviders: ['email'],
    features: { billing: false, google: true, microsoft: false, ai: true, push: false },
    webUrl: 'https://app.example.com',
  };

  afterEach(async () => {
    await AsyncStorage.clear();
  });

  it('getStoredServerConfig returns null when nothing is stored', async () => {
    await expect(getStoredServerConfig()).resolves.toBeNull();
  });

  it('setStoredServerConfig then getStoredServerConfig round-trips the config', async () => {
    await setStoredServerConfig(config);
    await expect(getStoredServerConfig()).resolves.toEqual(config);
  });

  it('backfills webUrl on a config persisted before instance.webUrl existed', async () => {
    const { webUrl: _dropped, ...legacy } = config;
    await AsyncStorage.setItem('calendium.serverConfig', JSON.stringify(legacy));
    await expect(getStoredServerConfig()).resolves.toEqual({ ...legacy, webUrl: '' });
  });

  it('setStoredServerConfig persists under the expected storage key', async () => {
    await setStoredServerConfig(config);
    const raw = await AsyncStorage.getItem('calendium.serverConfig');
    expect(JSON.parse(raw as string)).toEqual(config);
  });

  it('clearStoredServerConfig removes the persisted config', async () => {
    await setStoredServerConfig(config);
    await clearStoredServerConfig();
    await expect(getStoredServerConfig()).resolves.toBeNull();
  });

  it('getStoredServerConfig returns null (rather than throwing) on corrupt JSON', async () => {
    await AsyncStorage.setItem('calendium.serverConfig', '{not valid json');
    await expect(getStoredServerConfig()).resolves.toBeNull();
  });
});

describe('forgotPasswordUrl (piece 2)', () => {
  const base: ServerConfig = { ...DEMO_CONFIG, authBaseUrl: 'https://mail.example.com/api/auth', demoMode: false };

  it('prefers webUrl, falls back to the Better Auth origin, and is null without either', () => {
    expect(forgotPasswordUrl({ ...base, webUrl: 'https://app.example.com/' })).toBe('https://app.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '', authBaseUrl: '' })).toBeNull();
    expect(forgotPasswordUrl(null)).toBeNull();
  });

  it('verifyEmailCallbackUrl is absolute so the Expo client does not rewrite it into a deep link', () => {
    expect(verifyEmailCallbackUrl({ ...base, webUrl: 'https://app.example.com' })).toBe('https://app.example.com/verify-email');
    expect(verifyEmailCallbackUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/verify-email');
  });

  it('DEMO_CONFIG advertises features.email=false', () => {
    expect(DEMO_CONFIG.features.email).toBe(false);
  });
});

describe('cloud preset and demo hosts', () => {
  it('CLOUD_PRESET defaults to Calendium Cloud when EXPO_PUBLIC_CLOUD_API_URL is unset', () => {
    expect(CLOUD_PRESET.serverUrl).toBe('https://api.calendium.app');
  });

  it('DEMO_CONFIG defaults to demo.calendium.app (a label only; demo never dials out)', () => {
    expect(DEMO_CONFIG.serverUrl).toBe('https://demo.calendium.app');
    expect(DEMO_CONFIG.authBaseUrl).toBe('https://demo.calendium.app/api/auth');
    // Still no advertised web origin: the demo builds no billing link.
    expect(DEMO_CONFIG.webUrl).toBe('');
    expect(DEMO_CONFIG.demoMode).toBe(true);
  });

  it('envUrl normalizes an override (trailing slash, missing scheme) and falls back on blank', () => {
    expect(envUrl('https://cloud.example.com/', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('cloud.example.com', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('   ', 'https://api.calendium.app')).toBe('https://api.calendium.app');
    expect(envUrl(undefined, 'https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  // babel-preset-expo's inline-env-vars plugin only runs for production
  // bundles; under jest the module reads the live process.env, so isolate a
  // fresh load with the override set.
  function withEnv(name: string, value: string, run: () => void): void {
    const prev = process.env[name];
    process.env[name] = value;
    try {
      jest.isolateModules(run);
    } finally {
      if (prev === undefined) delete process.env[name];
      else process.env[name] = prev;
    }
  }

  it('honours EXPO_PUBLIC_CLOUD_API_URL at module load', () => {
    withEnv('EXPO_PUBLIC_CLOUD_API_URL', 'https://cloud.example.com/', () => {
      const fresh = require('./server-config') as typeof import('./server-config');
      expect(fresh.CLOUD_PRESET.serverUrl).toBe('https://cloud.example.com');
    });
  });

  it('honours EXPO_PUBLIC_DEMO_SERVER_URL at module load', () => {
    withEnv('EXPO_PUBLIC_DEMO_SERVER_URL', 'demo.example.org', () => {
      const fresh = require('./server-config') as typeof import('./server-config');
      expect(fresh.DEMO_CONFIG.serverUrl).toBe('https://demo.example.org');
      expect(fresh.DEMO_CONFIG.authBaseUrl).toBe('https://demo.example.org/api/auth');
    });
  });
});
