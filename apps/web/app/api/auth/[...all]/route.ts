import { toNextJsHandler } from 'better-auth/next-js';

import { auth } from '@/lib/auth';

/** Better Auth catch-all: hosts sign-in, OAuth callbacks, JWKS, token mint. */
export const { GET, POST } = toNextJsHandler(auth);
