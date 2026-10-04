import type { EnvLike } from '@/lib/auth-env';

const HEX64 = /^[0-9a-f]{64}$/i;

export interface RuntimeEnvProblem {
  name: string;
  reason: string;
}

/**
 * Validates the server-only env the web app needs at RUNTIME (never at build:
 * `next build` runs with no secrets). BETTER_AUTH_SECRET was already required
 * by Better Auth (its strength is lib/auth-secret.ts's job); INTERNAL_API_SECRET
 * is what account deletion uses to reach the Go API and must equal the API's
 * own value (parsed there like TOKEN_ENCRYPTION_KEY: 64 hex chars).
 */
export function checkRuntimeEnv(env: EnvLike): RuntimeEnvProblem[] {
  const problems: RuntimeEnvProblem[] = [];
  if (!env.BETTER_AUTH_SECRET) {
    problems.push({ name: 'BETTER_AUTH_SECRET', reason: 'is required (openssl rand -base64 32)' });
  }
  const internal = env.INTERNAL_API_SECRET?.trim();
  if (!internal) {
    problems.push({
      name: 'INTERNAL_API_SECRET',
      reason: 'is required (openssl rand -hex 32; must match the API service)',
    });
  } else if (!HEX64.test(internal)) {
    problems.push({ name: 'INTERNAL_API_SECRET', reason: 'must be exactly 64 hex chars' });
  }
  return problems;
}

/**
 * Boot gate called from instrumentation.ts: a production, non-demo web server
 * refuses to start without the secrets account deletion depends on. Exempt:
 * `next build` (secret-free Docker build stage), `next dev`, Vitest, and demo
 * mode (the Playwright suite runs `next start` in demo mode and never reaches
 * the internal route).
 */
export function assertRuntimeEnv(env: EnvLike = process.env): void {
  if (env.NEXT_PHASE === 'phase-production-build') return;
  if (env.NODE_ENV !== 'production' || env.NEXT_PUBLIC_DEMO_MODE === 'true') return;
  const problems = checkRuntimeEnv(env);
  if (problems.length > 0) {
    throw new Error(`Refusing to start: ${problems.map((p) => `${p.name} ${p.reason}`).join('; ')}`);
  }
}
