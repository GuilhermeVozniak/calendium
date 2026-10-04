import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

import '@testing-library/jest-dom/vitest';

// lib/auth.ts asserts BETTER_AUTH_SECRET at module load; give the unit
// suites a syntactically valid (never used) value.
process.env.BETTER_AUTH_SECRET ??= 'vitest-better-auth-secret-0123456789abcdef';

// Unmount every renderHook()/render() tree between tests so effects (event
// listeners, timers) from one test never leak into the next. @testing-library
// only auto-registers this when `afterEach` is a true global, which our
// config (imports, not `test.globals: true`) does not provide.
afterEach(cleanup);

// ---------------------------------------------------------------------------
// jsdom polyfills for Radix UI primitives (Dialog/Popover/Select/DropdownMenu)
// used throughout components/app and components/ui. jsdom does not implement
// these browser APIs; Radix's popper/pointer-capture logic calls them
// unconditionally, so component tests that render these primitives fail
// without these no-op stubs. Safe globally: they only add missing methods.
// ---------------------------------------------------------------------------
if (typeof globalThis.ResizeObserver === 'undefined') {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  globalThis.ResizeObserver = ResizeObserverStub;
}

if (typeof Element !== 'undefined') {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false;
  }
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = () => {};
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = () => {};
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  if (!Element.prototype.scrollTo) {
    Element.prototype.scrollTo = () => {};
  }
}

if (typeof window !== 'undefined' && typeof window.matchMedia === 'undefined') {
  // Minimal default so components that call matchMedia at module/mount time
  // (e.g. theme detection) don't crash in tests that don't care about it.
  // Tests that assert on system-theme behavior override this per-test.
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
