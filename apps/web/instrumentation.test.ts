import { afterEach, beforeEach, describe, expect, it, type MockInstance, vi } from 'vitest';

import { register } from './instrumentation';

const SMTP_KEYS = ['SMTP_HOST', 'SMTP_PORT', 'SMTP_USER', 'SMTP_PASS', 'SMTP_FROM', 'SMTP_SECURE'];

describe('instrumentation.register', () => {
  let warn: MockInstance<typeof console.warn>;

  beforeEach(() => {
    for (const k of [
      ...SMTP_KEYS,
      'SELF_HOSTED',
      'TRUST_PROXY',
      'TRUSTED_PROXY_CIDRS',
      'ALLOW_DEV_ORIGINS',
      'CSP_REPORT_ONLY',
      'NEXT_PHASE',
    ])
      vi.stubEnv(k, '');
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    vi.stubEnv('BETTER_AUTH_SECRET', 'x'.repeat(32));
    warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  it('does nothing on the edge runtime', async () => {
    vi.stubEnv('NEXT_RUNTIME', 'edge');
    await expect(register()).resolves.toBeUndefined();
    expect(warn).not.toHaveBeenCalled();
  });

  it('aborts cloud mode without SMTP, naming the variables', async () => {
    await expect(register()).rejects.toThrow('SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true');
  });

  it('boots self-host without SMTP and logs the disabled line', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    await expect(register()).resolves.toBeUndefined();
    expect(warn).toHaveBeenCalledWith('email: disabled (no SMTP_HOST); verification off, invitations fall back to links');
  });

  it('logs the production warnings', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('NODE_ENV', 'production');
    vi.stubEnv('ALLOW_DEV_ORIGINS', 'true');
    await register();
    const messages = warn.mock.calls.map((c) => String(c[0]));
    expect(messages.some((m) => m.startsWith('TRUST_PROXY is not true in production'))).toBe(true);
    expect(messages.some((m) => m.startsWith('ALLOW_DEV_ORIGINS=true in production'))).toBe(true);
    // This test process was not started with the peer-appending preload.
    expect(messages.some((m) => m.startsWith('scripts/forwarded-for-peer.cjs is not preloaded'))).toBe(true);
  });

  it('aborts on an invalid boolean, like the Go config, even outside production', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('ALLOW_DEV_ORIGINS', 'on');
    await expect(register()).rejects.toThrow('ALLOW_DEV_ORIGINS must be true or false (also 1/0, yes/no), got "on"');
  });

  it('accepts SELF_HOSTED=1 as self-host, like the api and worker', async () => {
    vi.stubEnv('SELF_HOSTED', '1');
    await expect(register()).resolves.toBeUndefined();
  });

  it('aborts on an invalid TRUSTED_PROXY_CIDRS', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('TRUST_PROXY', 'true');
    vi.stubEnv('TRUSTED_PROXY_CIDRS', '10.0.0.0/8,bogus');
    await expect(register()).rejects.toThrow(/TRUSTED_PROXY_CIDRS/);
  });

  it('rejects a short BETTER_AUTH_SECRET on the node runtime, in every mode', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('BETTER_AUTH_SECRET', 'x'.repeat(31));
    await expect(register()).rejects.toThrow(
      'BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32'
    );
    vi.stubEnv('SELF_HOSTED', '');
    await expect(register()).rejects.toThrow('BETTER_AUTH_SECRET must be at least 32 bytes');
  });

  it('passes with a 32-byte secret', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    await expect(register()).resolves.toBeUndefined();
  });

  it('skips the edge runtime and the build phase even with no secret', async () => {
    vi.stubEnv('BETTER_AUTH_SECRET', '');
    vi.stubEnv('NEXT_RUNTIME', 'edge');
    await expect(register()).resolves.toBeUndefined();
    vi.stubEnv('NEXT_RUNTIME', 'nodejs');
    vi.stubEnv('NEXT_PHASE', 'phase-production-build');
    await expect(register()).resolves.toBeUndefined();
  });

  it('aborts on an invalid CSP_REPORT_ONLY and accepts the boolean grammar', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    vi.stubEnv('CSP_REPORT_ONLY', 'enforce');
    await expect(register()).rejects.toThrow('CSP_REPORT_ONLY must be true or false (also 1/0, yes/no), got "enforce"');
    vi.stubEnv('CSP_REPORT_ONLY', '0');
    await expect(register()).resolves.toBeUndefined();
  });

  it('does not warn about the preload outside production', async () => {
    vi.stubEnv('SELF_HOSTED', 'true');
    await register();
    expect(warn.mock.calls.some((c) => String(c[0]).includes('forwarded-for-peer'))).toBe(false);
  });
});
