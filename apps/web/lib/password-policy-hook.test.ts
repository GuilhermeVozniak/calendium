// @vitest-environment node
import { betterAuth } from 'better-auth';
import { memoryAdapter } from 'better-auth/adapters/memory';
import { beforeEach, describe, expect, it } from 'vitest';

import { passwordPolicyHook } from '@/lib/password-policy-hook';

/**
 * The policy hook on the real Better Auth router (in-memory DB): the hook
 * sees the RAW body before the endpoint's schema strips unknown keys, so it
 * must check the field the endpoint will actually store.
 */
const ORIGIN = 'http://localhost:3000';
const EMAIL = 'ada.lovelace@example.test';
const GOOD = 'correct-horse-battery';
const CONTAINS_EMAIL = 'ada.lovelace-x1';

let resetToken: string | null;
let auth: ReturnType<typeof makeAuth>;

function makeAuth() {
  return betterAuth({
    database: memoryAdapter({ user: [], session: [], account: [], verification: [] }),
    secret: 'test-secret-test-secret-test-secret-0123',
    baseURL: ORIGIN,
    emailAndPassword: {
      enabled: true,
      minPasswordLength: 10,
      maxPasswordLength: 128,
      sendResetPassword: async ({ token }) => {
        resetToken = token;
      },
    },
    hooks: { before: passwordPolicyHook },
    logger: { disabled: true },
  });
}

function call(path: string, body: unknown, headers: Record<string, string> = {}): Promise<Response> {
  return auth.handler(
    new Request(`${ORIGIN}/api/auth${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', origin: ORIGIN, ...headers },
      body: JSON.stringify(body),
    })
  );
}

async function signUp(): Promise<string> {
  const res = await call('/sign-up/email', { name: 'Ada', email: EMAIL, password: GOOD });
  expect(res.status).toBe(200);
  const cookie = res.headers.get('set-cookie') ?? '';
  return cookie
    .split(/,(?=\s*[\w.-]+=)/)
    .map((c) => c.split(';')[0]?.trim())
    .join('; ');
}

async function requestReset(): Promise<string> {
  resetToken = null;
  await call('/request-password-reset', { email: EMAIL, redirectTo: '/reset-password' });
  expect(resetToken).toBeTruthy();
  return String(resetToken);
}

async function errorCode(res: Response): Promise<string | undefined> {
  return ((await res.json()) as { code?: string }).code;
}

beforeEach(() => {
  auth = makeAuth();
});

describe('password policy hook', () => {
  it('sign-up: rejects a password containing the email local part', async () => {
    const res = await call('/sign-up/email', { name: 'Ada', email: EMAIL, password: CONTAINS_EMAIL });
    expect(res.status).toBe(400);
    expect(await errorCode(res)).toBe('PASSWORD_CONTAINS_EMAIL');
  });

  it('change-password: validates newPassword even when a compliant decoy `password` is sent', async () => {
    const cookie = await signUp();
    const res = await call(
      '/change-password',
      { currentPassword: GOOD, newPassword: CONTAINS_EMAIL, password: 'decoy-ok-123456' },
      { cookie }
    );
    expect(res.status).toBe(400);
    expect(await errorCode(res)).toBe('PASSWORD_CONTAINS_EMAIL');
  });

  it('change-password: a compliant newPassword passes', async () => {
    const cookie = await signUp();
    const res = await call('/change-password', { currentPassword: GOOD, newPassword: 'another-good-passphrase' }, { cookie });
    expect(res.status).toBe(200);
  });

  it('reset-password: validates newPassword even when a compliant decoy `password` is sent', async () => {
    await signUp();
    const token = await requestReset();
    const res = await call('/reset-password', { token, newPassword: CONTAINS_EMAIL, password: 'decoy-ok-123456' });
    expect(res.status).toBe(400);
    expect(await errorCode(res)).toBe('PASSWORD_CONTAINS_EMAIL');
  });

  it('reset-password: resolves the token from the query string for the email rule', async () => {
    await signUp();
    const token = await requestReset();
    const res = await call(`/reset-password?token=${encodeURIComponent(token)}`, { newPassword: CONTAINS_EMAIL });
    expect(res.status).toBe(400);
    expect(await errorCode(res)).toBe('PASSWORD_CONTAINS_EMAIL');
  });

  it('reset-password: a compliant newPassword with a body token passes', async () => {
    await signUp();
    const token = await requestReset();
    const res = await call('/reset-password', { token, newPassword: 'another-good-passphrase' });
    expect(res.status).toBe(200);
  });

  it('reset-password: a too-short newPassword is rejected by the policy', async () => {
    await signUp();
    const token = await requestReset();
    const res = await call('/reset-password', { token, newPassword: 'short', password: 'decoy-ok-123456' });
    expect(res.status).toBe(400);
    expect(await errorCode(res)).toBe('PASSWORD_TOO_SHORT');
  });
});
