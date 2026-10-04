// @vitest-environment node
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
  });
});
