/**
 * Typed access to the NEXT_PUBLIC_* environment (see .env.example).
 * Getters keep the static `process.env.NEXT_PUBLIC_*` references Next.js needs
 * for build-time inlining while failing loudly at first use when unset.
 *
 * Auth is Better Auth, hosted same-origin at `/api/auth/*`, so no public auth
 * var is required here — the browser client (lib/auth-client.ts) infers origin.
 */

export const env = {
  get apiUrl(): string {
    // Self-hosted deployments serve the web app and the API behind a single
    // origin (via Caddy), so an unset NEXT_PUBLIC_API_URL means "same origin".
    // Return an empty base so the ApiClient builds same-origin relative paths
    // (`/v1/…`); a literal '/' would produce protocol-relative `//v1/…`.
    const raw = process.env.NEXT_PUBLIC_API_URL;
    if (!raw || raw === '/') return '';
    return raw.replace(/\/+$/, '');
  },
  get supportEmail(): string {
    // Contact address on the footer, pricing, privacy and terms pages.
    // Self-hosted / white-label deployments set NEXT_PUBLIC_SUPPORT_EMAIL at
    // build time; blank falls back to the Calendium Cloud mailbox so the
    // mailto: links never render empty.
    const raw = process.env.NEXT_PUBLIC_SUPPORT_EMAIL?.trim();
    return raw ? raw : 'support@calendium.app';
  },
};
