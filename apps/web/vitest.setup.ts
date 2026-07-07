import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

import '@testing-library/jest-dom/vitest';

// Unmount every renderHook()/render() tree between tests so effects (event
// listeners, timers) from one test never leak into the next. @testing-library
// only auto-registers this when `afterEach` is a true global, which our
// config (imports, not `test.globals: true`) does not provide.
afterEach(cleanup);
