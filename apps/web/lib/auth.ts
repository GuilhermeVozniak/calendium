import { betterAuth } from 'better-auth';
import { bearer, jwt, oneTimeToken } from 'better-auth/plugins';
import { nextCookies } from 'better-auth/next-js';
import { expo } from '@better-auth/expo';
import { Pool } from 'pg';

/**
 * Better Auth server — the identity provider for web, desktop, and mobile.
 * Hosted by the Next.js web app at `${BETTER_AUTH_URL}/api/auth/*` and backed
 * by the SAME Postgres as the Go API (its own tables: user, session, account,
 * verification, jwks — see backend/migrations/0002_better_auth.sql).
 *
 * The Go backend is a pure resource server: it verifies the EdDSA JWTs minted
 * here (GET /api/auth/token) by fetching JWKS from /api/auth/jwks. No Supabase,
 * no shared HS256 secret.
 */

/**
 * Lazily-created pg Pool. Constructing a Pool does NOT open a connection, so
 * this is safe at module load and keeps `next build` fully offline — the pool
 * only dials Postgres on the first query at runtime.
 */
let pool: Pool | undefined;
function db(): Pool {
  pool ??= new Pool({ connectionString: process.env.DATABASE_URL });
  return pool;
}

/** Only register a social provider when BOTH its id and secret are configured. */
function socialProviders() {
  const providers: Record<string, { clientId: string; clientSecret: string }> = {};
  if (process.env.GOOGLE_CLIENT_ID && process.env.GOOGLE_CLIENT_SECRET) {
    providers.google = {
      clientId: process.env.GOOGLE_CLIENT_ID,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET,
    };
  }
  if (process.env.APPLE_CLIENT_ID && process.env.APPLE_CLIENT_SECRET) {
    providers.apple = {
      clientId: process.env.APPLE_CLIENT_ID,
      clientSecret: process.env.APPLE_CLIENT_SECRET,
    };
  }
  return providers;
}

/**
 * Wails desktop WebView page origins. macOS/Linux serve the app from
 * `wails://wails`; Windows uses `http://wails.localhost`. These are a DIFFERENT
 * origin from the server they call, so they need explicit CORS + CSRF trust.
 */
const WAILS_ORIGINS = [
  'wails://wails',
  'wails://wails.localhost',
  'http://wails.localhost',
  'https://wails.localhost',
];

/** Extra browser origins an operator allows (comma-separated exact origins). */
function envAllowedOrigins(): string[] {
  return (process.env.CORS_ALLOWED_ORIGINS ?? '')
    .split(',')
    .map((o) => o.trim())
    .filter(Boolean);
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
 * Whether an `Origin` header should be reflected into
 * `Access-Control-Allow-Origin` (see app/api/auth/[...all]/route.ts). Mirrors
 * the Go API's CORS allowlist (wails origins + localhost dev + the
 * CORS_ALLOWED_ORIGINS env) so the whole stack admits the same clients.
 * Credentialed CORS forbids `*`, so the caller reflects the exact origin only
 * when this returns true.
 */
export function isAllowedOrigin(origin: string | null | undefined): boolean {
  if (!origin) return false;
  if (WAILS_ORIGINS.includes(origin)) return true;
  if (envAllowedOrigins().includes(origin)) return true;
  try {
    const url = new URL(origin);
    if (
      url.hostname === 'wails.localhost' &&
      (url.protocol === 'http:' || url.protocol === 'https:')
    ) {
      return true;
    }
    return isLocalhostDevOrigin(url);
  } catch {
    return false;
  }
}

/**
 * Origins Better Auth trusts for its own CSRF/callback checks — the native
 * deep-link scheme, the Wails WebView origins, localhost dev servers, and any
 * operator-provided CORS_ALLOWED_ORIGINS. Same clients the CORS layer reflects.
 */
function trustedOrigins() {
  const origins = [
    'calendium://',
    ...WAILS_ORIGINS,
    'http://localhost:*',
    'http://127.0.0.1:*',
    ...envAllowedOrigins(),
  ];
  if (process.env.BETTER_AUTH_URL) origins.push(process.env.BETTER_AUTH_URL);
  return origins;
}

export const auth = betterAuth({
  database: db(),
  secret: process.env.BETTER_AUTH_SECRET,
  baseURL: process.env.BETTER_AUTH_URL,
  trustedOrigins: trustedOrigins(),
  emailAndPassword: { enabled: true },
  socialProviders: socialProviders(),
  plugins: [
    // Asymmetric EdDSA (Ed25519) JWTs + JWKS at /api/auth/jwks. The token
    // carries sub=userId and iss=BETTER_AUTH_URL by default; add email/name so
    // the Go resource server can mirror the user without a DB round-trip.
    jwt({
      jwt: {
        definePayload: ({ user }) => ({
          email: user.email,
          name: user.name,
        }),
      },
    }),
    // Accept `Authorization: Bearer <session-token>` on Better Auth's own
    // endpoints for native (desktop/mobile) clients.
    bearer(),
    // Mobile (Expo) deep-link + secure-store session support.
    expo(),
    // Short-lived one-time tokens for the desktop browser → app handoff: the
    // web mints one at /desktop-callback and the desktop app verifies it (via
    // {authBaseUrl}/one-time-token/verify) to obtain a session. See
    // apps/web/app/desktop-callback/page.tsx.
    oneTimeToken(),
    // MUST be last: makes Set-Cookie from server actions/route handlers work.
    nextCookies(),
  ],
});
