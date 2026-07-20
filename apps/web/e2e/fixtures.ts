import { test as base, expect, type Page } from '@playwright/test';

/**
 * Fakes an authenticated Better Auth session entirely at the browser network
 * layer, so the e2e suite never needs a real Postgres-backed Better Auth
 * server — matching demo mode's "no backend required" contract (see
 * playwright.config.ts for the full rationale).
 *
 * app/(app)/layout.tsx gates every authenticated route on
 * `authClient.useSession()`, which does a same-origin
 * `GET /api/auth/get-session` and redirects to /signin when it returns no
 * session. We intercept that request and hand back a canned session/user
 * before it ever reaches the Next.js server, so `lib/auth.ts` (which needs
 * BETTER_AUTH_SECRET/DATABASE_URL) is never imported for these tests.
 * `/api/auth/token` (used to mint a Bearer JWT for real Go-API calls) is also
 * stubbed to fail fast — DEMO_MODE's data hooks already tolerate that and
 * fall back to lib/*-mock.ts, so no real backend or token is ever needed.
 */
const FAKE_USER = {
  id: 'e2e-user-1',
  email: 'e2e@calendium.app',
  name: 'E2E Test User',
  emailVerified: true,
  image: null,
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};

const FAKE_SESSION = {
  id: 'e2e-session-1',
  userId: FAKE_USER.id,
  token: 'e2e-fake-session-token',
  expiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(),
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  ipAddress: '127.0.0.1',
  userAgent: 'playwright',
};

export const test = base.extend<{ page: Page }>({
  page: async ({ page }, use) => {
    // The concierge tour auto-starts for fresh users (M2.8 Task 19); mark it
    // done so its popover never overlays unrelated specs. Tour keys are now
    // per-user (calendium.tour.v1.<userId>), but seeding the legacy un-scoped
    // key still works: lib/tour-state.ts adopts it into the signed-in user's
    // key on first read. onboarding.spec.ts removes this key in its own init
    // script to exercise the first-run path.
    await page.addInitScript(() => {
      window.localStorage.setItem(
        'calendium.tour.v1',
        JSON.stringify({ done: true, coachMuted: false })
      );
    });
    await page.route('**/api/auth/get-session*', (route) =>
      route.fulfill({ json: { user: FAKE_USER, session: FAKE_SESSION } })
    );
    await page.route('**/api/auth/token*', (route) =>
      route.fulfill({ status: 401, json: { error: 'e2e: token minting is stubbed out' } })
    );
    await use(page);
  },
});

export { expect };

/**
 * Presses the app's "mod+k" command-palette shortcut (lib/shortcuts.ts maps
 * `mod` to ⌘ on macOS and Ctrl elsewhere, based on `navigator.platform`) —
 * mirror that check here so the shortcut fires regardless of which OS this
 * suite runs on.
 */
export async function openCommandPalette(page: Page): Promise<void> {
  const isMac = await page.evaluate(() => /Mac|iPhone|iPad|iPod/.test(navigator.platform));
  await page.keyboard.press(isMac ? 'Meta+k' : 'Control+k');
}
