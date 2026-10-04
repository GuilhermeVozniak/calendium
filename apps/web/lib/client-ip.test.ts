// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { CLIENT_IP_HEADER } from '@/lib/auth-env';
import { DEFAULT_TRUSTED_PROXY_CIDRS, canonicalIp, clientIpFor, proxyTrustFromEnv, withClientIp } from '@/lib/client-ip';

/**
 * X-Forwarded-For as the route handler sees it: whatever the client and any
 * proxies sent, with the Next server's own socket peer APPENDED as the
 * right-most entry (scripts/forwarded-for-peer.cjs).
 */
const xff = (value?: string) => new Headers(value === undefined ? {} : { 'x-forwarded-for': value });
const UNTRUSTED = proxyTrustFromEnv({});
const TRUSTED = proxyTrustFromEnv({ TRUST_PROXY: 'true' });

describe('clientIpFor — TRUST_PROXY unset/false: the immediate peer only', () => {
  it.each([
    ['a direct client', '203.0.113.9', '203.0.113.9'],
    ['a spoofed left entry is ignored', '6.6.6.6, 203.0.113.9', '203.0.113.9'],
    ['several spoofed entries are ignored', '6.6.6.6, 7.7.7.7,8.8.8.8 , 203.0.113.9', '203.0.113.9'],
    ['behind a proxy every client shares the proxy bucket', '198.51.100.7, 172.18.0.5', '172.18.0.5'],
    ['an IPv4-mapped peer is unmapped', '::ffff:203.0.113.9', '203.0.113.9'],
    ['an IPv6 peer', '6.6.6.6, 2001:DB8::1', '2001:db8::1'],
    ['a trailing comma and whitespace', ' 203.0.113.9 , ', '203.0.113.9'],
  ])('%s', (_label, header, expected) => {
    expect(clientIpFor(xff(header), UNTRUSTED)).toBe(expected);
  });

  it.each([
    ['no header', undefined],
    ['a blank header', ' , '],
    ['an unparseable peer', '6.6.6.6, not-an-ip'],
    ['a peer with a port', '203.0.113.9:443'],
  ])('%s → "" (Better Auth\'s shared no-trusted-ip bucket)', (_label, header) => {
    expect(clientIpFor(xff(header), UNTRUSTED)).toBe('');
  });

  it('TRUST_PROXY parses like the Go config: false/0/no/blank trust nothing, true/1/yes trust the proxy', () => {
    const header = xff('198.51.100.7, 172.18.0.5');
    for (const value of ['false', 'FALSE', '0', 'no', '']) {
      expect(clientIpFor(header, proxyTrustFromEnv({ TRUST_PROXY: value }))).toBe('172.18.0.5');
    }
    for (const value of ['true', 'TRUE', '1', 'yes', 'Yes']) {
      expect(clientIpFor(header, proxyTrustFromEnv({ TRUST_PROXY: value }))).toBe('198.51.100.7');
    }
  });

  it('an invalid TRUST_PROXY is a configuration error naming the variable', () => {
    expect(() => proxyTrustFromEnv({ TRUST_PROXY: 'on' })).toThrow('TRUST_PROXY must be true or false (also 1/0, yes/no), got "on"');
  });
});

describe('clientIpFor — TRUST_PROXY=true: right-most hop outside TRUSTED_PROXY_CIDRS', () => {
  it.each([
    ['Caddy (overwrites XFF) in front', '198.51.100.7, 172.18.0.5', '198.51.100.7'],
    ['an appending proxy: spoofed left entry ignored', '6.6.6.6, 198.51.100.7, 172.18.0.5', '198.51.100.7'],
    ['a proxy chain of trusted hops', '6.6.6.6, 198.51.100.7, 10.1.2.3, 172.18.0.5', '198.51.100.7'],
    ['a direct hit from an untrusted peer: the peer, never the left entry', '6.6.6.6, 203.0.113.9', '203.0.113.9'],
    ['a direct untrusted peer without other hops', '203.0.113.9', '203.0.113.9'],
    ['unparseable hops are skipped', '198.51.100.7, garbage, 198.51.100.8:80, 127.0.0.1', '198.51.100.7'],
    ['IPv6 behind a loopback proxy', '2001:db8::7, ::1', '2001:db8::7'],
    ['IPv4-mapped hops are unmapped before matching', '::ffff:198.51.100.7, ::ffff:10.0.0.2', '198.51.100.7'],
    ['hex IPv4-mapped hops are unmapped too', '::ffff:c633:6407, ::ffff:a00:2', '198.51.100.7'],
    ['expanded hex IPv4-mapped hops are unmapped too', '0:0:0:0:0:FFFF:C633:6407, 0000::ffff:0a00:0002', '198.51.100.7'],
  ])('%s', (_label, header, expected) => {
    expect(clientIpFor(xff(header), TRUSTED)).toBe(expected);
  });

  it.each([
    ['a LAN client behind Caddy gets its own bucket, not the proxy\'s', '192.168.1.20, 172.18.0.5', '192.168.1.20'],
    ['two LAN clients behind Caddy get different buckets', '192.168.1.21, 172.18.0.5', '192.168.1.21'],
    ['an all-trusted chain (VPN → proxy → Caddy): the left-most entry', '10.8.0.4, 10.0.0.2, 172.18.0.5', '10.8.0.4'],
    ['an all-trusted chain skips an unparseable left-most entry', 'garbage, 192.168.1.20, 172.18.0.5', '192.168.1.20'],
    ['an IPv4-mapped LAN client is unmapped', '::ffff:192.168.1.20, ::1', '192.168.1.20'],
    ['a lone trusted peer with no other hops: the peer', '172.18.0.5', '172.18.0.5'],
    ['a trusted peer whose other hops are all unparseable: the peer', 'garbage, 1.2.3.4:80, 172.18.0.5', '172.18.0.5'],
    ['spoofed private left entries never win over an untrusted hop', '192.168.1.99, 10.0.0.1, 198.51.100.7, 172.18.0.5', '198.51.100.7'],
    ['spoofed public and private left entries: still the right-most untrusted hop', '6.6.6.6, 10.0.0.1, 198.51.100.7, 10.1.2.3, 172.18.0.5', '198.51.100.7'],
  ])('every other hop trusted → left-most valid entry: %s', (_label, header, expected) => {
    expect(clientIpFor(xff(header), TRUSTED)).toBe(expected);
  });

  it('defaults to loopback + private ranges, like the Go API', () => {
    expect(DEFAULT_TRUSTED_PROXY_CIDRS).toEqual(['127.0.0.0/8', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '::1/128', 'fc00::/7']);
  });

  it('TRUSTED_PROXY_CIDRS narrows the trusted set', () => {
    const trust = proxyTrustFromEnv({ TRUST_PROXY: 'true', TRUSTED_PROXY_CIDRS: ' 203.0.113.0/24 , ::1/128 ' });
    expect(clientIpFor(xff('198.51.100.7, 203.0.113.1'), trust)).toBe('198.51.100.7');
    // 10.0.0.2 is no longer trusted: it is the client as far as we know.
    expect(clientIpFor(xff('198.51.100.7, 10.0.0.2'), trust)).toBe('10.0.0.2');
  });

  it('an invalid TRUSTED_PROXY_CIDRS entry is a configuration error naming the variable', () => {
    expect(() => proxyTrustFromEnv({ TRUST_PROXY: 'true', TRUSTED_PROXY_CIDRS: '10.0.0.0/8,not-a-cidr' })).toThrow(/TRUSTED_PROXY_CIDRS.*not-a-cidr/);
    expect(() => proxyTrustFromEnv({ TRUST_PROXY: 'true', TRUSTED_PROXY_CIDRS: '10.0.0.0/33' })).toThrow(/TRUSTED_PROXY_CIDRS/);
  });
});

/**
 * TRUSTED_PROXY_CIDRS grammar — identical on both tiers (Go config.parseHardening):
 * comma-separated, whitespace trimmed, empty entries ignored, a bare IP is a
 * single host (/32 or /128), anything else netip.ParsePrefix rejects is a boot error.
 */
describe('proxyTrustFromEnv — TRUSTED_PROXY_CIDRS grammar', () => {
  const trustFor = (cidrs: string) => proxyTrustFromEnv({ TRUST_PROXY: 'true', TRUSTED_PROXY_CIDRS: cidrs });
  // The resolved client for "<client>, <peer>": the client when the peer is trusted, the peer otherwise.
  const resolved = (cidrs: string, peer: string) => clientIpFor(xff(`198.51.100.7, ${peer}`), trustFor(cidrs));

  it('a bare IPv4 address trusts exactly that host (/32)', () => {
    expect(resolved('203.0.113.5', '203.0.113.5')).toBe('198.51.100.7');
    expect(resolved('203.0.113.5', '203.0.113.6')).toBe('203.0.113.6');
  });

  it('a bare IPv6 address trusts exactly that host (/128)', () => {
    expect(resolved('2001:db8::5', '2001:db8::5')).toBe('198.51.100.7');
    expect(resolved('2001:DB8::5', '2001:db8::6')).toBe('2001:db8::6');
  });

  it('trims whitespace and ignores empty entries', () => {
    const cidrs = ' , 203.0.113.0/24 ,, \t2001:db8::/32 , ';
    expect(resolved(cidrs, '203.0.113.9')).toBe('198.51.100.7');
    expect(resolved(cidrs, '2001:db8::9')).toBe('198.51.100.7');
    // Defaults are replaced, not merged.
    expect(resolved(cidrs, '10.0.0.2')).toBe('10.0.0.2');
  });

  it('a blank value means the defaults', () => {
    expect(resolved('', '172.18.0.5')).toBe('198.51.100.7');
    expect(resolved('   ', '172.18.0.5')).toBe('198.51.100.7');
  });

  it('an IPv4-mapped entry in any textual form is unmapped (bare IP = /32)', () => {
    for (const entry of ['::ffff:a00:1', '::ffff:10.0.0.1', '0:0:0:0:0:ffff:0a00:0001']) {
      expect(resolved(entry, '10.0.0.1')).toBe('198.51.100.7');
      expect(resolved(entry, '10.0.0.2')).toBe('10.0.0.2');
    }
  });

  it('canonicalIp unmaps hex IPv4-mapped addresses', () => {
    expect(canonicalIp('::ffff:a00:1')).toBe('10.0.0.1');
    expect(canonicalIp('::FFFF:C633:6407')).toBe('198.51.100.7');
    expect(canonicalIp('::ffff:1:2:3')).toBe('::ffff:1:2:3'); // not a mapped address
    expect(canonicalIp('64:ff9b::a00:1')).toBe('64:ff9b::a00:1');
  });

  it('a non-network-aligned prefix is accepted as its network (like netip.ParsePrefix)', () => {
    expect(resolved('203.0.113.77/24', '203.0.113.1')).toBe('198.51.100.7');
  });

  it.each([
    ['garbage', 'proxy'],
    ['a hostname', 'caddy'],
    ['a prefix over 32 for IPv4', '10.0.0.0/33'],
    ['a prefix over 128 for IPv6', '::1/129'],
    ['an empty prefix', '10.0.0.0/'],
    ['a leading-zero prefix', '10.0.0.0/08'],
    ['a signed prefix', '10.0.0.0/+8'],
    ['two slashes', '10.0.0.0/8/8'],
    ['an IPv6 zone', 'fe80::1%eth0/64'],
    ['a bare IPv6 address with a zone', 'fe80::1%eth0'],
    ['an IPv4 address with leading zeros', '010.0.0.1/32'],
    ['an address with a port', '10.0.0.1:80'],
    ['an invalid entry next to valid ones', '10.0.0.0/8, nope, ::1'],
  ])('rejects %s at boot', (_label, cidrs) => {
    expect(() => trustFor(cidrs)).toThrow(/TRUSTED_PROXY_CIDRS/);
  });

  it('is validated even while TRUST_PROXY is off (the Go config always parses it)', () => {
    expect(() => proxyTrustFromEnv({ TRUST_PROXY: 'false', TRUSTED_PROXY_CIDRS: 'nope' })).toThrow(/TRUSTED_PROXY_CIDRS/);
    expect(() => proxyTrustFromEnv({ TRUSTED_PROXY_CIDRS: '10.0.0.1' })).not.toThrow();
  });
});

describe('withClientIp', () => {
  it('overwrites a forged x-calendium-client-ip and preserves method, URL and body', async () => {
    const original = new Request('https://mail.example.com/api/auth/sign-in/email', {
      method: 'POST',
      headers: { 'content-type': 'application/json', [CLIENT_IP_HEADER]: '1.2.3.4', 'x-forwarded-for': '6.6.6.6, 203.0.113.9' },
      body: JSON.stringify({ email: 'a@b.test' }),
    });
    const stamped = withClientIp(original, UNTRUSTED);
    expect(stamped.headers.get(CLIENT_IP_HEADER)).toBe('203.0.113.9');
    expect(stamped.method).toBe('POST');
    expect(stamped.url).toBe(original.url);
    expect(stamped.headers.get('content-type')).toBe('application/json');
    expect(await stamped.text()).toBe(JSON.stringify({ email: 'a@b.test' }));
  });

  it('stamps the right-most untrusted hop when the proxy is trusted', () => {
    const req = new Request('https://mail.example.com/api/auth/token', { headers: { 'x-forwarded-for': '6.6.6.6, 198.51.100.7, 172.18.0.5' } });
    expect(withClientIp(req, TRUSTED).headers.get(CLIENT_IP_HEADER)).toBe('198.51.100.7');
  });

  it('stamps "" rather than leaving a forged value when nothing resolves', () => {
    const req = new Request('https://mail.example.com/api/auth/token', { headers: { [CLIENT_IP_HEADER]: '1.2.3.4' } });
    expect(withClientIp(req, UNTRUSTED).headers.get(CLIENT_IP_HEADER)).toBe('');
  });
});
