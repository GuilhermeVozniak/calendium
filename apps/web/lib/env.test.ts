import { afterEach, describe, expect, it, vi } from 'vitest';

import { env } from '@/lib/env';

// `env.apiUrl` is a getter — it reads process.env on every access rather than
// baking in a value at import time — so these tests stub the env var and
// re-read the getter directly, with no module reset required.
describe('env.apiUrl', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('returns an empty base when NEXT_PUBLIC_API_URL is unset (same-origin)', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', undefined);
    expect(env.apiUrl).toBe('');
  });

  it('returns an empty base when NEXT_PUBLIC_API_URL is a bare "/"', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', '/');
    expect(env.apiUrl).toBe('');
  });

  it('strips a trailing slash from an explicit URL', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com/');
    expect(env.apiUrl).toBe('https://api.example.com');
  });

  it('strips multiple trailing slashes', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com///');
    expect(env.apiUrl).toBe('https://api.example.com');
  });

  it('leaves a URL without a trailing slash untouched', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com/v1');
    expect(env.apiUrl).toBe('https://api.example.com/v1');
  });
});
