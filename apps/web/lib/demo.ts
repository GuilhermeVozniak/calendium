/**
 * Explicit demo mode (honesty policy). Mock/sample data is served ONLY when
 * this flag is on (NEXT_PUBLIC_DEMO_MODE=true). Outside demo mode every data
 * layer hits the real API and surfaces genuine loading / empty / error states —
 * it never fabricates success. See docs feature-map + the fixer contract.
 */
export const DEMO_MODE = process.env.NEXT_PUBLIC_DEMO_MODE === 'true';
