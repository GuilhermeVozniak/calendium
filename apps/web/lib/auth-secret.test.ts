// @vitest-environment node
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

import { assertBetterAuthSecret, MIN_BETTER_AUTH_SECRET_BYTES } from '@/lib/auth-secret';

const MESSAGE = 'BETTER_AUTH_SECRET must be at least 32 bytes; generate one with: openssl rand -base64 32';

describe('assertBetterAuthSecret', () => {
  it('requires 32 bytes', () => {
    expect(MIN_BETTER_AUTH_SECRET_BYTES).toBe(32);
  });
  it('throws on a 31-byte secret', () => {
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'a'.repeat(31) })).toThrow(MESSAGE);
  });
  it('throws when unset or empty', () => {
    expect(() => assertBetterAuthSecret({})).toThrow(MESSAGE);
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: '' })).toThrow(MESSAGE);
  });
  it('accepts 32 bytes and counts bytes, not characters', () => {
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'a'.repeat(32) })).not.toThrow();
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'é'.repeat(16) })).not.toThrow(); // 32 bytes
    expect(() => assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'é'.repeat(15) })).toThrow(MESSAGE); // 30 bytes
  });
  it("accepts Playwright's 45-char e2e secret", () => {
    expect(() =>
      assertBetterAuthSecret({ BETTER_AUTH_SECRET: 'e2e-playwright-not-a-real-secret-0123456789ab' })
    ).not.toThrow();
  });
  it('skips during the production build phase (no secret in the image build)', () => {
    expect(() => assertBetterAuthSecret({ NEXT_PHASE: 'phase-production-build' })).not.toThrow();
    expect(() =>
      assertBetterAuthSecret({ NEXT_PHASE: 'phase-production-build', NODE_ENV: 'production', BETTER_AUTH_SECRET: 'change-me' })
    ).not.toThrow();
  });
});

describe('assertBetterAuthSecret — placeholder secrets (production only)', () => {
  const PLACEHOLDER = 'BETTER_AUTH_SECRET is still a placeholder; generate one with: openssl rand -base64 32';
  const exampleSecret = (file: string) => {
    const line = readFileSync(join(__dirname, file), 'utf8')
      .split('\n')
      .find((l) => l.startsWith('BETTER_AUTH_SECRET='));
    if (line === undefined) throw new Error(`${file} has no BETTER_AUTH_SECRET line`);
    return line.slice('BETTER_AUTH_SECRET='.length).trim();
  };
  const prod = (secret: string) => () => assertBetterAuthSecret({ NODE_ENV: 'production', BETTER_AUTH_SECRET: secret });

  it('rejects the root .env.example value verbatim', () => {
    expect(prod(exampleSecret('../../../.env.example'))).toThrow(/BETTER_AUTH_SECRET/);
  });
  it('rejects the apps/web/.env.example placeholder verbatim', () => {
    const placeholder = exampleSecret('../.env.example');
    expect(Buffer.byteLength(placeholder)).toBeGreaterThanOrEqual(32); // long enough to pass the length check alone
    expect(prod(placeholder)).toThrow(PLACEHOLDER);
  });
  it.each([
    'change-me-change-me-change-me-change-me',
    'CHANGE-ME-please-0123456789abcdefghijklmnop',
    'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxChangeMexx',
    'my-changeme-secret-0123456789abcdefghijklmnop',
  ])('rejects a value containing change-me/changeme in any case: %s', (secret) => {
    expect(prod(secret)).toThrow(PLACEHOLDER);
  });
  it('accepts a generated secret in production', () => {
    expect(prod('vY3n0q8mJ2+u3kG1yT8xQ0rZ5bF7cW9dE4hL6pN1sA0=')).not.toThrow();
    expect(prod('e2e-playwright-not-a-real-secret-0123456789ab')).not.toThrow();
  });
  it('leaves development and test alone (the length check still applies)', () => {
    for (const NODE_ENV of ['development', 'test', undefined]) {
      expect(() =>
        assertBetterAuthSecret({ NODE_ENV, BETTER_AUTH_SECRET: 'change-me-change-me-change-me-change-me' })
      ).not.toThrow();
      expect(() => assertBetterAuthSecret({ NODE_ENV, BETTER_AUTH_SECRET: 'change-me' })).toThrow(MESSAGE);
    }
  });
});
