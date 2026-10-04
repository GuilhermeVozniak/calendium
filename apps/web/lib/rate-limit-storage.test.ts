// @vitest-environment node
import { betterAuth, type DBAdapter } from 'better-auth';
import { memoryAdapter } from 'better-auth/adapters/memory';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CLIENT_IP_HEADER } from '@/lib/auth-env';
import { rateLimitConfig } from '@/lib/rate-limit-storage';

/**
 * Drives the real Better Auth handler with lib/auth.ts's rate-limit config
 * over an in-memory database whose reads return "lastRequest" as a STRING —
 * exactly what node-postgres does for the int8 column in migration 0028.
 */
type Row = Record<string, unknown>;

function pgLikeMemoryDb(tables: Record<string, Row[]>) {
  const base = memoryAdapter(tables);
  return (options: Parameters<typeof base>[0]): DBAdapter => {
    const adapter = base(options);
    const stringify = <T,>(row: T): T => {
      const r = row as Row | null;
      return (r && typeof r.lastRequest === 'number' ? { ...r, lastRequest: String(r.lastRequest) } : row) as T;
    };
    return {
      ...adapter,
      findOne: async (args) => stringify(await adapter.findOne(args)),
      findMany: async (args) => (await adapter.findMany(args)).map(stringify),
    } as DBAdapter;
  };
}

const T0 = Date.UTC(2026, 9, 4, 12, 0, 0);
let tables: Record<string, Row[]>;
let auth: ReturnType<typeof makeAuth>;

function makeAuth() {
  let adapter: (() => Promise<DBAdapter>) | undefined;
  const instance = betterAuth({
    database: pgLikeMemoryDb(tables),
    secret: 'test-secret-test-secret-test-secret-0123',
    baseURL: 'http://localhost:3000',
    emailAndPassword: { enabled: true },
    rateLimit: rateLimitConfig(() => (adapter as () => Promise<DBAdapter>)()),
    advanced: { ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] } },
    logger: { disabled: true },
  });
  adapter = () => instance.$context.then((context) => context.adapter as unknown as DBAdapter);
  return instance;
}

async function post(path: string, ip: string, body: unknown): Promise<Response> {
  return auth.handler(
    new Request(`http://localhost:3000/api/auth${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', origin: 'http://localhost:3000', [CLIENT_IP_HEADER]: ip },
      body: JSON.stringify(body),
    })
  );
}

const resetRequest = (ip: string) => post('/request-password-reset', ip, { email: 'ada@example.test', redirectTo: '/reset-password' });
const signIn = (ip: string) => post('/sign-in/email', ip, { email: 'ada@example.test', password: 'wrong-password-1' });
/** Lets fire-and-forget pruning settle (only Date is faked). */
const settle = () => new Promise((r) => setTimeout(r, 20));

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(T0);
  tables = { user: [], session: [], account: [], verification: [], rateLimit: [] };
  auth = makeAuth();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('auth rate limiting (database storage)', () => {
  it('a blocked request carries X-Retry-After in whole seconds within the rule window', async () => {
    for (let i = 0; i < 5; i++) expect((await signIn('203.0.113.7')).status).not.toBe(429);
    vi.setSystemTime(T0 + 10_000);
    const blocked = await signIn('203.0.113.7');
    expect(blocked.status).toBe(429);
    const header = blocked.headers.get('X-Retry-After') ?? '';
    expect(header).toMatch(/^\d+$/);
    // Last allowed hit at T0, window 60 s, now T0+10 s → 50 s left.
    expect(Number(header)).toBe(50);
  });

  it('the 600 s password-reset rule holds after 60 s, even when another key resets its window', async () => {
    for (let i = 0; i < 3; i++) expect((await resetRequest('198.51.100.1')).status).not.toBe(429);
    expect((await resetRequest('198.51.100.1')).status).toBe(429);

    // Another client's 60 s window rolls over 61 s later — the moment Better
    // Auth prunes rate-limit rows.
    await signIn('198.51.100.2');
    vi.setSystemTime(T0 + 61_000);
    await signIn('198.51.100.2');
    await settle();

    vi.setSystemTime(T0 + 70_000);
    const fourth = await resetRequest('198.51.100.1');
    expect(fourth.status).toBe(429);
    const retryAfter = Number(fourth.headers.get('X-Retry-After'));
    expect(retryAfter).toBe(530);
    expect(retryAfter).toBeLessThanOrEqual(600);
  });

  it('the verification-resend rule (3 per 600 s) holds the same way', async () => {
    const resend = (ip: string) => post('/send-verification-email', ip, { email: 'ada@example.test' });
    for (let i = 0; i < 3; i++) await resend('198.51.100.9');
    await signIn('198.51.100.10');
    vi.setSystemTime(T0 + 120_000);
    await signIn('198.51.100.10');
    await settle();
    vi.setSystemTime(T0 + 300_000);
    expect((await resend('198.51.100.9')).status).toBe(429);
  });

  it('still prunes rows once they are older than the longest rule window', async () => {
    await resetRequest('198.51.100.1');
    await signIn('198.51.100.2');
    vi.setSystemTime(T0 + 601_000);
    await signIn('198.51.100.2');
    await settle();
    const keys = tables.rateLimit?.map((r) => String(r.key)) ?? [];
    expect(keys.some((k) => k.includes('198.51.100.1'))).toBe(false);
    expect(keys.some((k) => k.includes('198.51.100.2'))).toBe(true);
  });

  it('allows the request again once the 600 s window has passed', async () => {
    for (let i = 0; i < 3; i++) await resetRequest('198.51.100.1');
    vi.setSystemTime(T0 + 601_000);
    expect((await resetRequest('198.51.100.1')).status).not.toBe(429);
  });
});
