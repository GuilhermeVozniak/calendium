import { describe, expect, it } from 'vitest';

import {
  APPLE_FORM_POST_ORIGIN,
  BOOLEAN_ENV_VARS,
  assertBooleanEnv,
  buildTrustedOrigins,
  devOriginsAllowed,
  envBool,
  isAllowedOrigin,
  passwordPolicyError,
  rateLimitRules,
  startupWarnings,
} from '@/lib/auth-env';

const WAILS = ['wails://wails', 'wails://wails.localhost', 'http://wails.localhost', 'https://wails.localhost'];
const DEV = { NODE_ENV: 'development' };
const PROD = { NODE_ENV: 'production' };

/**
 * The boolean grammar shared with the Go config (config.parseBool, same table
 * in config_test.go TestBooleanEnvParsing).
 */
describe('envBool — identical to the Go config', () => {
  const TRUTHY = ['true', 'TRUE', 'True', '1', 'yes', 'YES', 'Yes', ' true '];
  const FALSY = ['false', 'FALSE', 'False', '0', 'no', 'NO', '', '  '];
  const INVALID = ['t', 'f', 'T', 'F', 'on', 'off', 'y', 'n', '2', 'maybe', 'truee'];

  it('names every variable both tiers read', () => {
    expect([...BOOLEAN_ENV_VARS].sort()).toEqual(['ALLOW_DEV_ORIGINS', 'SELF_HOSTED', 'SMTP_SECURE', 'TRUST_PROXY']);
  });

  for (const name of BOOLEAN_ENV_VARS) {
    it.each(TRUTHY)(`${name}=%j is true`, (value) => {
      expect(envBool({ [name]: value }, name)).toBe(true);
    });
    it.each(FALSY)(`${name}=%j is false`, (value) => {
      expect(envBool({ [name]: value }, name)).toBe(false);
    });
    it.each(INVALID)(`${name}=%j is a configuration error naming the variable`, (value) => {
      expect(() => envBool({ [name]: value }, name)).toThrow(`${name} must be true or false (also 1/0, yes/no), got "${value}"`);
    });
  }

  it('unset is false', () => {
    expect(envBool({}, 'SELF_HOSTED')).toBe(false);
  });

  it('assertBooleanEnv rejects the first invalid variable and accepts valid ones', () => {
    expect(() => assertBooleanEnv({ SELF_HOSTED: '1', SMTP_SECURE: 'YES', ALLOW_DEV_ORIGINS: 'no', TRUST_PROXY: '' })).not.toThrow();
    expect(() => assertBooleanEnv({ SELF_HOSTED: 'true', ALLOW_DEV_ORIGINS: 'on' })).toThrow('ALLOW_DEV_ORIGINS must be true or false');
    expect(() => assertBooleanEnv({ TRUST_PROXY: 'maybe' })).toThrow('TRUST_PROXY must be true or false');
  });
});

describe('devOriginsAllowed', () => {
  it('is on outside production, off in production, and re-enabled by ALLOW_DEV_ORIGINS=true', () => {
    expect(devOriginsAllowed({})).toBe(true);
    expect(devOriginsAllowed(DEV)).toBe(true);
    expect(devOriginsAllowed({ NODE_ENV: 'test' })).toBe(true);
    expect(devOriginsAllowed(PROD)).toBe(false);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toBe(true);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: '1' })).toBe(true);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: 'YES' })).toBe(true);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: '0' })).toBe(false);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: 'no' })).toBe(false);
  });
});

describe('buildTrustedOrigins', () => {
  it('always trusts the native scheme, Apple form_post and the four Wails origins', () => {
    const trusted = buildTrustedOrigins(PROD);
    expect(trusted).toContain('calendium://');
    expect(trusted).toContain(APPLE_FORM_POST_ORIGIN);
    for (const w of WAILS) expect(trusted).toContain(w);
  });

  it('adds localhost wildcards only when dev origins are allowed', () => {
    expect(buildTrustedOrigins(DEV)).toEqual(expect.arrayContaining(['http://localhost:*', 'http://127.0.0.1:*']));
    expect(buildTrustedOrigins(PROD)).not.toContain('http://localhost:*');
    expect(buildTrustedOrigins(PROD)).not.toContain('http://127.0.0.1:*');
    expect(buildTrustedOrigins({ ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toContain('http://localhost:*');
  });

  it('adds BETTER_AUTH_URL, PUBLIC_WEB_URL and CORS_ALLOWED_ORIGINS (trimmed, de-duplicated, no trailing slash)', () => {
    const trusted = buildTrustedOrigins({
      ...PROD,
      BETTER_AUTH_URL: 'https://mail.example.com/',
      PUBLIC_WEB_URL: 'https://mail.example.com',
      CORS_ALLOWED_ORIGINS: ' https://ops.example.com , https://partner.example.com/ ,,',
    });
    expect(trusted.filter((o) => o === 'https://mail.example.com')).toHaveLength(1);
    expect(trusted).toContain('https://ops.example.com');
    expect(trusted).toContain('https://partner.example.com');
    expect(trusted).not.toContain('');
  });
});

describe('isAllowedOrigin (CORS reflection)', () => {
  it('rejects a missing or unparseable origin', () => {
    expect(isAllowedOrigin(null, DEV)).toBe(false);
    expect(isAllowedOrigin(undefined, DEV)).toBe(false);
    expect(isAllowedOrigin('', DEV)).toBe(false);
    expect(isAllowedOrigin('not a url', DEV)).toBe(false);
  });

  it('always reflects the Wails origins, including any port on wails.localhost, even in production', () => {
    for (const w of WAILS) expect(isAllowedOrigin(w, PROD)).toBe(true);
    expect(isAllowedOrigin('http://wails.localhost:1420', PROD)).toBe(true);
    expect(isAllowedOrigin('https://wails.localhost:9999', PROD)).toBe(true);
  });

  it('reflects localhost / 127.0.0.1 / ::1 only when dev origins are allowed', () => {
    for (const o of ['http://localhost:3000', 'https://localhost', 'http://127.0.0.1:8080', 'http://[::1]:3000']) {
      expect(isAllowedOrigin(o, DEV)).toBe(true);
      expect(isAllowedOrigin(o, PROD)).toBe(false);
      expect(isAllowedOrigin(o, { ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toBe(true);
    }
    expect(isAllowedOrigin('ftp://localhost', DEV)).toBe(false);
    expect(isAllowedOrigin('http://localhost.evil.example', DEV)).toBe(false);
  });

  it('reflects the explicit origins in production', () => {
    const env = { ...PROD, BETTER_AUTH_URL: 'https://mail.example.com', PUBLIC_WEB_URL: 'https://app.example.com/', CORS_ALLOWED_ORIGINS: ' https://ops.example.com ' };
    expect(isAllowedOrigin('https://mail.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://app.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://ops.example.com', env)).toBe(true);
    expect(isAllowedOrigin('https://unlisted.example.com', env)).toBe(false);
  });

  it('never reflects appleid.apple.com (trusted for CSRF, not for CORS)', () => {
    expect(isAllowedOrigin(APPLE_FORM_POST_ORIGIN, DEV)).toBe(false);
    expect(isAllowedOrigin(APPLE_FORM_POST_ORIGIN, PROD)).toBe(false);
  });
});

describe('rateLimitRules', () => {
  it('matches the spec table exactly', () => {
    expect(rateLimitRules()).toEqual({
      '/sign-in/email': { window: 60, max: 5 },
      '/sign-up/email': { window: 60, max: 3 },
      '/request-password-reset': { window: 600, max: 3 },
      '/forget-password': { window: 600, max: 3 },
      '/send-verification-email': { window: 600, max: 3 },
      '/token': { window: 60, max: 60 },
    });
  });
});

describe('passwordPolicyError', () => {
  it('enforces 10–128 characters', () => {
    expect(passwordPolicyError('short-one', 'a@b.test')).toEqual({ code: 'PASSWORD_TOO_SHORT', message: 'Password must be at least 10 characters.' });
    expect(passwordPolicyError('x'.repeat(129), 'a@b.test')).toEqual({ code: 'PASSWORD_TOO_LONG', message: 'Password must be at most 128 characters.' });
    expect(passwordPolicyError('x'.repeat(10), 'a@b.test')).toBeNull();
    expect(passwordPolicyError('x'.repeat(128), 'a@b.test')).toBeNull();
  });

  it('rejects the email local part case-insensitively', () => {
    expect(passwordPolicyError('ADA.LOVELACE-2026!', 'Ada.Lovelace@example.test')).toEqual({
      code: 'PASSWORD_CONTAINS_EMAIL',
      message: 'Password must not contain your email address.',
    });
    expect(passwordPolicyError('correct-horse-battery', 'ada.lovelace@example.test')).toBeNull();
  });

  it('ignores local parts shorter than 3 characters and a missing email', () => {
    expect(passwordPolicyError('abcdefghijk1', 'ab@example.test')).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', null)).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', undefined)).toBeNull();
    expect(passwordPolicyError('abcdefghijk1', 'abc@example.test')).toEqual(expect.objectContaining({ code: 'PASSWORD_CONTAINS_EMAIL' }));
  });
});

describe('startupWarnings', () => {
  it('is silent outside production', () => {
    expect(startupWarnings(DEV)).toEqual([]);
    expect(startupWarnings({ ...DEV, ALLOW_DEV_ORIGINS: 'true' })).toEqual([]);
  });

  it('warns about TRUST_PROXY and ALLOW_DEV_ORIGINS in production', () => {
    const both = startupWarnings({ ...PROD, ALLOW_DEV_ORIGINS: 'true' });
    expect(both).toHaveLength(2);
    expect(both[0]).toMatch(/^TRUST_PROXY is not true in production/);
    expect(both[1]).toMatch(/^ALLOW_DEV_ORIGINS=true in production/);
    expect(startupWarnings({ ...PROD, TRUST_PROXY: 'true' })).toEqual([]);
    expect(startupWarnings({ ...PROD, TRUST_PROXY: '1' })).toEqual([]);
    expect(startupWarnings({ ...PROD, TRUST_PROXY: 'Yes', ALLOW_DEV_ORIGINS: 'YES' })).toHaveLength(1);
  });
});
