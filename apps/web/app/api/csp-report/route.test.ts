// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';

import { POST } from '@/app/api/csp-report/route';

function post(body: string, type = 'application/csp-report'): Request {
  return new Request('http://localhost/api/csp-report', {
    method: 'POST',
    headers: { 'content-type': type, 'user-agent': 'vitest' },
    body,
  });
}

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
});
