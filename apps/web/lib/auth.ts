import { betterAuth } from 'better-auth';
import { APIError, createAuthMiddleware, getSessionFromCtx } from 'better-auth/api';
import { bearer, jwt, oneTimeToken } from 'better-auth/plugins';
import { nextCookies } from 'better-auth/next-js';
import { expo } from '@better-auth/expo';
import { Pool } from 'pg';

import { CLIENT_IP_HEADER, buildTrustedOrigins, passwordPolicyError, rateLimitRules } from '@/lib/auth-env';
import { readMailConfig } from '@/lib/email/config';
import { resetPasswordEmail } from '@/lib/email/templates/reset-password';
import { verifyEmail } from '@/lib/email/templates/verify-email';
import { sendMail } from '@/lib/email/transport';

/**
 * Better Auth server — the identity provider for web, desktop, and mobile.
 * Hosted by the Next.js web app at `${BETTER_AUTH_URL}/api/auth/*` and backed
 * by the SAME Postgres as the Go API (its own tables: user, session, account,
 * verification, jwks, rateLimit — see backend/migrations/0002 and 0028).
 *
 * The Go backend is a pure resource server: it verifies the EdDSA JWTs minted
 * here (GET /api/auth/token) by fetching JWKS from /api/auth/jwks.
 */

/**
 * Lazily-created pg Pool. Constructing a Pool does NOT open a connection, so
 * this is safe at module load and keeps `next build` fully offline.
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
 * SMTP is read once at module load. readMailConfig returns {configured:false}
 * when no SMTP_* is set (self-host without email, `next build`) and throws on
 * a half-set block so a typo surfaces at boot. The cloud-mode requirement is
 * enforced by instrumentation.ts before any request is served.
 */
const mail = readMailConfig(process.env);

/**
 * Password policy, enforced server-side on every password-setting route (the
 * pages mirror it for instant feedback). The email the policy checks against
 * comes from the request body (sign-up), the session (change-password) or the
 * reset token's verification row (reset-password).
 */
const passwordPolicyHook = createAuthMiddleware(async (ctx) => {
  if (ctx.path !== '/sign-up/email' && ctx.path !== '/change-password' && ctx.path !== '/reset-password') return;
  const body = (ctx.body ?? {}) as { password?: unknown; newPassword?: unknown; email?: unknown; token?: unknown };
  const password =
    typeof body.password === 'string' ? body.password : typeof body.newPassword === 'string' ? body.newPassword : '';
  let email: string | null = null;
  if (ctx.path === '/sign-up/email') {
    email = typeof body.email === 'string' ? body.email : null;
  } else if (ctx.path === '/change-password') {
    email = (await getSessionFromCtx(ctx))?.user.email ?? null;
  } else if (typeof body.token === 'string') {
    const verification = await ctx.context.internalAdapter.findVerificationValue(`reset-password:${body.token}`);
    if (verification) email = (await ctx.context.internalAdapter.findUserById(verification.value))?.email ?? null;
  }
  const violation = passwordPolicyError(password, email);
  if (violation) throw new APIError('BAD_REQUEST', { message: violation.message, code: violation.code });
});

export const auth = betterAuth({
  database: db(),
  secret: process.env.BETTER_AUTH_SECRET,
  baseURL: process.env.BETTER_AUTH_URL,
  trustedOrigins: buildTrustedOrigins(process.env),
  emailAndPassword: {
    enabled: true,
    minPasswordLength: 10,
    maxPasswordLength: 128,
    // With SMTP, new accounts must verify before signing in (and sign-up
    // answers generically for duplicates); without it, self-host keeps the
    // auto-sign-in behaviour and surfaces the duplicate error.
    requireEmailVerification: mail.configured,
    resetPasswordTokenExpiresIn: 3600,
    revokeSessionsOnPasswordReset: true,
    sendResetPassword: mail.configured
      ? async ({ user, url }) => {
          await sendMail(resetPasswordEmail({ to: user.email, url }));
        }
      : undefined,
  },
  emailVerification: mail.configured
    ? {
        sendOnSignUp: true,
        sendOnSignIn: true,
        autoSignInAfterVerification: true,
        expiresIn: 86_400,
        sendVerificationEmail: async ({ user, url }) => {
          await sendMail(verifyEmail({ to: user.email, url }));
        },
      }
    : undefined,
  // Per-IP limits in Postgres (replica-safe, restart-safe); keys are ip+path.
  rateLimit: { enabled: true, storage: 'database', window: 60, max: 100, customRules: rateLimitRules() },
  // The ONLY header Better Auth reads the client IP from; route.ts overwrites
  // it on every request from X-Forwarded-For per TRUST_PROXY.
  advanced: { ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] } },
  hooks: { before: passwordPolicyHook },
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
    // Short-lived one-time tokens for the desktop browser → app handoff (see
    // apps/web/app/desktop-callback/page.tsx).
    oneTimeToken(),
    // MUST be last: makes Set-Cookie from server actions/route handlers work.
    nextCookies(),
  ],
});
