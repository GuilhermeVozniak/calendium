import { defineConfig, devices } from '@playwright/test';

/**
 * E2E suite for the web app, driven entirely in DEMO MODE
 * (NEXT_PUBLIC_DEMO_MODE=true — see lib/demo.ts): every data hook
 * (mail/calendar/settings) tries the real Calendium API first, then falls
 * back to the offline lib/*-mock.ts dataset when it's unreachable. No Go
 * backend is started for this suite.
 *
 * Better Auth still gates the authenticated app shell even in demo mode
 * (app/(app)/layout.tsx checks a real session), and Better Auth itself needs
 * Postgres — which this suite deliberately does NOT stand up. Instead
 * e2e/fixtures.ts stubs the two Better Auth endpoints the browser calls
 * (`/api/auth/get-session`, `/api/auth/token`) at the network layer, so
 * Better Auth's server route is never invoked and no database is required.
 * NEXT_PUBLIC_API_URL points at an unreachable local port so every API call
 * fails fast and deterministically (rather than depending on whether a real
 * backend happens to be running on the default port).
 */
const PORT = process.env.PLAYWRIGHT_WEB_PORT ?? '3100';
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: process.env.CI
      ? `node_modules/.bin/next build && node_modules/.bin/next start -p ${PORT}`
      : `node_modules/.bin/next dev -p ${PORT}`,
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 240_000,
    env: {
      NEXT_PUBLIC_DEMO_MODE: 'true',
      // Unreachable on purpose — forces every real API attempt to fail fast
      // and fall back to demo data, regardless of what else is running
      // locally (e.g. a developer's own `bun run dev:api` on :8080).
      NEXT_PUBLIC_API_URL: 'http://127.0.0.1:58080',
      BETTER_AUTH_SECRET: 'e2e-playwright-not-a-real-secret-0123456789ab',
      BETTER_AUTH_URL: BASE_URL,
      // Never actually queried: the auth route handler that would construct
      // a Postgres pool from this is never hit (see fixtures.ts).
      DATABASE_URL: 'postgres://calendium:calendium@127.0.0.1:5432/calendium_e2e_unused',
    },
  },
});
