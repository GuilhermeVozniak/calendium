import { describe, expect, it } from 'vitest';

// lib/auth.ts constructs the Better Auth server at import. `db()` builds a
// pg.Pool lazily (no connection) and readMailConfig sees no SMTP_* in the
// Vitest environment, so the module loads under jsdom without Postgres or
// SMTP. The origin/IP/policy helpers live in lib/auth-env.ts and are covered
// by auth-env.test.ts; this file pins what lib/auth.ts hands to betterAuth().
import { auth } from '@/lib/auth';

interface Options {
  trustedOrigins?: string[];
  rateLimit?: {
    enabled?: boolean;
    storage?: string;
    window?: number;
    max?: number;
    customRules?: Record<string, { window: number; max: number }>;
    customStorage?: { consume?: unknown };
  };
  emailAndPassword?: {
    requireEmailVerification?: boolean;
    minPasswordLength?: number;
    maxPasswordLength?: number;
    resetPasswordTokenExpiresIn?: number;
    revokeSessionsOnPasswordReset?: boolean;
    sendResetPassword?: unknown;
  };
  emailVerification?: unknown;
  advanced?: { ipAddress?: { ipAddressHeaders?: string[] } };
  hooks?: { before?: unknown };
}

const options = auth.options as unknown as Options;

describe('auth options', () => {
  it('trusts the fixed origins (native scheme, Apple form_post, four Wails origins)', () => {
    for (const o of ['calendium://', 'https://appleid.apple.com', 'wails://wails', 'wails://wails.localhost', 'http://wails.localhost', 'https://wails.localhost']) {
      expect(options.trustedOrigins).toContain(o);
    }
  });

  it('enables database-backed rate limiting with the per-route rules', () => {
    expect(options.rateLimit).toMatchObject({ enabled: true, storage: 'database', window: 60, max: 100 });
    // lib/rate-limit-storage.ts: int8-safe reads and 600 s row retention.
    expect(typeof options.rateLimit?.customStorage?.consume).toBe('function');
    expect(options.rateLimit?.customRules).toEqual({
      '/sign-in/email': { window: 60, max: 5 },
      '/sign-up/email': { window: 60, max: 3 },
      '/request-password-reset': { window: 600, max: 3 },
      '/forget-password': { window: 600, max: 3 },
      '/send-verification-email': { window: 600, max: 3 },
      '/token': { window: 60, max: 60 },
    });
  });

  it('reads the client IP only from the server-set header', () => {
    expect(options.advanced?.ipAddress?.ipAddressHeaders).toEqual(['x-calendium-client-ip']);
  });

  it('applies the password bounds and reset semantics; verification follows SMTP (unset in this run)', () => {
    expect(options.emailAndPassword).toMatchObject({
      minPasswordLength: 10,
      maxPasswordLength: 128,
      resetPasswordTokenExpiresIn: 3600,
      revokeSessionsOnPasswordReset: true,
      requireEmailVerification: false,
    });
    expect(options.emailAndPassword?.sendResetPassword).toBeUndefined();
    expect(options.emailVerification).toBeUndefined();
  });

  it('installs a before hook (the password policy)', () => {
    expect(typeof options.hooks?.before).toBe('function');
  });
});
