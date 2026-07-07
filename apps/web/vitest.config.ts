import path from 'node:path';

import { defineConfig } from 'vitest/config';

// apps/web renders DOM components (hooks, event dispatch, future React
// components) — use jsdom. Pure-node logic lives in packages/shared (see its
// vitest.config.ts), which stays on the node environment.
export default defineConfig({
  esbuild: {
    // tsconfig.json sets "jsx": "preserve" for Next's own compiler; tell
    // esbuild (Vitest's default transformer) to use the React 19 automatic
    // runtime directly so .tsx test/component files transform without
    // pulling in @vitejs/plugin-react.
    jsx: 'automatic',
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    include: ['**/*.test.{ts,tsx}'],
    exclude: ['**/node_modules/**', '**/.next/**'],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, '.'),
    },
  },
});
