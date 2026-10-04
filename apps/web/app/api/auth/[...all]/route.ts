import { toNextJsHandler } from 'better-auth/next-js';

import { auth } from '@/lib/auth';
import { isAllowedOrigin } from '@/lib/auth-env';
import { type ProxyTrust, proxyTrustFromEnv, withClientIp } from '@/lib/client-ip';

/** Better Auth catch-all: hosts sign-in, OAuth callbacks, JWKS, token mint. */
const handlers = toNextJsHandler(auth);

/** TRUST_PROXY / TRUSTED_PROXY_CIDRS, parsed once (instrumentation.ts already validated them at boot). */
let proxyTrust: ProxyTrust | undefined;
const trustProxy = (): ProxyTrust => {
  proxyTrust ??= proxyTrustFromEnv(process.env);
  return proxyTrust;
};

/**
 * Cross-origin auth support for native clients. The Wails desktop WebView
 * (and, with ALLOW_DEV_ORIGINS, the localhost dev servers) call these
 * endpoints from a DIFFERENT origin, so the browser preflights and needs
 * CORS headers back — Better Auth itself emits none. We reflect only
 * allowlisted origins (credentialed CORS forbids `*`) and expose
 * `set-auth-token` so the bearer clients can read their session token.
 */
function corsHeaders(req: Request): Headers {
  const headers = new Headers();
  headers.set('Vary', 'Origin');
  const origin = req.headers.get('origin');
  if (isAllowedOrigin(origin, process.env)) {
    headers.set('Access-Control-Allow-Origin', origin as string);
    headers.set('Access-Control-Allow-Credentials', 'true');
    headers.set('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
    headers.set('Access-Control-Allow-Headers', 'Content-Type, Authorization');
    headers.set('Access-Control-Expose-Headers', 'set-auth-token, x-retry-after');
  }
  return headers;
}

function withCors(res: Response, req: Request): Response {
  corsHeaders(req).forEach((value, key) => {
    res.headers.set(key, value);
  });
  return res;
}

// Every request is re-wrapped with the server-resolved client IP before Better
// Auth sees it (rate-limit buckets are keyed on it); see lib/client-ip.ts.
export async function GET(req: Request): Promise<Response> {
  return withCors(await handlers.GET(withClientIp(req, trustProxy())), req);
}

export async function POST(req: Request): Promise<Response> {
  return withCors(await handlers.POST(withClientIp(req, trustProxy())), req);
}

export function OPTIONS(req: Request): Response {
  return new Response(null, { status: 204, headers: corsHeaders(req) });
}
