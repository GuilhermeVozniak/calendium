/**
 * Pure request-classification logic for the service worker in public/sw.js.
 *
 * IMPORTANT: sw.js must stay dependency-free (it is served verbatim from
 * public/), so it inlines a mirror of these functions. Keep both in sync —
 * this module exists so the logic is unit-testable (lib/sw-caching.test.ts)
 * and is intentionally imported by no app code.
 *
 * Scope discipline: the SW caches static assets and the offline shell ONLY.
 * API data lives in the TanStack persisted cache — /v1 responses are never
 * cached here (that would create a second, conflicting source of truth).
 */

/** Versioned static-asset cache. Bump the suffix to invalidate on deploy. */
export const SW_CACHE = 'calendium-static-v1';

/** Pre-cached offline shell returned when a navigation fails while offline. */
export const OFFLINE_URL = '/offline';

export type CacheStrategy = 'cache-first' | 'network-only' | 'navigation';

/**
 * cache-first: /_next/static/**, /fonts/**, .png/.svg/.ico under /;
 * network-only: anything with Authorization, non-GET, /v1/**, /api/auth/**;
 * navigation requests: network-first with cached '/offline' fallback.
 */
export function classifyRequest(url: URL, method: string, hasAuth: boolean): CacheStrategy {
  if (method.toUpperCase() !== 'GET' || hasAuth) return 'network-only';
  const path = url.pathname;
  if (path === '/v1' || path.startsWith('/v1/') || path.startsWith('/api/auth/')) {
    return 'network-only';
  }
  if (
    path.startsWith('/_next/static/') ||
    path.startsWith('/fonts/') ||
    /\.(png|svg|ico)$/.test(path)
  ) {
    return 'cache-first';
  }
  return 'navigation';
}

/** True for calendium-static-* caches from previous SW versions (deleted on activate). */
export function isStaleCache(name: string): boolean {
  return name.startsWith('calendium-static-') && name !== SW_CACHE;
}
