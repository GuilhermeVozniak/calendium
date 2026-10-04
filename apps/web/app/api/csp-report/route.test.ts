// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { POST } from '@/app/api/csp-report/route';

/** Each test reports from its own peer so the module-level per-IP limiter never couples tests. */
let peer = 0;
let ip = '';
beforeEach(() => {
  peer += 1;
  ip = `203.0.113.${peer}`;
});

function post(body: string, type = 'application/csp-report', headers: Record<string, string> = {}): Request {
  return new Request('http://localhost/api/csp-report', {
    method: 'POST',
    headers: { 'content-type': type, 'user-agent': 'vitest', 'x-forwarded-for': ip, ...headers },
    body,
  });
}

const reportsJson = (n: number, body: Record<string, unknown> = {}) =>
  JSON.stringify(
    Array.from({ length: n }, (_, i) => ({
      type: 'csp-violation',
      body: { documentURL: `https://app.example.com/mail/${i}`, effectiveDirective: 'img-src', ...body },
    }))
  );

describe('POST /api/csp-report', () => {
  afterEach(() => vi.restoreAllMocks());

  it('logs one csp_violation line per report and answers 204', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(
      post(
        JSON.stringify({
          'csp-report': { 'document-uri': 'https://a/b', 'violated-directive': 'script-src', 'blocked-uri': 'inline' },
        })
      )
    );
    expect(res.status).toBe(204);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(JSON.parse(warn.mock.calls[0][0] as string)).toEqual({
      msg: 'csp_violation',
      documentUri: 'https://a/b',
      violatedDirective: 'script-src',
      blockedUri: 'inline',
      sourceFile: '',
      lineNumber: null,
      userAgent: 'vitest',
    });
  });
  it('accepts application/reports+json', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(
      post(
        JSON.stringify([
          { type: 'csp-violation', body: { documentURL: 'https://a', effectiveDirective: 'img-src', blockedURL: 'x' } },
        ]),
        'application/reports+json'
      )
    );
    expect(res.status).toBe(204);
    expect(warn).toHaveBeenCalledTimes(1);
  });
  it('answers 204 without logging for a well-formed body with no CSP reports', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(post(JSON.stringify({ other: 1 })));
    expect(res.status).toBe(204);
    expect(warn).not.toHaveBeenCalled();
  });
  it('rejects other content types and malformed JSON with 400', async () => {
    expect((await POST(post('{}', 'text/plain'))).status).toBe(400);
    expect((await POST(post('{not json'))).status).toBe(400);
  });
  it('rejects bodies over 16 KiB with 413', async () => {
    const res = await POST(post(JSON.stringify({ 'csp-report': { 'blocked-uri': 'x'.repeat(16 * 1024) } })));
    expect(res.status).toBe(413);
  });
  it('rejects a declared Content-Length over 16 KiB with 413 before reading', async () => {
    const req = new Request('http://localhost/api/csp-report', {
      method: 'POST',
      headers: { 'content-type': 'application/csp-report', 'content-length': String(16 * 1024 + 1) },
      body: '{}',
    });
    expect((await POST(req)).status).toBe(413);
  });

  it('never writes a capability token or query string to the logs', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const legacy = (doc: string, blocked: string, source: string) =>
      post(JSON.stringify({ 'csp-report': { 'document-uri': doc, 'blocked-uri': blocked, 'source-file': source, 'violated-directive': 'script-src' } }));
    await POST(
      legacy(
        'https://app.example.com/shared/SHARE-TOKEN-1?ref=SHARE-QUERY#SHARE-FRAG',
        'https://app.example.com/poll/POLL-TOKEN-2/vote?k=POLL-QUERY',
        'https://app.example.com/reset-password?token=RESET-TOKEN-3'
      )
    );
    await POST(
      post(
        JSON.stringify([
          {
            type: 'csp-violation',
            body: {
              documentURL: 'https://app.example.com/book/BOOK-TOKEN-4?email=a@b.test',
              blockedURL: 'https://app.example.com/api/auth/reset-password/RESET-TOKEN-5?callbackURL=/x',
              sourceFile: 'https://app.example.com/booking/BOOKING-TOKEN-6',
              effectiveDirective: 'img-src',
            },
          },
        ]),
        'application/reports+json'
      )
    );
    expect(warn).toHaveBeenCalledTimes(2);
    const logged = warn.mock.calls.map((c) => String(c[0])).join('\n');
    for (const secret of ['TOKEN', 'QUERY', 'FRAG', 'a@b.test', 'callbackURL', '?', '#']) {
      expect(logged).not.toContain(secret);
    }
    expect(JSON.parse(String(warn.mock.calls[0][0]))).toMatchObject({
      documentUri: 'https://app.example.com/shared/:token',
      blockedUri: 'https://app.example.com/poll/:token/:token',
      sourceFile: 'https://app.example.com/reset-password',
    });
  });

  it('logs at most 5 reports per request', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const res = await POST(post(reportsJson(100), 'application/reports+json'));
    expect(res.status).toBe(204);
    expect(warn).toHaveBeenCalledTimes(5);
  });

  it('truncates the User-Agent to 120 characters and every other field', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    await POST(
      post(reportsJson(1, { effectiveDirective: 'd'.repeat(5000) }), 'application/reports+json', { 'user-agent': 'U'.repeat(5000) })
    );
    const line = JSON.parse(String(warn.mock.calls[0][0]));
    expect(line.userAgent).toBe('U'.repeat(120));
    expect(line.violatedDirective.length).toBeLessThanOrEqual(200);
    expect(String(warn.mock.calls[0][0]).length).toBeLessThan(1024);
  });

  it('logs at most 60 reports per client IP per minute; the excess still answers 204', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const statuses: number[] = [];
    for (let i = 0; i < 20; i++) statuses.push((await POST(post(reportsJson(5), 'application/reports+json'))).status);
    expect(statuses.every((s) => s === 204)).toBe(true);
    expect(warn).toHaveBeenCalledTimes(60);
    // Another client is unaffected.
    await POST(post(reportsJson(1), 'application/reports+json', { 'x-forwarded-for': '198.51.100.250' }));
    expect(warn).toHaveBeenCalledTimes(61);
  });

  it('keys the limit on the resolved client IP, not a client-chosen X-Forwarded-For entry', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    // TRUST_PROXY is off: the right-most entry (the socket peer) is the key, so
    // rotating a spoofed left entry does not buy a fresh bucket.
    for (let i = 0; i < 20; i++) {
      await POST(post(reportsJson(5), 'application/reports+json', { 'x-forwarded-for': `6.6.6.${i}, ${ip}` }));
    }
    expect(warn).toHaveBeenCalledTimes(60);
  });
});
