// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { CLIENT_IP_HEADER } from '@/lib/auth-env';
import { DEFAULT_TRUSTED_PROXY_CIDRS, clientIpFor, proxyTrustFromEnv, withClientIp } from '@/lib/client-ip';

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

  it('TRUST_PROXY values other than "true" do not trust anything', () => {
    const header = xff('198.51.100.7, 172.18.0.5');
    for (const value of ['false', '1', 'TRUE', 'yes', '']) {
      expect(clientIpFor(header, proxyTrustFromEnv({ TRUST_PROXY: value }))).toBe('172.18.0.5');
    }
  });
});

describe('clientIpFor — TRUST_PROXY=true: right-most hop outside TRUSTED_PROXY_CIDRS', () => {
  it.each([
    ['Caddy (overwrites XFF) in front', '198.51.100.7, 172.18.0.5', '198.51.100.7'],
    ['an appending proxy: spoofed left entry ignored', '6.6.6.6, 198.51.100.7, 172.18.0.5', '198.51.100.7'],
    ['a proxy chain of trusted hops', '6.6.6.6, 198.51.100.7, 10.1.2.3, 172.18.0.5', '198.51.100.7'],
    ['a direct hit from an untrusted peer: the peer, never the left entry', '6.6.6.6, 203.0.113.9', '203.0.113.9'],
    ['a direct untrusted peer without other hops', '203.0.113.9', '203.0.113.9'],
    ['every hop trusted (LAN client): the peer itself', '192.168.1.20, 10.0.0.2', '10.0.0.2'],
    ['unparseable hops are skipped', '198.51.100.7, garbage, 198.51.100.8:80, 127.0.0.1', '198.51.100.7'],
    ['IPv6 behind a loopback proxy', '2001:db8::7, ::1', '2001:db8::7'],
    ['IPv4-mapped hops are unmapped before matching', '::ffff:198.51.100.7, ::ffff:10.0.0.2', '198.51.100.7'],
  ])('%s', (_label, header, expected) => {
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
