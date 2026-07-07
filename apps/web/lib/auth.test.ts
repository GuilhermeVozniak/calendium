import { afterEach, describe, expect, it, vi } from 'vitest';

// lib/auth.ts also constructs the full Better Auth server (`export const
// auth = betterAuth({ database: db(), ... })`), but `db()` builds a `pg.Pool`
// lazily — the Pool constructor does not open a connection — so importing the
// module is safe under jsdom without a real Postgres instance or env vars.
// Only `isAllowedOrigin` is exported; `isLocalhostDevOrigin`, `envAllowedOrigins`,
// and `trustedOrigins` are private helpers, so their behavior is exercised
// indirectly below (and, for `trustedOrigins`, via the constructed `auth`
// instance's own config — see the last describe block).
import { auth, isAllowedOrigin } from '@/lib/auth';

describe('isAllowedOrigin', () => {
  it('rejects a missing origin', () => {
    expect(isAllowedOrigin(null)).toBe(false);
    expect(isAllowedOrigin(undefined)).toBe(false);
    expect(isAllowedOrigin('')).toBe(false);
  });

  it('allows every exact Wails WebView origin', () => {
    expect(isAllowedOrigin('wails://wails')).toBe(true);
    expect(isAllowedOrigin('wails://wails.localhost')).toBe(true);
    expect(isAllowedOrigin('http://wails.localhost')).toBe(true);
    expect(isAllowedOrigin('https://wails.localhost')).toBe(true);
  });

  it('allows any port on the wails.localhost hostname', () => {
    expect(isAllowedOrigin('http://wails.localhost:1420')).toBe(true);
    expect(isAllowedOrigin('https://wails.localhost:9999')).toBe(true);
  });

  it('allows localhost / 127.0.0.1 / ::1 dev origins on http or https', () => {
    expect(isAllowedOrigin('http://localhost:3000')).toBe(true);
    expect(isAllowedOrigin('https://localhost')).toBe(true);
    expect(isAllowedOrigin('http://127.0.0.1:8080')).toBe(true);
    expect(isAllowedOrigin('http://[::1]:3000')).toBe(true);
  });

  it('rejects localhost on a non-http(s) protocol', () => {
    expect(isAllowedOrigin('ftp://localhost')).toBe(false);
  });

  it('rejects an arbitrary external origin', () => {
    expect(isAllowedOrigin('https://evil.example.com')).toBe(false);
  });

  it('rejects an unparseable origin instead of throwing', () => {
    expect(isAllowedOrigin('not a url')).toBe(false);
  });

  describe('CORS_ALLOWED_ORIGINS env allowlist', () => {
    afterEach(() => {
      vi.unstubAllEnvs();
    });

    it('allows an origin listed in CORS_ALLOWED_ORIGINS (with whitespace trimmed)', () => {
      vi.stubEnv(
        'CORS_ALLOWED_ORIGINS',
        ' https://ops.example.com , https://partner.example.com ',
      );
      expect(isAllowedOrigin('https://ops.example.com')).toBe(true);
      expect(isAllowedOrigin('https://partner.example.com')).toBe(true);
      expect(isAllowedOrigin('https://unlisted.example.com')).toBe(false);
    });

    it('treats an unset/empty CORS_ALLOWED_ORIGINS as an empty allowlist', () => {
      vi.stubEnv('CORS_ALLOWED_ORIGINS', '');
      expect(isAllowedOrigin('https://ops.example.com')).toBe(false);
    });

    it('does not leak the stubbed env into a later test', () => {
      expect(isAllowedOrigin('https://ops.example.com')).toBe(false);
    });
  });
});

describe('trustedOrigins (via the constructed auth instance)', () => {
  it('includes the native deep link, Wails origins, and localhost wildcards by default', () => {
    // `trustedOrigins()` is a private, unexported helper; its return value is
    // passed straight through to betterAuth() with no transformation, so
    // reading it back off `auth.options` exercises the real function output
    // without requiring a source-code export change.
    const trusted = (auth.options as { trustedOrigins?: string[] }).trustedOrigins ?? [];
    expect(trusted).toContain('calendium://');
    expect(trusted).toContain('wails://wails');
    expect(trusted).toContain('wails://wails.localhost');
    expect(trusted).toContain('http://wails.localhost');
    expect(trusted).toContain('https://wails.localhost');
    expect(trusted).toContain('http://localhost:*');
    expect(trusted).toContain('http://127.0.0.1:*');
  });
});
