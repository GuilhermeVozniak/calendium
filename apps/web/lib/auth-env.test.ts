import { describe, expect, it } from 'vitest';

import {
  APPLE_FORM_POST_ORIGIN,
  CLIENT_IP_HEADER,
  buildTrustedOrigins,
  clientIpFor,
  devOriginsAllowed,
  isAllowedOrigin,
  passwordPolicyError,
  rateLimitRules,
  startupWarnings,
  withClientIp,
} from '@/lib/auth-env';

const WAILS = ['wails://wails', 'wails://wails.localhost', 'http://wails.localhost', 'https://wails.localhost'];
const DEV = { NODE_ENV: 'development' };
const PROD = { NODE_ENV: 'production' };

describe('devOriginsAllowed', () => {
  it('is on outside production, off in production, and re-enabled by ALLOW_DEV_ORIGINS=true', () => {
    expect(devOriginsAllowed({})).toBe(true);
    expect(devOriginsAllowed(DEV)).toBe(true);
    expect(devOriginsAllowed({ NODE_ENV: 'test' })).toBe(true);
    expect(devOriginsAllowed(PROD)).toBe(false);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: 'true' })).toBe(true);
    expect(devOriginsAllowed({ ...PROD, ALLOW_DEV_ORIGINS: '1' })).toBe(false);
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

describe('clientIpFor', () => {
  const headers = (xff?: string) => new Headers(xff === undefined ? {} : { 'x-forwarded-for': xff });

  it('returns empty when the header is absent or blank', () => {
    expect(clientIpFor(headers(), true)).toBe('');
    expect(clientIpFor(headers(''), false)).toBe('');
    expect(clientIpFor(headers(' , '), true)).toBe('');
  });

  it('TRUST_PROXY=false: trusts only a single-valued header (the socket fill)', () => {
    expect(clientIpFor(headers('203.0.113.9'), false)).toBe('203.0.113.9');
    expect(clientIpFor(headers('203.0.113.9, 10.0.0.1'), false)).toBe('');
  });

  it('TRUST_PROXY=true: takes the first hop and tolerates whitespace and a trailing comma', () => {
    expect(clientIpFor(headers(' 203.0.113.9 , 10.0.0.1,'), true)).toBe('203.0.113.9');
    expect(clientIpFor(headers(' 203.0.113.9 , 10.0.0.1,'), false)).toBe('');
  });
});

describe('withClientIp', () => {
  it('overwrites a forged x-calendium-client-ip and preserves method, URL and body', async () => {
    const original = new Request('https://mail.example.com/api/auth/sign-in/email', {
      method: 'POST',
      headers: { 'content-type': 'application/json', [CLIENT_IP_HEADER]: '1.2.3.4', 'x-forwarded-for': '203.0.113.9, 10.0.0.1' },
      body: JSON.stringify({ email: 'a@b.test' }),
    });
    const stamped = withClientIp(original, false);
    expect(stamped.headers.get(CLIENT_IP_HEADER)).toBe('');
    expect(stamped.method).toBe('POST');
    expect(stamped.url).toBe(original.url);
    expect(stamped.headers.get('content-type')).toBe('application/json');
    expect(await stamped.text()).toBe(JSON.stringify({ email: 'a@b.test' }));
  });

  it('stamps the first hop when the proxy is trusted', () => {
    const req = new Request('https://mail.example.com/api/auth/token', { headers: { 'x-forwarded-for': '203.0.113.9, 10.0.0.1' } });
    expect(withClientIp(req, true).headers.get(CLIENT_IP_HEADER)).toBe('203.0.113.9');
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
  });
});
