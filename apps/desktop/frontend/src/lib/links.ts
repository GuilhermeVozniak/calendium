/**
 * Small URL guards for links that cross the WebView boundary: update Download
 * URLs headed for the system browser, and calendium:// deep links coming in
 * from the OS.
 */

/**
 * True only for an https URL with a host. The update banner's Download URL
 * goes to OpenExternal, so anything else (http, javascript:, a custom protocol
 * handler, a relative path) is refused. The Go host enforces the same rule.
 */
export function isSafeDownloadUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    return parsed.protocol === 'https:' && parsed.hostname !== '';
  } catch {
    return false;
  }
}

const DEEP_LINK_SCHEME = 'calendium://';

/**
 * Returns the link with its calendium scheme lower-cased (schemes are
 * case-insensitive; argv may carry CALENDIUM://) and the rest verbatim, or
 * null for any other scheme.
 */
export function normalizeDeepLink(url: string): string | null {
  if (url.slice(0, DEEP_LINK_SCHEME.length).toLowerCase() !== DEEP_LINK_SCHEME) return null;
  return DEEP_LINK_SCHEME + url.slice(DEEP_LINK_SCHEME.length);
}

/** The normalized link when it targets calendium://<route>, else null. */
export function deepLinkTo(url: string, route: string): string | null {
  const link = normalizeDeepLink(url);
  return link?.startsWith(DEEP_LINK_SCHEME + route) ? link : null;
}
