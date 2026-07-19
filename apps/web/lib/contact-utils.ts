/**
 * Contact-pane presentation helpers: gravatar/favicon avatar chain and a
 * best-effort "company" label derived from a sender's domain. None of these
 * invent data — they're pure derivations from the email address the backend
 * already returned on the contact summary.
 */

const FREEMAIL_DOMAINS = new Set([
  'gmail.com',
  'outlook.com',
  'yahoo.com',
  'icloud.com',
  'hotmail.com',
  'proton.me',
  'protonmail.com',
]);

// Common two-label public suffixes where the registrable "company" label sits
// one level further up (acme.co.uk -> "acme", not "co"). Not exhaustive —
// covers the common cases; anything else falls back to a single-label TLD.
const TWO_LABEL_SUFFIXES = new Set([
  'co.uk',
  'org.uk',
  'net.au',
  'com.au',
  'co.nz',
  'co.jp',
  'co.in',
  'co.za',
  'com.br',
  'com.mx',
  'co.id',
  'com.sg',
]);

async function sha256Hex(input: string): Promise<string> {
  const bytes = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Gravatar avatar URL for an email, per Gravatar's SHA-256 identifier scheme.
 * `?d=404` makes Gravatar 404 instead of serving a placeholder when the
 * address has no avatar, so callers can fall through to the next step in the
 * chain (favicon, then initials) on image load error.
 */
export async function gravatarUrl(email: string): Promise<string> {
  const hash = await sha256Hex(email.trim().toLowerCase());
  return `https://www.gravatar.com/avatar/${hash}?d=404`;
}

/** DuckDuckGo's icon proxy — a reasonable favicon fallback with no API key. */
export function faviconUrl(domain: string): string {
  return `https://icons.duckduckgo.com/ip3/${domain}.ico`;
}

/**
 * Best-effort company name from a sender's domain: the capitalized
 * second-level label, or '' for known freemail domains (no company to show).
 */
export function companyFromDomain(domain: string): string {
  const normalized = domain.trim().toLowerCase();
  if (FREEMAIL_DOMAINS.has(normalized)) return '';
  const parts = normalized.split('.').filter(Boolean);
  if (parts.length < 2) return '';
  const suffixLen = TWO_LABEL_SUFFIXES.has(parts.slice(-2).join('.')) ? 2 : 1;
  const labelParts = parts.slice(0, parts.length - suffixLen);
  const label = labelParts[labelParts.length - 1];
  if (!label) return '';
  return label.charAt(0).toUpperCase() + label.slice(1);
}
