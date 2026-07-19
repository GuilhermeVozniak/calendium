import { describe, expect, it } from 'vitest';

import { OFFLINE_URL, SW_CACHE, classifyRequest, isStaleCache } from '@/lib/sw-caching';

const classify = (path: string, method = 'GET', hasAuth = false) =>
  classifyRequest(new URL(path, 'https://app.calendium.test'), method, hasAuth);

describe('classifyRequest', () => {
  it.each([
    '/_next/static/chunks/main-abc123.js',
    '/_next/static/css/app.css',
    '/_next/static/media/inter.woff2',
    '/fonts/inter-var.woff2',
    '/icon.png',
    '/icon.svg',
    '/favicon.ico',
    '/images/logo.svg',
  ])('static asset %s → cache-first', (path) => {
    expect(classify(path)).toBe('cache-first');
  });

  it.each(['/v1/mail/threads', '/v1/accounts', '/v1', '/api/auth/get-session', '/api/auth/token'])(
    'API/auth path %s → network-only',
    (path) => {
      expect(classify(path)).toBe('network-only');
    }
  );

  it.each(['POST', 'PUT', 'PATCH', 'DELETE'])('%s requests → network-only', (method) => {
    expect(classify('/_next/static/chunks/main.js', method)).toBe('network-only');
    expect(classify('/mail', method)).toBe('network-only');
  });

  it('any request carrying Authorization → network-only', () => {
    expect(classify('/_next/static/chunks/main.js', 'GET', true)).toBe('network-only');
    expect(classify('/mail', 'GET', true)).toBe('network-only');
  });

  it.each(['/', '/mail', '/calendar', '/settings', '/mail?split=vip'])(
    'document navigation %s → navigation',
    (path) => {
      expect(classify(path)).toBe('navigation');
    }
  );

  it('lowercase method strings are treated as their uppercase equivalent', () => {
    expect(classify('/icon.png', 'get')).toBe('cache-first');
    expect(classify('/mail', 'post')).toBe('network-only');
  });
});

describe('cache versioning', () => {
  it('cache name is a versioned calendium-static cache', () => {
    expect(SW_CACHE).toMatch(/^calendium-static-v\d+$/);
  });

  it('flags older calendium-static caches as stale, never the current one', () => {
    expect(isStaleCache(SW_CACHE)).toBe(false);
    expect(isStaleCache('calendium-static-v0')).toBe(true);
    expect(isStaleCache('calendium-static-v999')).toBe(true);
  });

  it('never touches caches owned by other systems', () => {
    expect(isStaleCache('workbox-precache-v2')).toBe(false);
    expect(isStaleCache('calendium-data')).toBe(false);
  });
});

describe('offline shell', () => {
  it('offline fallback URL is the /offline route', () => {
    expect(OFFLINE_URL).toBe('/offline');
  });
});
