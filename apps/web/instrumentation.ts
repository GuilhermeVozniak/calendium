/**
 * Next.js instrumentation hook (Node runtime only). Runs once when
 * `next start` / `next dev` boot the server — never during `next build` — so
 * a broken email/auth configuration aborts the process with the exact
 * variable named instead of failing on the first sign-up. Mirrors
 * config.FromEnv + ValidateCloudEmail on the Go side. The edge runtime never
 * hosts Better Auth, and the build phase (Docker build stage) is secret-free.
 * Production (non-demo) also requires INTERNAL_API_SECRET for account deletion.
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== 'nodejs') return;
  if (process.env.NEXT_PHASE === 'phase-production-build') return;
  const { assertMailConfigForMode } = await import('@/lib/email/config');
  const { assertBooleanEnv, envBool, startupWarnings } = await import('@/lib/auth-env');
  const { assertBetterAuthSecret } = await import('@/lib/auth-secret');
  // Same boolean grammar as config.FromEnv: an invalid SELF_HOSTED,
  // SMTP_SECURE, ALLOW_DEV_ORIGINS or TRUST_PROXY stops the boot.
  assertBooleanEnv(process.env);
  // CSP_REPORT_ONLY (middleware.ts) uses the same grammar; blank = default.
  envBool(process.env, 'CSP_REPORT_ONLY');
  // A weak Better Auth root secret (signs sessions, encrypts the JWKS keys)
  // stops the boot in every mode; a copied example placeholder in production.
  assertBetterAuthSecret(process.env);
  // Account deletion reaches the Go API's internal purge route with
  // INTERNAL_API_SECRET: a production, non-demo server refuses to start
  // without a well-formed one (lib/runtime-env.ts).
  const { assertRuntimeEnv } = await import('@/lib/runtime-env');
  assertRuntimeEnv(process.env);
  const mail = assertMailConfigForMode(process.env);
  if (!mail.configured) {
    console.warn('email: disabled (no SMTP_HOST); verification off, invitations fall back to links');
  }
  for (const warning of startupWarnings(process.env)) console.warn(warning);
  // Client-IP keying (lib/client-ip.ts): a bad TRUSTED_PROXY_CIDRS stops the
  // boot, and production must run with the peer-appending preload or the
  // right-most X-Forwarded-For entry could be client-supplied. lib/client-ip
  // uses node:net, so its import sits inside the literal NEXT_RUNTIME check
  // that Next's compiler strips from the edge bundle.
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    const { peerAppendedToForwardedFor, proxyTrustFromEnv } = await import('@/lib/client-ip');
    proxyTrustFromEnv(process.env);
    if (process.env.NODE_ENV === 'production' && !peerAppendedToForwardedFor()) {
      console.warn(
        'scripts/forwarded-for-peer.cjs is not preloaded: a client reaching this server directly can choose its auth rate-limit key via X-Forwarded-For. Start it as `node --require ./apps/web/scripts/forwarded-for-peer.cjs apps/web/server.js` (the Docker image does).'
      );
    }
  }
}
