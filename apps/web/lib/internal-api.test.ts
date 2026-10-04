import { APIError } from 'better-auth/api';
import { afterEach, beforeEach, describe, expect, it, type MockInstance, vi } from 'vitest';

import { deleteVerificationRows, INTERNAL_API_TIMEOUT_MS, internalApiUrl, purgeOnApi } from '@/lib/internal-api';

const SECRET = 'ab'.repeat(32);
const ENV = { INTERNAL_API_URL: 'http://api:8080/', INTERNAL_API_SECRET: SECRET };

function fetchReturning(status: number, body?: unknown) {
  return vi.fn(
    async () =>
      new Response(body === undefined ? null : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
  );
}

let errorLog: MockInstance<typeof console.error>;
beforeEach(() => {
  errorLog = vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('internalApiUrl', () => {
  it('defaults to localhost:8080 and strips trailing slashes', () => {
    expect(internalApiUrl({})).toBe('http://localhost:8080');
    expect(internalApiUrl(ENV)).toBe('http://api:8080');
  });
});

describe('purgeOnApi', () => {
  it('DELETEs /v1/internal/users/{id} with the secret header and an abort signal', async () => {
    const timeout = vi.spyOn(AbortSignal, 'timeout');
    const fetchImpl = fetchReturning(204);
    await purgeOnApi('user 1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV });
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('http://api:8080/v1/internal/users/user%201');
    expect(init.method).toBe('DELETE');
    expect((init.headers as Record<string, string>)['X-Internal-Secret']).toBe(SECRET);
    expect(init.signal).toBeInstanceOf(AbortSignal);
    expect(timeout).toHaveBeenCalledWith(INTERNAL_API_TIMEOUT_MS);
    expect(INTERNAL_API_TIMEOUT_MS).toBe(15_000);
  });

  it('maps 409 to APIError CONFLICT carrying the envelope code, message and details', async () => {
    const fetchImpl = fetchReturning(409, {
      error: { code: 'owns_teams', message: 'Transfer first', details: { teams: [{ id: 't1', name: 'Design' }] } },
    });
    const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
      (e: unknown) => e
    )) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(409);
    expect(err.body?.code).toBe('owns_teams');
    expect(err.body?.message).toBe('Transfer first');
    expect((err.body as { details?: { teams?: { name: string }[] } }).details?.teams?.[0]?.name).toBe('Design');
  });

  it('maps any other non-204 status to SERVICE_UNAVAILABLE', async () => {
    for (const status of [500, 502, 404, 200]) {
      const fetchImpl = fetchReturning(status, { error: { code: 'x', message: 'y' } });
      const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
        (e: unknown) => e
      )) as APIError;
      expect(err).toBeInstanceOf(APIError);
      expect(err.statusCode).toBe(503);
    }
  });

  it('maps a network failure / timeout to SERVICE_UNAVAILABLE', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new DOMException('aborted', 'TimeoutError');
    });
    const err = (await purgeOnApi('u1', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(
      (e: unknown) => e
    )) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(503);
  });

  it('refuses without a configured secret and never calls fetch', async () => {
    const fetchImpl = fetchReturning(204);
    const err = (await purgeOnApi('u1', {
      fetchImpl: fetchImpl as unknown as typeof fetch,
      env: { INTERNAL_API_URL: 'http://api:8080' },
    }).catch((e: unknown) => e)) as APIError;
    expect(err).toBeInstanceOf(APIError);
    expect(err.statusCode).toBe(503);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('logs the status code only — never the user id or the secret', async () => {
    const fetchImpl = fetchReturning(500, { error: { code: 'internal', message: 'x' } });
    await purgeOnApi('user-secret-id', { fetchImpl: fetchImpl as unknown as typeof fetch, env: ENV }).catch(() => {});
    const logged = JSON.stringify(errorLog.mock.calls);
    expect(logged).toContain('500');
    expect(logged).not.toContain('user-secret-id');
    expect(logged).not.toContain(SECRET);
  });
});

describe('deleteVerificationRows', () => {
  it('deletes by identifier', async () => {
    const query = vi.fn(async () => ({ rowCount: 1 }));
    await deleteVerificationRows({ query }, 'me@example.com');
    expect(query).toHaveBeenCalledWith('DELETE FROM "verification" WHERE identifier = $1', ['me@example.com']);
  });

  it('is best effort: a failing query never throws', async () => {
    const query = vi.fn(async () => {
      throw new Error('db down');
    });
    await expect(deleteVerificationRows({ query }, 'me@example.com')).resolves.toBeUndefined();
  });
});
