import { afterEach, describe, expect, it, vi } from 'vitest';

describe('DEMO_MODE', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('is true only when NEXT_PUBLIC_DEMO_MODE is exactly "true"', async () => {
    vi.stubEnv('NEXT_PUBLIC_DEMO_MODE', 'true');
    const { DEMO_MODE } = await import('@/lib/demo');
    expect(DEMO_MODE).toBe(true);
  });

  it('is false when unset', async () => {
    vi.stubEnv('NEXT_PUBLIC_DEMO_MODE', undefined);
    const { DEMO_MODE } = await import('@/lib/demo');
    expect(DEMO_MODE).toBe(false);
  });

  it('is false for any non-"true" value', async () => {
    vi.stubEnv('NEXT_PUBLIC_DEMO_MODE', '1');
    const { DEMO_MODE } = await import('@/lib/demo');
    expect(DEMO_MODE).toBe(false);
  });
});
