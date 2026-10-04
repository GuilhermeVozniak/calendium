/**
 * Content Security Policy for the web app (platform-hardening spec,
 * decision 3). One builder serves both the per-request nonce policy
 * (middleware.ts) and the static /offline policy (next.config.ts headers):
 * the only difference is script-src 'nonce-<n>' versus 'unsafe-inline'.
 *
 * Third parties, all for the /checkout Paddle overlay or social sign-in.
 * Paddle publishes no CSP list, so the Paddle entries are what Paddle.js v2
 * (cdn.paddle.com/paddle/v2/paddle.js, v2.10.0) actually loads:
 *   - script: paddle.js itself; in live mode Initialize() also injects the
 *     Retain/ProfitWell snippet from public.profitwell.com.
 *   - style: a paddle.css <link> from cdn. / sandbox-cdn.paddle.com plus
 *     injected <style> and style attributes ('unsafe-inline').
 *   - frame: the overlay on buy. / sandbox-buy.paddle.com (Retain widgets
 *     on *-retain-widgets.paddle.com).
 *   - connect: fetch() to api. / sandbox-api.paddle.com; ProfitWell posts to
 *     www2. / retain-api.profitwell.com and api.profitwell-events.com.
 *   - img: cdn. / sandbox-cdn.paddle.com (covered by https:).
 * blob: in frame-src / img-src is the attachment preview (attachments-pane:
 * a PDF iframe and an <img> over URL.createObjectURL) — 'self' never matches
 * blob: URLs.
 */
export type CspOptions = {
  /** Per-request nonce; omit for the static /offline policy. */
  nonce?: string;
  /** API origin for connect-src; '' means same origin (env.apiUrl). */
  apiUrl: string;
  /** Dev adds 'unsafe-eval' (React refresh) and ws: (HMR). */
  dev: boolean;
};

export function cspPolicy({ nonce, apiUrl, dev }: CspOptions): string {
  const scriptSrc = [
    "'self'",
    nonce ? `'nonce-${nonce}'` : "'unsafe-inline'",
    'https://cdn.paddle.com',
    'https://public.profitwell.com',
  ];
  if (dev) scriptSrc.push("'unsafe-eval'");
  const connectSrc = ["'self'"];
  if (apiUrl) connectSrc.push(apiUrl);
  connectSrc.push('https://*.paddle.com', 'https://*.profitwell.com', 'https://api.profitwell-events.com');
  if (dev) connectSrc.push('ws:');
  return [
    "default-src 'self'",
    `script-src ${scriptSrc.join(' ')}`,
    'frame-src blob: https://*.paddle.com https://accounts.google.com https://appleid.apple.com',
    `connect-src ${connectSrc.join(' ')}`,
    "img-src 'self' data: blob: https:",
    "style-src 'self' 'unsafe-inline' https://cdn.paddle.com https://sandbox-cdn.paddle.com",
    "font-src 'self' data:",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self' https://appleid.apple.com",
  ].join('; ');
}

/** 16 random bytes, base64 — valid in both the edge and node runtimes. */
export function makeNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
