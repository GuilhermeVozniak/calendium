import { APIError } from 'better-auth/api';

import type { EnvLike } from '@/lib/auth-env';

/**
 * Server-only bridge from Better Auth's account-deletion hook to the Go API's
 * secret-authenticated internal surface. Never import from client components:
 * it reads INTERNAL_API_SECRET.
 */

export const INTERNAL_API_TIMEOUT_MS = 15_000;

export interface PurgeTeam {
  id: string;
  name: string;
}

interface PurgeErrorBody {
  error?: { code?: string; message?: string; details?: { teams?: PurgeTeam[] } };
}

/** Base URL of the Go API as reachable from the web server (compose: http://api:8080). */
export function internalApiUrl(env: EnvLike = process.env): string {
  return (env.INTERNAL_API_URL || 'http://localhost:8080').replace(/\/+$/, '');
}

/**
 * DELETE /v1/internal/users/{id}. Throwing aborts Better Auth's deleteUser
 * and keeps the auth rows intact: 409 → CONFLICT with the API's envelope
 * (owns_teams + details.teams), anything else → SERVICE_UNAVAILABLE. Logs
 * carry the status code only — never the user's id, email or name.
 */
export async function purgeOnApi(
  userId: string,
  opts: { fetchImpl?: typeof fetch; env?: EnvLike } = {}
): Promise<void> {
  const env = opts.env ?? process.env;
  const doFetch = opts.fetchImpl ?? fetch;
  const secret = env.INTERNAL_API_SECRET?.trim();
  if (!secret) {
    console.error('[account-delete] INTERNAL_API_SECRET is not configured');
    throw new APIError('SERVICE_UNAVAILABLE', { message: 'Account deletion is not available right now.' });
  }
  let res: Response;
  try {
    res = await doFetch(`${internalApiUrl(env)}/v1/internal/users/${encodeURIComponent(userId)}`, {
      method: 'DELETE',
      headers: { 'X-Internal-Secret': secret },
      signal: AbortSignal.timeout(INTERNAL_API_TIMEOUT_MS),
    });
  } catch (err) {
    console.error('[account-delete] purge request failed:', err instanceof Error ? err.name : 'error');
    throw new APIError('SERVICE_UNAVAILABLE', { message: 'Could not delete your account. Try again.' });
  }
  if (res.status === 204) return;
  console.error('[account-delete] purge status', res.status);
  if (res.status === 409) {
    const body = (await res.json().catch(() => null)) as PurgeErrorBody | null;
    throw new APIError('CONFLICT', {
      code: body?.error?.code ?? 'conflict',
      message: body?.error?.message ?? 'Your account cannot be deleted yet.',
      details: body?.error?.details ?? {},
    });
  }
  throw new APIError('SERVICE_UNAVAILABLE', { message: 'Could not delete your account. Try again.' });
}

export interface VerificationStore {
  query(sql: string, params: unknown[]): Promise<unknown>;
}

/**
 * afterDelete cleanup: Better Auth keys "verification" rows by email
 * (identifier), with no FK to "user", so they would outlive the account.
 * Best effort — the account is already gone when this runs.
 */
export async function deleteVerificationRows(store: VerificationStore, email: string): Promise<void> {
  try {
    await store.query('DELETE FROM "verification" WHERE identifier = $1', [email]);
  } catch (err) {
    console.error('[account-delete] verification cleanup failed:', err instanceof Error ? err.name : 'error');
  }
}
