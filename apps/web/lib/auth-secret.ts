import type { EnvLike } from '@/lib/auth-env';

export const MIN_BETTER_AUTH_SECRET_BYTES = 32;

/**
 * Refuses to run Better Auth with a weak root secret (it signs sessions and
 * encrypts the JWKS private keys). Skipped during `next build`: the Docker
 * build stage has no secret and must stay secret-free. Called from
 * instrumentation.ts (server start) and lib/auth.ts (module load).
 */
export function assertBetterAuthSecret(env: EnvLike = process.env): void {
  if (env.NEXT_PHASE === 'phase-production-build') return;
  if (Buffer.byteLength(env.BETTER_AUTH_SECRET ?? '', 'utf8') >= MIN_BETTER_AUTH_SECRET_BYTES) return;
  throw new Error('BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32');
}
