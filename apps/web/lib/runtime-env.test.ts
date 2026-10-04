import { describe, expect, it } from 'vitest';

import { assertRuntimeEnv, checkRuntimeEnv } from '@/lib/runtime-env';

const OK = { BETTER_AUTH_SECRET: 'x'.repeat(32), INTERNAL_API_SECRET: 'ab'.repeat(32) };

describe('checkRuntimeEnv', () => {
  it('passes with both secrets', () => {
    expect(checkRuntimeEnv(OK)).toEqual([]);
  });
  it('accepts uppercase hex', () => {
    expect(checkRuntimeEnv({ ...OK, INTERNAL_API_SECRET: 'AB'.repeat(32) })).toEqual([]);
  });
  it('reports a missing INTERNAL_API_SECRET', () => {
    const problems = checkRuntimeEnv({ BETTER_AUTH_SECRET: 'x' });
    expect(problems.map((p) => p.name)).toEqual(['INTERNAL_API_SECRET']);
  });
  it('reports a malformed INTERNAL_API_SECRET (not 64 hex chars)', () => {
    const problems = checkRuntimeEnv({ ...OK, INTERNAL_API_SECRET: 'not-hex' });
    expect(problems[0]?.reason).toMatch(/64 hex/);
    const short = checkRuntimeEnv({ ...OK, INTERNAL_API_SECRET: 'ab'.repeat(31) });
    expect(short[0]?.reason).toMatch(/64 hex/);
  });
  it('reports a missing BETTER_AUTH_SECRET too', () => {
    const problems = checkRuntimeEnv({ INTERNAL_API_SECRET: 'ab'.repeat(32) });
    expect(problems.map((p) => p.name)).toEqual(['BETTER_AUTH_SECRET']);
  });
});

describe('assertRuntimeEnv', () => {
  const prod = { NODE_ENV: 'production' };

  it('refuses a production server without INTERNAL_API_SECRET, naming it', () => {
    expect(() => assertRuntimeEnv({ ...prod, BETTER_AUTH_SECRET: 'x'.repeat(32) })).toThrow(
      /Refusing to start: INTERNAL_API_SECRET is required/
    );
  });

  it('passes a fully configured production server', () => {
    expect(() => assertRuntimeEnv({ ...prod, ...OK })).not.toThrow();
  });

  it('exempts demo mode (the e2e suite), development, tests and the build phase', () => {
    expect(() => assertRuntimeEnv({ ...prod, NEXT_PUBLIC_DEMO_MODE: 'true' })).not.toThrow();
    expect(() => assertRuntimeEnv({ NODE_ENV: 'development' })).not.toThrow();
    expect(() => assertRuntimeEnv({ NODE_ENV: 'test' })).not.toThrow();
    expect(() =>
      assertRuntimeEnv({ ...prod, NEXT_PHASE: 'phase-production-build' })
    ).not.toThrow();
  });
});
