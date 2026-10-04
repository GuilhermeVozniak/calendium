import { afterEach, describe, expect, it, vi } from 'vitest';

// links.ts evaluates SUPPORT_EMAIL at import time (Next inlines the
// NEXT_PUBLIC_* read at build), so each case re-imports a fresh module.
describe('marketing links', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('points at the real public repository', async () => {
    const { GITHUB_URL } = await import('@/components/marketing/links');
    expect(GITHUB_URL).toBe('https://github.com/GuilhermeVozniak/calendium');
  });

  it('SUPPORT_EMAIL defaults to support@calendium.app', async () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', undefined);
    vi.resetModules();
    const { SUPPORT_EMAIL } = await import('@/components/marketing/links');
    expect(SUPPORT_EMAIL).toBe('support@calendium.app');
  });

  it('SUPPORT_EMAIL follows NEXT_PUBLIC_SUPPORT_EMAIL', async () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', 'help@example.org');
    vi.resetModules();
    const { SUPPORT_EMAIL } = await import('@/components/marketing/links');
    expect(SUPPORT_EMAIL).toBe('help@example.org');
  });
});
