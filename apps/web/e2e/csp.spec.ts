import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

/**
 * Platform hardening: every app page carries the nonce CSP (Report-Only
 * until CSP_REPORT_ONLY=false) and the theme-init inline script is nonced
 * with the same value, so enforcing the policy cannot break first paint.
 * Browsers hide the nonce content attribute; read the IDL property.
 */

type Violation = { directive: string; blocked: string; source: string };

/** Records every securitypolicyviolation (fired in Report-Only mode too). */
async function recordViolations(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const w = window as unknown as { __cspViolations: Violation[] };
    w.__cspViolations = [];
    document.addEventListener('securitypolicyviolation', (e) => {
      w.__cspViolations.push({ directive: e.effectiveDirective, blocked: e.blockedURI, source: e.sourceFile });
    });
  });
}

async function violations(page: Page): Promise<Violation[]> {
  return page.evaluate(() => (window as unknown as { __cspViolations: Violation[] }).__cspViolations ?? []);
}

test.describe('Content Security Policy', () => {
  test('page response has the CSP header and the theme script carries its nonce', async ({ page }) => {
    const response = await page.goto('/mail');
    expect(response).not.toBeNull();
    const headers = response?.headers() ?? {};
    const csp = headers['content-security-policy-report-only'] ?? headers['content-security-policy'];
    expect(csp, 'CSP header present').toBeTruthy();
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain('https://cdn.paddle.com');
    expect(csp).toContain("frame-ancestors 'none'");
    const nonce = /'nonce-([^']+)'/.exec(csp ?? '')?.[1];
    expect(nonce).toBeTruthy();
    const scriptNonce = await page.evaluate(
      () => (document.getElementById('theme-init') as HTMLScriptElement | null)?.nonce ?? null
    );
    expect(scriptNonce).toBe(nonce);
    expect(headers['x-frame-options']).toBe('DENY');
    expect(headers['x-content-type-options']).toBe('nosniff');
  });

  test('every inline script Next renders carries the request nonce', async ({ page }) => {
    const response = await page.goto('/mail');
    const headers = response?.headers() ?? {};
    const csp = headers['content-security-policy-report-only'] ?? headers['content-security-policy'];
    const nonce = /'nonce-([^']+)'/.exec(csp ?? '')?.[1];
    const unnonced = await page.evaluate(
      (expected) =>
        // The dev overlay's <script data-nextjs-dev-overlay> is a portal host, not code (next dev only).
        Array.from(document.querySelectorAll('script:not([src]):not([data-nextjs-dev-overlay])'))
          .filter((s) => (s as HTMLScriptElement).nonce !== expected)
          .map((s) => (s.textContent ?? '').slice(0, 80)),
      nonce
    );
    expect(unnonced).toEqual([]);
  });

  for (const path of ['/', '/pricing', '/signin', '/mail', '/calendar', '/settings', '/checkout']) {
    test(`no CSP violations on ${path}`, async ({ page }) => {
      await recordViolations(page);
      await page.goto(path);
      await page.waitForLoadState('networkidle');
      expect(await violations(page)).toEqual([]);
    });
  }

  test('/offline keeps a static CSP without a nonce', async ({ page }) => {
    const response = await page.goto('/offline');
    const csp = response?.headers()['content-security-policy'];
    expect(csp).toContain("script-src 'self' 'unsafe-inline'");
    expect(csp).not.toContain('nonce-');
  });
});
