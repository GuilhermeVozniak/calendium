// Relative imports on purpose: next.config.ts loads this before the `@/`
// alias exists.
import { cspPolicy } from './csp';
import { env } from './env';

/** The five static headers next.config.ts sets on every route. */
export const STATIC_SECURITY_HEADERS = [
  { key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' },
  { key: 'X-Content-Type-Options', value: 'nosniff' },
  { key: 'X-Frame-Options', value: 'DENY' },
  { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
  // geolocation=(self), not (): lib/use-weather.ts falls back to
  // navigator.geolocation when the user has no home coordinates (spec amendment).
  { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=(self)' },
];

/**
 * /offline is force-static and precached by public/sw.js, so it cannot
 * carry a per-request nonce: it gets the same policy with 'unsafe-inline'
 * scripts. It renders no user content.
 */
export function offlineCsp(): string {
  return cspPolicy({ apiUrl: env.apiUrl, dev: process.env.NODE_ENV !== 'production' });
}

export function securityHeaders() {
  return [
    { source: '/(.*)', headers: STATIC_SECURITY_HEADERS },
    { source: '/offline', headers: [{ key: 'Content-Security-Policy', value: offlineCsp() }] },
  ];
}
