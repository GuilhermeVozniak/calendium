// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

import { offlineCsp, STATIC_SECURITY_HEADERS, securityHeaders } from '@/lib/security-headers';

describe('securityHeaders', () => {
  afterEach(() => vi.unstubAllEnvs());

  it('matches the spec snapshot for every route', () => {
    expect(STATIC_SECURITY_HEADERS).toEqual([
      { key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' },
      { key: 'X-Content-Type-Options', value: 'nosniff' },
      { key: 'X-Frame-Options', value: 'DENY' },
      { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
      { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()' },
    ]);
    const [all, offline] = securityHeaders();
    expect(all.source).toBe('/(.*)');
    expect(all.headers).toBe(STATIC_SECURITY_HEADERS);
    expect(offline.source).toBe('/offline');
    expect(offline.headers).toEqual([{ key: 'Content-Security-Policy', value: offlineCsp() }]);
  });

  it('offline CSP is the nonce policy with unsafe-inline scripts and the API origin', () => {
    vi.stubEnv('NEXT_PUBLIC_API_URL', 'https://api.example.com/');
    expect(offlineCsp()).toContain("script-src 'self' 'unsafe-inline' https://cdn.paddle.com");
    expect(offlineCsp()).toContain("connect-src 'self' https://api.example.com https://*.paddle.com");
  });
});
