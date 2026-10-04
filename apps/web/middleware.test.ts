// @vitest-environment node
import { NextRequest } from 'next/server';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { config, cspReportOnly, middleware } from '@/middleware';

function run(path = '/mail') {
  return middleware(new NextRequest(`http://localhost:3000${path}`));
}

describe('middleware CSP', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('defaults to Report-Only with a fresh nonce on request and response', () => {
    vi.stubEnv('CSP_REPORT_ONLY', undefined);
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com');
    const res = run();
    const header = res.headers.get('content-security-policy-report-only');
    expect(header).toBeTruthy();
    expect(res.headers.get('content-security-policy')).toBeNull();
    const nonce = /'nonce-([^']+)'/.exec(header ?? '')?.[1];
    expect(nonce).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(header).toContain("connect-src 'self' https://api.example.com https://*.paddle.com");
    expect(header).toContain('; report-uri /api/csp-report; report-to csp');
    expect(res.headers.get('reporting-endpoints')).toBe('csp="/api/csp-report"');
    // NextResponse.next({request:{headers}}) exposes the overridden request
    // headers as x-middleware-request-<name>.
    expect(res.headers.get('x-middleware-request-x-nonce')).toBe(nonce);
    expect(res.headers.get('x-middleware-request-content-security-policy-report-only')).toBe(header);
  });

  it('mints a different nonce for every request', () => {
    const nonceOf = (res: ReturnType<typeof run>) => res.headers.get('x-middleware-request-x-nonce');
    expect(nonceOf(run())).not.toBe(nonceOf(run()));
  });

  it('strips a client-supplied x-nonce and CSP request header', () => {
    const res = middleware(
      new NextRequest('http://localhost:3000/mail', {
        headers: { 'x-nonce': 'attacker', 'content-security-policy': "script-src 'unsafe-inline'" },
      })
    );
    expect(res.headers.get('x-middleware-request-x-nonce')).not.toBe('attacker');
    expect(res.headers.get('x-middleware-request-content-security-policy') ?? '').not.toContain('unsafe-inline');
  });

  it('enforces when CSP_REPORT_ONLY=false', () => {
    vi.stubEnv('CSP_REPORT_ONLY', 'false');
    const res = run();
    expect(res.headers.get('content-security-policy')).toContain("default-src 'self'");
    expect(res.headers.get('content-security-policy-report-only')).toBeNull();
    expect(cspReportOnly()).toBe(false);
  });

  it('reads CSP_REPORT_ONLY with the shared boolean grammar', () => {
    for (const [raw, want] of [
      ['0', false],
      ['no', false],
      [' FALSE ', false],
      ['1', true],
      ['yes', true],
      ['true', true],
      ['', true],
    ] as const) {
      vi.stubEnv('CSP_REPORT_ONLY', raw);
      expect(cspReportOnly(), JSON.stringify(raw)).toBe(want);
    }
  });

  it('falls back to Report-Only on an unparsable value (boot rejects it; requests never throw)', () => {
    vi.stubEnv('CSP_REPORT_ONLY', 'enforce');
    expect(cspReportOnly()).toBe(true);
    expect(run().headers.get('content-security-policy-report-only')).toBeTruthy();
  });

  it('same-origin API when NEXT_PUBLIC_API_URL is unset', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', undefined);
    const header = run().headers.get('content-security-policy-report-only') ?? '';
    expect(header).toContain("connect-src 'self' https://*.paddle.com");
  });

  it('adds the dev-only sources outside production', () => {
    vi.stubEnv('NODE_ENV', 'development');
    const header = run().headers.get('content-security-policy-report-only') ?? '';
    expect(header).toContain("'unsafe-eval'");
    expect(header).toContain('ws:');
    vi.stubEnv('NODE_ENV', 'production');
    const prod = run().headers.get('content-security-policy-report-only') ?? '';
    expect(prod).not.toContain("'unsafe-eval'");
  });

  it('never matches the excluded paths', () => {
    expect(config.matcher).toEqual([
      '/((?!api/|_next/static|_next/image|sw\\.js|manifest\\.webmanifest|icon|offline).*)',
    ]);
    const re = new RegExp(`^${config.matcher[0]}$`);
    for (const excluded of [
      '/api/auth/token',
      '/_next/static/a.js',
      '/_next/image',
      '/sw.js',
      '/manifest.webmanifest',
      '/icon.svg',
      '/icon-192.png',
      '/offline',
    ]) {
      expect(re.test(excluded), excluded).toBe(false);
    }
    for (const included of ['/', '/mail', '/checkout', '/checkout/success', '/signin']) {
      expect(re.test(included), included).toBe(true);
    }
  });
});
