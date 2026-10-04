import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { InstanceInfo } from '@calendium/shared';

// discoverServer() delegates the actual HTTP call to @calendium/shared's
// fetchInstance, which already has its own thorough test coverage
// (packages/shared/src/client.test.ts). Mocking it here isolates
// server-config.ts's own job: normalizing the input URL and mapping the
// InstanceInfo response onto a ServerConfig.
// vi.mock factories are hoisted above the rest of the file, so the mock fn
// must be created via vi.hoisted() to avoid a temporal-dead-zone reference.
const { fetchInstanceMock } = vi.hoisted(() => ({
  fetchInstanceMock: vi.fn<(baseUrl: string) => Promise<InstanceInfo>>(),
}));
vi.mock('@calendium/shared', () => ({
  fetchInstance: (baseUrl: string) => fetchInstanceMock(baseUrl),
}));

import {
  billingWebOrigin,
  CLOUD_PRESET,
  clearStoredServerConfig,
  DEMO_CONFIG,
  discoverServer,
  envUrl,
  forgotPasswordUrl,
  getActiveServerConfig,
  isDemoMode,
  normalizeServerUrl,
  setStoredServerConfig,
  webOrigin,
  type ServerConfig,
} from './server-config';

beforeEach(() => {
  localStorage.clear();
  clearStoredServerConfig();
  fetchInstanceMock.mockReset();
});

describe('normalizeServerUrl', () => {
  it('adds https:// to a bare host', () => {
    expect(normalizeServerUrl('api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('leaves an explicit https:// URL untouched (minus trailing slash)', () => {
    expect(normalizeServerUrl('https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('preserves an explicit http:// scheme for local dev servers', () => {
    expect(normalizeServerUrl('http://localhost:8080')).toBe('http://localhost:8080');
  });

  it('is case-insensitive when detecting an existing scheme', () => {
    expect(normalizeServerUrl('HTTPS://api.calendium.app')).toBe('HTTPS://api.calendium.app');
  });

  it('trims surrounding whitespace', () => {
    expect(normalizeServerUrl('  api.calendium.app  ')).toBe('https://api.calendium.app');
  });

  it('strips one or more trailing slashes', () => {
    expect(normalizeServerUrl('https://api.calendium.app/')).toBe('https://api.calendium.app');
    expect(normalizeServerUrl('https://api.calendium.app///')).toBe('https://api.calendium.app');
  });

  it('returns an empty string unchanged (no https:// prefix on nothing)', () => {
    expect(normalizeServerUrl('')).toBe('');
    expect(normalizeServerUrl('   ')).toBe('');
  });

  it('handles a host with a path', () => {
    expect(normalizeServerUrl('example.com/api')).toBe('https://example.com/api');
  });
});

describe('webOrigin', () => {
  it('returns null for a null config', () => {
    expect(webOrigin(null)).toBeNull();
  });

  it('returns null when authBaseUrl is empty (e.g. demo config)', () => {
    expect(webOrigin(DEMO_CONFIG)).toBeNull();
  });

  it('returns the scheme+host of authBaseUrl, dropping the path', () => {
    const config = { ...DEMO_CONFIG, authBaseUrl: 'https://app.calendium.app/api/auth' };
    expect(webOrigin(config)).toBe('https://app.calendium.app');
  });

  it('returns null for an unparseable authBaseUrl instead of throwing', () => {
    const config = { ...DEMO_CONFIG, authBaseUrl: 'not a url' };
    expect(webOrigin(config)).toBeNull();
  });
});

describe('billingWebOrigin', () => {
  it('prefers the server-advertised webUrl, trimming trailing slashes', () => {
    expect(billingWebOrigin({ ...DEMO_CONFIG, webUrl: 'https://app.example.com/' })).toBe(
      'https://app.example.com'
    );
  });

  it('falls back to the auth origin when webUrl is empty (pre-webUrl servers)', () => {
    expect(
      billingWebOrigin({ ...DEMO_CONFIG, authBaseUrl: 'https://auth.example.com/api/auth' })
    ).toBe('https://auth.example.com');
  });

  it('returns null when neither a webUrl nor an auth origin is known', () => {
    expect(billingWebOrigin(null)).toBeNull();
    expect(billingWebOrigin(DEMO_CONFIG)).toBeNull();
  });
});

describe('getActiveServerConfig / isDemoMode — persistence', () => {
  it('starts with no active config and demo mode off', () => {
    expect(getActiveServerConfig()).toBeNull();
    expect(isDemoMode()).toBe(false);
  });

  it('setStoredServerConfig updates the active config and persists it to localStorage', () => {
    const config: ServerConfig = {
      serverUrl: 'https://api.calendium.app',
      authBaseUrl: 'https://api.calendium.app/api/auth',
      authProviders: ['email'],
      mode: 'cloud',
      name: 'Calendium Cloud',
      features: { billing: true, google: true, microsoft: true, ai: false, push: false },
      undoSendSeconds: 15,
      webUrl: 'https://app.calendium.app',
    };
    setStoredServerConfig(config);
    expect(getActiveServerConfig()).toEqual(config);
    const raw = localStorage.getItem('calendium.serverConfig');
    expect(raw).not.toBeNull();
    expect(JSON.parse(raw!)).toEqual(config);
  });

  it('clearStoredServerConfig resets the active config and removes it from localStorage', () => {
    setStoredServerConfig({ ...DEMO_CONFIG, serverUrl: 'https://api.calendium.app' });
    clearStoredServerConfig();
    expect(getActiveServerConfig()).toBeNull();
    expect(localStorage.getItem('calendium.serverConfig')).toBeNull();
  });

  it('CLOUD_PRESET points at the managed Calendium Cloud server', () => {
    expect(CLOUD_PRESET.serverUrl).toBe('https://api.calendium.app');
  });
});

describe('getActiveServerConfig / isDemoMode — reads persisted state at module load', () => {
  afterEach(() => {
    localStorage.clear();
  });

  it('picks up a previously persisted server config on a fresh module load', async () => {
    const config: ServerConfig = {
      serverUrl: 'https://self-host.example.com',
      authBaseUrl: 'https://self-host.example.com/api/auth',
      authProviders: ['email'],
      mode: 'self_host',
      name: 'Acme Calendium',
      features: { billing: false, google: true, microsoft: false, ai: false, push: false },
      undoSendSeconds: 10,
      webUrl: 'https://self-host.example.com',
    };
    localStorage.setItem('calendium.serverConfig', JSON.stringify(config));
    vi.resetModules();
    const fresh = await import('./server-config');
    expect(fresh.getActiveServerConfig()).toEqual(config);
  });

  it('picks up persisted demo mode on a fresh module load', async () => {
    localStorage.setItem('calendium.demoMode', 'true');
    vi.resetModules();
    const fresh = await import('./server-config');
    expect(fresh.isDemoMode()).toBe(true);
  });

  it('backfills authProviders/features/undoSendSeconds/webUrl missing from an older persisted config', async () => {
    localStorage.setItem(
      'calendium.serverConfig',
      JSON.stringify({ serverUrl: 'https://old.example.com' })
    );
    vi.resetModules();
    const fresh = await import('./server-config');
    const config = fresh.getActiveServerConfig();
    expect(config?.serverUrl).toBe('https://old.example.com');
    expect(config?.authProviders).toEqual([]);
    expect(config?.undoSendSeconds).toBe(15);
    expect(config?.webUrl).toBe('');
    expect(config?.features).toEqual({
      billing: false,
      google: false,
      microsoft: false,
      ai: false,
      push: false,
      email: false,
    });
  });

  it('tolerates malformed JSON in localStorage by treating the config as absent', async () => {
    localStorage.setItem('calendium.serverConfig', '{not valid json');
    vi.resetModules();
    const fresh = await import('./server-config');
    expect(fresh.getActiveServerConfig()).toBeNull();
  });
});

describe('discoverServer', () => {
  const INFO: InstanceInfo = {
    name: 'Acme Calendium',
    mode: 'self_host',
    version: '1.0.0',
    authBaseUrl: 'https://acme.example.com/api/auth',
    authProviders: ['email', 'google'],
    undoSendSeconds: 12,
    webUrl: 'https://acme.example.com',
    features: { billing: false, google: true, microsoft: false, ai: true, push: false },
  };

  it('normalizes the input URL before fetching', async () => {
    fetchInstanceMock.mockResolvedValue(INFO);
    await discoverServer('acme.example.com/');
    expect(fetchInstanceMock).toHaveBeenCalledWith('https://acme.example.com');
  });

  it('maps the InstanceInfo response onto a ServerConfig', async () => {
    fetchInstanceMock.mockResolvedValue(INFO);
    const config = await discoverServer('https://acme.example.com');
    expect(config).toEqual({
      serverUrl: 'https://acme.example.com',
      authBaseUrl: INFO.authBaseUrl,
      authProviders: INFO.authProviders,
      mode: INFO.mode,
      name: INFO.name,
      features: INFO.features,
      undoSendSeconds: INFO.undoSendSeconds,
      webUrl: INFO.webUrl,
    });
  });

  it('propagates a rejection from fetchInstance (e.g. unreachable server)', async () => {
    fetchInstanceMock.mockRejectedValue(new Error('network error'));
    await expect(discoverServer('https://down.example.com')).rejects.toThrow('network error');
  });
});

describe('forgotPasswordUrl', () => {
  it('prefers the advertised webUrl, falls back to the Better Auth origin, and is null without either', async () => {
    const { forgotPasswordUrl, DEMO_CONFIG } = await import('./server-config');
    const base = { ...DEMO_CONFIG, authBaseUrl: 'https://mail.example.com/api/auth' };
    expect(forgotPasswordUrl({ ...base, webUrl: 'https://app.example.com/' })).toBe('https://app.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '' })).toBe('https://mail.example.com/forgot-password');
    expect(forgotPasswordUrl({ ...base, webUrl: '', authBaseUrl: '' })).toBeNull();
    expect(forgotPasswordUrl(null)).toBeNull();
  });

  it('DEFAULT_FEATURES and DEMO_CONFIG carry features.email=false', async () => {
    const { DEMO_CONFIG } = await import('./server-config');
    expect(DEMO_CONFIG.features.email).toBe(false);
  });
});

describe('CLOUD_PRESET and envUrl', () => {
  it('defaults to Calendium Cloud', () => {
    expect(CLOUD_PRESET.serverUrl).toBe('https://api.calendium.app');
  });

  it('envUrl normalizes an override and falls back on blank', () => {
    expect(envUrl('https://cloud.example.com/', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('cloud.example.com', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('   ', 'https://api.calendium.app')).toBe('https://api.calendium.app');
    expect(envUrl(undefined, 'https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('honours VITE_CLOUD_API_URL at module load', async () => {
    vi.stubEnv('VITE_CLOUD_API_URL', 'https://cloud.example.com/');
    vi.resetModules();
    try {
      const fresh = await import('./server-config');
      expect(fresh.CLOUD_PRESET.serverUrl).toBe('https://cloud.example.com');
    } finally {
      vi.unstubAllEnvs();
      vi.resetModules();
    }
  });
});

describe('webOrigin prefers the advertised webUrl', () => {
  const base: ServerConfig = { ...DEMO_CONFIG, authBaseUrl: 'https://api.example.com/api/auth' };

  it('uses webUrl when present', () => {
    expect(webOrigin({ ...base, webUrl: 'https://mail.example.com/' })).toBe('https://mail.example.com');
  });

  it('falls back to the authBaseUrl origin when webUrl is empty or invalid', () => {
    expect(webOrigin(base)).toBe('https://api.example.com');
    expect(webOrigin({ ...base, webUrl: 'not a url' })).toBe('https://api.example.com');
  });

  it.each(['javascript:alert(1)', 'mailto:ops@example.com', 'file:///etc/passwd', 'calendium://auth', 'ftp://example.com'])(
    'ignores the non-http(s) webUrl %j instead of yielding "null"',
    (webUrl) => {
      const config = { ...base, webUrl };
      expect(webOrigin(config)).toBe('https://api.example.com');
      expect(billingWebOrigin(config)).toBe('https://api.example.com');
      expect(forgotPasswordUrl(config)).toBe('https://api.example.com/forgot-password');
    }
  );

  it('accepts a plain-http webUrl (self-host on a LAN)', () => {
    expect(webOrigin({ ...base, webUrl: 'http://calendium.lan:3000/' })).toBe('http://calendium.lan:3000');
  });
});
