import { fileURLToPath, URL } from 'node:url';

import { defineConfig } from 'vitest/config';

// apps/desktop/frontend is a Vite + React SPA (the Wails WebView). The lib
// modules under test touch DOM-backed globals (localStorage, window,
// CustomEvent) and, for auth.ts's useSession hook, React itself — so this
// uses jsdom, mirroring apps/web/vitest.config.ts.
export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
