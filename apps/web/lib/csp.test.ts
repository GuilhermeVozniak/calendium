// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { cspPolicy, makeNonce } from '@/lib/csp';

describe('cspPolicy', () => {
  it('renders the production nonce policy', () => {
    const policy = cspPolicy({ nonce: 'abc123', apiUrl: 'https://api.example.com', dev: false });
    expect(policy).toBe(
      "default-src 'self'; script-src 'self' 'nonce-abc123' https://cdn.paddle.com https://public.profitwell.com; frame-src blob: https://*.paddle.com https://accounts.google.com https://appleid.apple.com; connect-src 'self' https://api.example.com https://*.paddle.com https://*.profitwell.com https://api.profitwell-events.com; img-src 'self' data: blob: https:; style-src 'self' 'unsafe-inline' https://cdn.paddle.com https://sandbox-cdn.paddle.com; font-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self' https://appleid.apple.com"
    );
  });
  it('omits the API origin when same-origin', () => {
    const policy = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    expect(policy).toContain("connect-src 'self' https://*.paddle.com https://*.profitwell.com");
  });
  it('adds unsafe-eval and ws: in dev only', () => {
    const dev = cspPolicy({ nonce: 'n', apiUrl: '', dev: true });
    expect(dev).toContain("script-src 'self' 'nonce-n' https://cdn.paddle.com https://public.profitwell.com 'unsafe-eval'");
    expect(dev).toContain('https://api.profitwell-events.com ws:;');
    const prod = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    expect(prod).not.toContain('unsafe-eval');
    expect(prod).not.toContain('ws:');
  });
  it('uses unsafe-inline for scripts when no nonce is given (the /offline page)', () => {
    const policy = cspPolicy({ apiUrl: '', dev: false });
    expect(policy).toContain("script-src 'self' 'unsafe-inline' https://cdn.paddle.com");
    expect(policy).not.toContain('nonce-');
  });
  it('covers every host Paddle.js v2 touches in live and sandbox mode', () => {
    const policy = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    const directive = (name: string) =>
      policy
        .split('; ')
        .find((d) => d.startsWith(`${name} `))
        ?.split(' ')
        .slice(1) ?? [];
    // paddle.js itself, plus the Retain/ProfitWell snippet it injects on Initialize() in live mode.
    expect(directive('script-src')).toEqual(expect.arrayContaining(['https://cdn.paddle.com', 'https://public.profitwell.com']));
    // paddle.css <link> (live + sandbox CDN) and its injected <style>/style attributes.
    expect(directive('style-src')).toEqual(
      expect.arrayContaining(["'unsafe-inline'", 'https://cdn.paddle.com', 'https://sandbox-cdn.paddle.com'])
    );
    // Overlay iframe on buy./sandbox-buy.paddle.com (and Retain widgets).
    expect(directive('frame-src')).toContain('https://*.paddle.com');
    // fetch() to api./sandbox-api.paddle.com; ProfitWell beacons.
    expect(directive('connect-src')).toEqual(
      expect.arrayContaining(['https://*.paddle.com', 'https://*.profitwell.com', 'https://api.profitwell-events.com'])
    );
  });
  it('allows blob: frames and images for the in-memory attachment preview', () => {
    const policy = cspPolicy({ nonce: 'n', apiUrl: '', dev: false });
    expect(policy).toContain('frame-src blob: ');
    expect(policy).toContain("img-src 'self' data: blob: https:");
  });
});

describe('makeNonce', () => {
  it('returns base64 of 16 random bytes and never repeats', () => {
    const a = makeNonce();
    const b = makeNonce();
    expect(a).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(a).not.toBe(b);
  });
});
