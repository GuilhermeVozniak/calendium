/**
 * Next.js instrumentation hook (Node runtime only). Runs once when
 * `next start` / `next dev` boot the server — never during `next build` — so
 * a broken email/auth configuration aborts the process with the exact
 * variable named instead of failing on the first sign-up. Mirrors
 * config.FromEnv + ValidateCloudEmail on the Go side.
 */
export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== 'nodejs') return;
  const { assertMailConfigForMode } = await import('@/lib/email/config');
  const { startupWarnings } = await import('@/lib/auth-env');
  const mail = assertMailConfigForMode(process.env);
  if (!mail.configured) {
    console.warn('email: disabled (no SMTP_HOST); verification off, invitations fall back to links');
  }
  for (const warning of startupWarnings(process.env)) console.warn(warning);
}
