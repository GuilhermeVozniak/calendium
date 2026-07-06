import { toNextJsHandler } from 'better-auth/next-js';

import { auth, isAllowedOrigin } from '@/lib/auth';

/** Better Auth catch-all: hosts sign-in, OAuth callbacks, JWKS, token mint. */
const handlers = toNextJsHandler(auth);

/**
 * Cross-origin auth support for native clients. The Wails desktop WebView (and
 * the localhost dev servers) call these endpoints from a DIFFERENT origin, so
 * the browser preflights and needs CORS headers back — Better Auth itself emits
 * none. We reflect only allowlisted origins (credentialed CORS forbids `*`) and
 * expose `set-auth-token` so the bearer clients can read their session token.
 */
function corsHeaders(req: Request): Headers {
  const headers = new Headers();
  headers.set('Vary', 'Origin');
  const origin = req.headers.get('origin');
  if (isAllowedOrigin(origin)) {
    headers.set('Access-Control-Allow-Origin', origin as string);
    headers.set('Access-Control-Allow-Credentials', 'true');
    headers.set('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
    headers.set('Access-Control-Allow-Headers', 'Content-Type, Authorization');
    headers.set('Access-Control-Expose-Headers', 'set-auth-token');
  }
  return headers;
}

function withCors(res: Response, req: Request): Response {
  corsHeaders(req).forEach((value, key) => res.headers.set(key, value));
  return res;
}

export async function GET(req: Request): Promise<Response> {
  return withCors(await handlers.GET(req), req);
}

export async function POST(req: Request): Promise<Response> {
  return withCors(await handlers.POST(req), req);
}

export function OPTIONS(req: Request): Response {
  return new Response(null, { status: 204, headers: corsHeaders(req) });
}
