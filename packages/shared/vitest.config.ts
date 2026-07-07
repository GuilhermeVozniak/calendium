import { defineConfig } from 'vitest/config';

// packages/shared is pure TypeScript (no DOM) — use the node environment.
// Reused as the template for later web/desktop/mobile Vitest configs.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
});
