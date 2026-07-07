import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ServerConfig } from './server-config';

// api.ts only reads getActiveServerConfig/isDemoMode from server-config.ts at
// call time (not at import time), so mocking that module in isolation lets
// these tests drive apiConfigured/orMock's branching without touching
// localStorage or real server discovery (already covered by
// server-config.test.ts). api.ts also imports getAccessToken from ./auth
// purely to hand it to `new ApiClient(...)` — that's inert at module load
// (no network call happens just by constructing the client), so ./auth is
// left unmocked and imported for real.
const { getActiveServerConfigMock, isDemoModeMock } = vi.hoisted(() => ({
  getActiveServerConfigMock: vi.fn<() => ServerConfig | null>(),
  isDemoModeMock: vi.fn<() => boolean>(),
}));
vi.mock('./server-config', () => ({
  getActiveServerConfig: getActiveServerConfigMock,
  isDemoMode: isDemoModeMock,
}));

import { apiConfigured, orMock } from './api';

function configWithUrl(serverUrl: string): ServerConfig {
  return {
    serverUrl,
    authBaseUrl: `${serverUrl}/api/auth`,
    authProviders: ['email'],
    mode: 'cloud',
    name: 'Test',
    features: { billing: false, google: false, microsoft: false, ai: false, push: false },
    undoSendSeconds: 15,
  };
}

beforeEach(() => {
  getActiveServerConfigMock.mockReset();
  isDemoModeMock.mockReset();
});

describe('apiConfigured', () => {
  it('is false when there is no active server config', () => {
    getActiveServerConfigMock.mockReturnValue(null);
    expect(apiConfigured()).toBe(false);
  });

  it('is false when the active config has an empty serverUrl (e.g. DEMO_CONFIG)', () => {
    getActiveServerConfigMock.mockReturnValue(configWithUrl(''));
    expect(apiConfigured()).toBe(false);
  });

  it('is true once a server with a non-empty serverUrl is configured', () => {
    getActiveServerConfigMock.mockReturnValue(configWithUrl('https://api.calendium.app'));
    expect(apiConfigured()).toBe(true);
  });
});

describe('orMock', () => {
  it('calls the mock branch and skips the real call in demo mode', async () => {
    isDemoModeMock.mockReturnValue(true);
    const real = vi.fn().mockResolvedValue('real-data');
    const mock = vi.fn().mockReturnValue('mock-data');
    const result = await orMock(real, mock);
    expect(result).toBe('mock-data');
    expect(mock).toHaveBeenCalledTimes(1);
    expect(real).not.toHaveBeenCalled();
  });

  it('calls the real branch and skips the mock outside demo mode', async () => {
    isDemoModeMock.mockReturnValue(false);
    const real = vi.fn().mockResolvedValue('real-data');
    const mock = vi.fn().mockReturnValue('mock-data');
    const result = await orMock(real, mock);
    expect(result).toBe('real-data');
    expect(real).toHaveBeenCalledTimes(1);
    expect(mock).not.toHaveBeenCalled();
  });

  it('accepts a synchronous mock function (T | Promise<T>)', async () => {
    isDemoModeMock.mockReturnValue(true);
    const result = await orMock(
      () => Promise.resolve(0),
      () => 42
    );
    expect(result).toBe(42);
  });

  it('propagates a rejection from the real call instead of swallowing it (honesty policy)', async () => {
    isDemoModeMock.mockReturnValue(false);
    const real = vi.fn().mockRejectedValue(new Error('server unreachable'));
    const mock = vi.fn();
    await expect(orMock(real, mock)).rejects.toThrow('server unreachable');
  });
});
