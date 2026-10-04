/**
 * Pure, DB-free helpers behind lib/auth.ts and app/api/auth/[...all]/route.ts:
 * trusted origins, CORS reflection, rate-limit rules, the client-IP header
 * name, the password policy and startup warnings (client-IP resolution is
 * server-only: lib/client-ip.ts). Everything takes `env`
 * explicitly so it is unit-testable without process.env or Better Auth.
 * Mirrors the Go side (config.FromEnv + httpapi.corsMiddleware).
 */

export type EnvLike = Record<string, string | undefined>;

/**
 * Wails desktop WebView page origins. macOS/Linux serve the app from
 * `wails://wails`; Windows uses `http://wails.localhost`. They are
 * production client origins and are ALWAYS trusted — never gated on NODE_ENV.
 */
export const WAILS_ORIGINS = [
  'wails://wails',
  'wails://wails.localhost',
  'http://wails.localhost',
  'https://wails.localhost',
] as const;

/**
 * Sign in with Apple posts its callback (`response_mode=form_post`) from this
 * origin. Trusted for the CSRF/origin check, never reflected in CORS (a form
 * POST is a navigation, not a fetch).
 */
export const APPLE_FORM_POST_ORIGIN = 'https://appleid.apple.com';

/** Native deep-link scheme (mobile OAuth callbacks). */
export const NATIVE_SCHEME_ORIGIN = 'calendium://';

/**
 * The server-set header Better Auth reads the client IP from. route.ts
 * overwrites it on every request (lib/client-ip.ts), so a caller can never
 * choose its bucket.
 */
export const CLIENT_IP_HEADER = 'x-calendium-client-ip';

const stripSlash = (s: string) => s.trim().replace(/\/+$/, '');

/** localhost / 127.0.0.1 origins are trusted outside production, or when explicitly re-enabled. */
export function devOriginsAllowed(env: EnvLike): boolean {
  return env.NODE_ENV !== 'production' || env.ALLOW_DEV_ORIGINS === 'true';
}

/** CORS_ALLOWED_ORIGINS: comma-separated exact origins (trimmed, trailing slashes dropped). */
export function envAllowedOrigins(env: EnvLike): string[] {
  return (env.CORS_ALLOWED_ORIGINS ?? '').split(',').map(stripSlash).filter(Boolean);
}

/** Operator-configured exact origins: BETTER_AUTH_URL, PUBLIC_WEB_URL and CORS_ALLOWED_ORIGINS. */
export function explicitOrigins(env: EnvLike): string[] {
  const out: string[] = [];
  for (const raw of [env.BETTER_AUTH_URL, env.PUBLIC_WEB_URL]) {
    const origin = raw ? stripSlash(raw) : '';
    if (origin) out.push(origin);
  }
  out.push(...envAllowedOrigins(env));
  return Array.from(new Set(out));
}

/**
 * Origins Better Auth trusts for its CSRF/callback checks. Fixed: the native
 * scheme, Apple's form_post origin and the Wails origins. From env: the
 * explicit origins. Dev only: localhost / 127.0.0.1 wildcards.
 */
export function buildTrustedOrigins(env: EnvLike): string[] {
  const origins: string[] = [NATIVE_SCHEME_ORIGIN, APPLE_FORM_POST_ORIGIN, ...WAILS_ORIGINS, ...explicitOrigins(env)];
  if (devOriginsAllowed(env)) origins.push('http://localhost:*', 'http://127.0.0.1:*');
  return Array.from(new Set(origins));
}

/** http(s)://localhost | 127.0.0.1 | ::1 on any port — the dev servers. */
function isLocalhostDevOrigin(url: URL): boolean {
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return false;
  return (
    url.hostname === 'localhost' ||
    url.hostname === '127.0.0.1' ||
    url.hostname === '::1' ||
    url.hostname === '[::1]'
  );
}

/**
 * Whether an `Origin` header is reflected into Access-Control-Allow-Origin
 * (credentialed CORS forbids `*`). Always: the Wails origins (any port on
 * wails.localhost) and the explicit origins. Gated on devOriginsAllowed:
 * localhost / 127.0.0.1 / ::1. Never: appleid.apple.com or anything else.
 * Mirrors the Go API's corsMiddleware so the whole stack admits the same
 * clients.
 */
export function isAllowedOrigin(origin: string | null | undefined, env: EnvLike): boolean {
  if (!origin) return false;
  if ((WAILS_ORIGINS as readonly string[]).includes(origin)) return true;
  if (explicitOrigins(env).includes(stripSlash(origin))) return true;
  try {
    const url = new URL(origin);
    if (url.hostname === 'wails.localhost' && (url.protocol === 'http:' || url.protocol === 'https:')) {
      return true;
    }
    return devOriginsAllowed(env) && isLocalhostDevOrigin(url);
  } catch {
    return false;
  }
}

export interface RateLimitRule {
  window: number;
  max: number;
}

/**
 * Per-IP Better Auth `customRules`, keyed by the exact path after /api/auth.
 * Better Auth applies them to GET too, so the jwt plugin's /token is limited;
 * /forget-password is the deprecated alias of /request-password-reset.
 */
export function rateLimitRules(): Record<string, RateLimitRule> {
  return {
    '/sign-in/email': { window: 60, max: 5 },
    '/sign-up/email': { window: 60, max: 3 },
    '/request-password-reset': { window: 600, max: 3 },
    '/forget-password': { window: 600, max: 3 },
    '/send-verification-email': { window: 600, max: 3 },
    '/token': { window: 60, max: 60 },
  };
}

export const PASSWORD_MIN_LENGTH = 10;
export const PASSWORD_MAX_LENGTH = 128;

export interface PasswordPolicyError {
  code: 'PASSWORD_TOO_SHORT' | 'PASSWORD_TOO_LONG' | 'PASSWORD_CONTAINS_EMAIL';
  message: string;
}

/**
 * Server-side password policy (mirrored client-side for instant feedback):
 * 10–128 characters and no email local part (case-insensitive). Local parts
 * shorter than 3 characters are ignored so `ab@x.test` cannot reject every
 * password containing "ab".
 */
export function passwordPolicyError(password: string, email: string | null | undefined): PasswordPolicyError | null {
  if (password.length < PASSWORD_MIN_LENGTH) {
    return { code: 'PASSWORD_TOO_SHORT', message: `Password must be at least ${PASSWORD_MIN_LENGTH} characters.` };
  }
  if (password.length > PASSWORD_MAX_LENGTH) {
    return { code: 'PASSWORD_TOO_LONG', message: `Password must be at most ${PASSWORD_MAX_LENGTH} characters.` };
  }
  const local = (email ?? '').split('@')[0]?.trim().toLowerCase() ?? '';
  if (local.length >= 3 && password.toLowerCase().includes(local)) {
    return { code: 'PASSWORD_CONTAINS_EMAIL', message: 'Password must not contain your email address.' };
  }
  return null;
}

/** Production misconfiguration warnings, logged once at boot by instrumentation.ts. */
export function startupWarnings(env: EnvLike): string[] {
  const out: string[] = [];
  if (env.NODE_ENV !== 'production') return out;
  if (env.TRUST_PROXY !== 'true') {
    out.push(
      'TRUST_PROXY is not true in production: auth rate limits key on the immediate peer, so behind a reverse proxy every client shares the proxy\'s bucket on every /api/auth endpoint (sign-in, session reads, JWT minting). Behind the bundled Caddy profile (or any proxy in TRUSTED_PROXY_CIDRS) set TRUST_PROXY=true (the docker compose default) — see docs/self-hosting/security.md.'
    );
  }
  if (env.ALLOW_DEV_ORIGINS === 'true') {
    out.push(
      'ALLOW_DEV_ORIGINS=true in production: http://localhost and http://127.0.0.1 origins are trusted for auth and reflected in CORS. Unset it unless you are debugging.'
    );
  }
  return out;
}
