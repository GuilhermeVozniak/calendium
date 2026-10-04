import { type NextRequest, NextResponse } from 'next/server';

import { envBool } from '@/lib/auth-env';
import { cspPolicy, makeNonce } from '@/lib/csp';
import { env } from '@/lib/env';

/**
 * Per-request nonce CSP (platform-hardening spec, decision 3). Excluded:
 * the API routes (Better Auth, csp-report, health), Next static assets, the
 * service worker and manifest, icons, and /offline (static CSP from
 * next.config.ts). Next 15.5 reads the policy from the REQUEST header to
 * nonce its own inline scripts; the same value is copied to the response.
 */
export const config = {
  matcher: ['/((?!api/|_next/static|_next/image|sw\\.js|manifest\\.webmanifest|icon|offline).*)'],
};

const CSP_REPORT_ONLY_DEFAULT = true;

/**
 * CSP_REPORT_ONLY is read at runtime (not inlined) so the policy can be
 * rolled back to Report-Only without a rebuild. Same boolean grammar as the
 * rest of the config (true/1/yes, false/0/no); unset or blank means the
 * default. instrumentation.ts rejects an invalid value at boot, so the
 * catch here only keeps a request from ever throwing.
 */
export function cspReportOnly(): boolean {
  const raw = process.env.CSP_REPORT_ONLY;
  if (raw === undefined || raw.trim() === '') return CSP_REPORT_ONLY_DEFAULT;
  try {
    return envBool({ CSP_REPORT_ONLY: raw }, 'CSP_REPORT_ONLY');
  } catch {
    return CSP_REPORT_ONLY_DEFAULT;
  }
}

export function middleware(req: NextRequest): NextResponse {
  const nonce = makeNonce();
  const policy = `${cspPolicy({
    nonce,
    apiUrl: env.apiUrl,
    dev: process.env.NODE_ENV !== 'production',
  })}; report-uri /api/csp-report; report-to csp`;
  const headerName = cspReportOnly() ? 'Content-Security-Policy-Report-Only' : 'Content-Security-Policy';

  // Never let a client-supplied value reach the renderer: Next takes the
  // script nonce from these request headers.
  const requestHeaders = new Headers(req.headers);
  requestHeaders.delete('Content-Security-Policy');
  requestHeaders.delete('Content-Security-Policy-Report-Only');
  requestHeaders.set('x-nonce', nonce);
  requestHeaders.set(headerName, policy);

  const res = NextResponse.next({ request: { headers: requestHeaders } });
  res.headers.set(headerName, policy);
  res.headers.set('Reporting-Endpoints', 'csp="/api/csp-report"');
  return res;
}
