import { betterAuth } from 'better-auth';
import { bearer, jwt } from 'better-auth/plugins';
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

function trustedOrigins() {
  const origins = ['calendium://', 'wails://wails'];
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
    // MUST be last: makes Set-Cookie from server actions/route handlers work.
    nextCookies(),
  ],
});
