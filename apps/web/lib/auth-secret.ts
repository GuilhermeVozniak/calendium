import type { EnvLike } from '@/lib/auth-env';

export const MIN_BETTER_AUTH_SECRET_BYTES = 32;

/**
 * Example-file values that are long enough to pass the length check: the
 * apps/web/.env.example placeholder (the root .env.example ships it empty,
 * which the length check already refuses). Anything containing
 * "change-me"/"changeme" is refused too.
 */
const PLACEHOLDER_SECRETS = new Set(['replace-with-openssl-rand-base64-32']);
const PLACEHOLDER_PATTERN = /change-?me/i;

/**
 * Refuses to run Better Auth with a weak root secret (it signs sessions and
 * encrypts the JWKS private keys): under 32 bytes in every mode, and a
 * copied example placeholder in production. Skipped during `next build`:
 * the Docker build stage has no secret and must stay secret-free. Called
 * from instrumentation.ts (server start) and lib/auth.ts (module load).
 */
export function assertBetterAuthSecret(env: EnvLike = process.env): void {
  if (env.NEXT_PHASE === 'phase-production-build') return;
  const secret = env.BETTER_AUTH_SECRET ?? '';
  if (Buffer.byteLength(secret, 'utf8') < MIN_BETTER_AUTH_SECRET_BYTES) {
    throw new Error('BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32');
  }
  if (env.NODE_ENV === 'production' && (PLACEHOLDER_SECRETS.has(secret.trim()) || PLACEHOLDER_PATTERN.test(secret))) {
    throw new Error('BETTER_AUTH_SECRET is still a placeholder; generate one with: openssl rand -base64 32');
  }
}
