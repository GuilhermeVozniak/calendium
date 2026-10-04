import { BlockList, isIP } from 'node:net';

import { CLIENT_IP_HEADER, type EnvLike, envBool } from '@/lib/auth-env';

/**
 * Client IP for Better Auth's per-IP rate limits — the same rule as the Go
 * API (platform-hardening plan, httpapi proxyTrust.clientIP):
 *
 * - The immediate peer is the RIGHT-MOST X-Forwarded-For entry. Next.js only
 *   fills the header from the socket when it is absent, so the production
 *   server runs with scripts/forwarded-for-peer.cjs preloaded, which APPENDS
 *   the socket peer: a client can prepend whatever it likes, never replace
 *   the right-most entry.
 * - TRUST_PROXY false (false/0/no/blank, parsed by envBool like the Go
 *   config): the client is that peer. Nothing a client sends is ever used.
 * - TRUST_PROXY true (true/1/yes) and the peer is inside TRUSTED_PROXY_CIDRS
 *   (default: loopback + private ranges): walk X-Forwarded-For right to
 *   left, skipping hops that do not parse or are trusted proxies; the first
 *   other hop is the client. No such hop (every hop is trusted: a LAN or VPN
 *   client behind the proxy) → the left-most parseable entry, i.e. the
 *   client as the all-trusted chain reported it; the peer only when it is
 *   the sole parseable entry. A forged left entry cannot win while any
 *   untrusted hop sits to its right.
 * - No parseable peer → '' (Better Auth's shared `no-trusted-ip` bucket).
 *
 * Server-only (node:net); lib/auth-env.ts stays importable by client pages.
 */

export const DEFAULT_TRUSTED_PROXY_CIDRS = ['127.0.0.0/8', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '::1/128', 'fc00::/7'] as const;

export interface ProxyTrust {
  enabled: boolean;
  proxies: BlockList;
}

/** The eight 16-bit groups of a valid (isIP === 6), zone-free, lower-cased IPv6 address. */
function ipv6Groups(ip: string): number[] {
  const toGroups = (part: string): number[] =>
    part === ''
      ? []
      : part.split(':').flatMap((g) => {
          if (!g.includes('.')) return [Number.parseInt(g, 16)];
          const [a = 0, b = 0, c = 0, d = 0] = g.split('.').map(Number);
          return [(a << 8) | b, (c << 8) | d];
        });
  const [head = '', tail] = ip.split('::');
  if (tail === undefined) return toGroups(head);
  const left = toGroups(head);
  const right = toGroups(tail);
  return [...left, ...Array<number>(8 - left.length - right.length).fill(0), ...right];
}

/** The IPv4 address an IPv4-mapped IPv6 address (::ffff:a.b.c.d in any textual form, e.g. ::ffff:a00:1) carries, else null. */
function unmapIPv4(ip: string): string | null {
  const g = ipv6Groups(ip);
  if (g.length !== 8 || g.slice(0, 5).some((x) => x !== 0) || g[5] !== 0xffff) return null;
  const [hi = 0, lo = 0] = g.slice(6);
  return [hi >> 8, hi & 0xff, lo >> 8, lo & 0xff].join('.');
}

/**
 * A canonical address (IPv4-mapped IPv6 unmapped in any textual form, like
 * Go's netip Addr.Unmap; zone dropped; lower-cased), or null when `raw` is
 * not a bare IP.
 */
export function canonicalIp(raw: string): string | null {
  let ip = raw.trim().toLowerCase();
  const zone = ip.indexOf('%');
  if (zone !== -1) ip = ip.slice(0, zone);
  const version = isIP(ip);
  if (version === 0) return null;
  return (version === 6 && unmapIPv4(ip)) || ip;
}

/** Whether scripts/forwarded-for-peer.cjs is active in this process (the right-most XFF entry is then the real peer). */
export function peerAppendedToForwardedFor(): boolean {
  return (globalThis as Record<symbol, unknown>)[Symbol.for('calendium.forwardedForPeer')] === true;
}

function family(ip: string): 'ipv4' | 'ipv6' {
  return isIP(ip) === 4 ? 'ipv4' : 'ipv6';
}

/**
 * One TRUSTED_PROXY_CIDRS entry → [address, prefix, family], with the Go
 * tier's grammar (netip.ParsePrefix, plus a bare IP = single host): no zone,
 * a decimal prefix without sign or leading zeros, at most 32 (IPv4) / 128
 * (IPv6). null when the entry is invalid.
 */
function parseTrustedCidr(entry: string): [string, number, 'ipv4' | 'ipv6'] | null {
  const [address = '', prefixText, ...rest] = entry.split('/');
  if (rest.length > 0 || address.includes('%')) return null;
  const version = isIP(address);
  if (version === 0) return null;
  const max = version === 4 ? 32 : 128;
  if (prefixText === undefined) {
    // A bare IPv4-mapped address is that IPv4 host, like the client addresses it is matched against.
    const unmapped = version === 6 ? unmapIPv4(address.toLowerCase()) : null;
    return unmapped ? [unmapped, 32, 'ipv4'] : [address, max, family(address)];
  }
  if (!/^(?:0|[1-9]\d{0,2})$/.test(prefixText)) return null;
  const prefix = Number(prefixText);
  return prefix <= max ? [address, prefix, family(address)] : null;
}

/**
 * TRUST_PROXY / TRUSTED_PROXY_CIDRS → ProxyTrust. Throws on an invalid
 * TRUST_PROXY or CIDR (instrumentation.ts calls this at boot, so a typo stops the server like
 * the Go API's config.FromEnv does). TRUSTED_PROXY_CIDRS is validated even
 * while TRUST_PROXY is off, as the Go config does: comma-separated, each
 * entry trimmed, empty entries ignored (no entries at all = the defaults), a
 * bare IP is /32 or /128, anything else invalid is a boot error.
 */
export function proxyTrustFromEnv(env: EnvLike): ProxyTrust {
  const proxies = new BlockList();
  const enabled = envBool(env, 'TRUST_PROXY');
  const configured = (env.TRUSTED_PROXY_CIDRS ?? '')
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean);
  for (const cidr of configured.length > 0 ? configured : DEFAULT_TRUSTED_PROXY_CIDRS) {
    const parsed = parseTrustedCidr(cidr);
    if (!parsed) throw new Error(`TRUSTED_PROXY_CIDRS: "${cidr}" is not a valid IP or CIDR`);
    proxies.addSubnet(...parsed);
  }
  return { enabled, proxies };
}

function isTrusted(trust: ProxyTrust, ip: string): boolean {
  return trust.proxies.check(ip, family(ip));
}

/** The rate-limit client IP for a request's X-Forwarded-For (see the module comment). */
export function clientIpFor(headers: Headers, trust: ProxyTrust): string {
  const hops = (headers.get('x-forwarded-for') ?? '')
    .split(',')
    .map((h) => h.trim())
    .filter(Boolean);
  const peer = canonicalIp(hops.pop() ?? '');
  if (!peer) return '';
  if (!trust.enabled || !isTrusted(trust, peer)) return peer;
  // Right to left: the first untrusted hop is the client. Every hop passed on
  // the way is a trusted proxy, so when none is untrusted the left-most valid
  // entry is the client as the trusted chain reported it (a LAN/VPN client
  // behind Caddy) — never the proxy's own address shared by everyone.
  let leftmost = peer;
  for (let i = hops.length - 1; i >= 0; i--) {
    const hop = canonicalIp(hops[i] ?? '');
    if (!hop) continue;
    if (!isTrusted(trust, hop)) return hop;
    leftmost = hop;
  }
  return leftmost;
}

/** A copy of `req` whose CLIENT_IP_HEADER is always the server-resolved value (forged values are overwritten). */
export function withClientIp(req: Request, trust: ProxyTrust): Request {
  const headers = new Headers(req.headers);
  headers.set(CLIENT_IP_HEADER, clientIpFor(req.headers, trust));
  // `duplex` is required by undici when re-wrapping a request that carries a
  // body stream; it is absent from lib.dom's RequestInit, hence the cast.
  return new Request(req, { headers, duplex: 'half' } as RequestInit);
}
