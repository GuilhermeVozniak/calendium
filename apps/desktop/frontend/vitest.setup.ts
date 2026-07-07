import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// Unmount every renderHook() tree between tests (auth.test.ts's useSession
// hook) so effects — event listeners, in-flight refreshSession() calls —
// from one test never leak into the next. Mirrors apps/web/vitest.setup.ts.
afterEach(cleanup);
