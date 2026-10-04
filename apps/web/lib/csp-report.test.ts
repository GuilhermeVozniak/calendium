// @vitest-environment node
import { describe, expect, it } from 'vitest';

import {
  MAX_CSP_FIELD_CHARS,
  createCspReportLimiter,
  parseCspReports,
  redactReportUrl,
} from '@/lib/csp-report';

describe('parseCspReports', () => {
  it('normalises a legacy report-uri body', () => {
    expect(
      parseCspReports({
        'csp-report': {
          'document-uri': 'https://app.example.com/mail',
          'violated-directive': 'script-src',
          'blocked-uri': 'https://evil.example/x.js',
          'source-file': 'https://app.example.com/_next/a.js',
          'line-number': 12,
        },
      })
    ).toEqual([
      {
        documentUri: 'https://app.example.com/mail',
        violatedDirective: 'script-src',
        blockedUri: 'https://evil.example/x.js',
        sourceFile: 'https://app.example.com/_next/a.js',
        lineNumber: 12,
      },
    ]);
  });
  it('normalises a Reporting API array and ignores other report types', () => {
    expect(
      parseCspReports([
        { type: 'deprecation', body: {} },
        { type: 'csp-violation', body: null },
        {
          type: 'csp-violation',
          body: { documentURL: 'https://a/b', effectiveDirective: 'img-src', blockedURL: 'http://x/y.png' },
        },
      ])
    ).toEqual([
      { documentUri: 'https://a/b', violatedDirective: 'img-src', blockedUri: 'http://x/y.png', sourceFile: '', lineNumber: null },
    ]);
  });
  it('returns an empty list for anything else', () => {
    expect(parseCspReports('nope')).toEqual([]);
    expect(parseCspReports({ other: 1 })).toEqual([]);
    expect(parseCspReports({ 'csp-report': null })).toEqual([]);
    expect(parseCspReports(null)).toEqual([]);
  });
  it('redacts every URL field and truncates every string field', () => {
    const [report] = parseCspReports({
      'csp-report': {
        'document-uri': 'https://app.example.com/shared/SHARETOKEN?utm=1#frag',
        'violated-directive': `script-src ${'x'.repeat(500)}`,
        'blocked-uri': 'https://app.example.com/poll/POLLTOKEN',
        'source-file': 'https://app.example.com/reset-password?token=RESETTOKEN',
      },
    });
    expect(report).toEqual({
      documentUri: 'https://app.example.com/shared/:token',
      violatedDirective: `script-src ${'x'.repeat(MAX_CSP_FIELD_CHARS - 'script-src '.length)}`,
      blockedUri: 'https://app.example.com/poll/:token',
      sourceFile: 'https://app.example.com/reset-password',
      lineNumber: null,
    });
  });
});

describe('redactReportUrl', () => {
  it.each([
    ['keeps origin + path', 'https://app.example.com/mail/inbox', 'https://app.example.com/mail/inbox'],
    ['drops the query and fragment', 'https://app.example.com/mail?q=secret#x', 'https://app.example.com/mail'],
    ['drops userinfo', 'https://user:pass@cdn.example.com/a.js', 'https://cdn.example.com/a.js'],
    ['keeps a non-default port', 'http://localhost:3000/calendar', 'http://localhost:3000/calendar'],
    ['redacts a shared-thread token', 'https://app.example.com/shared/abc123DEF', 'https://app.example.com/shared/:token'],
    ['redacts a poll token', 'https://app.example.com/poll/abc123', 'https://app.example.com/poll/:token'],
    ['redacts a booking link', 'https://app.example.com/book/jane-x7k2/confirm', 'https://app.example.com/book/:token/:token'],
    ['redacts a booking token', 'https://app.example.com/booking/abc123', 'https://app.example.com/booking/:token'],
    ['redacts a path-style reset token', 'https://app.example.com/api/auth/reset-password/RT?callbackURL=/x', 'https://app.example.com/api/auth/reset-password/:token'],
    ['drops the reset-password query token', 'https://app.example.com/reset-password?token=RT', 'https://app.example.com/reset-password'],
    ['redacts after a percent-encoded parent', 'https://app.example.com/%73hared/T1', 'https://app.example.com/%73hared/:token'],
    ['redacts after an upper-case parent', 'https://app.example.com/Shared/T1', 'https://app.example.com/Shared/:token'],
    ['keeps a trailing slash on the parent alone', 'https://app.example.com/shared/', 'https://app.example.com/shared/'],
    ['reduces a blob: URL to its scheme', 'blob:https://app.example.com/0f1e-2d3c', 'blob:'],
    ['reduces a data: URL to its scheme', 'data:text/html;base64,PHNjcmlwdD4=', 'data:'],
    ['reduces an extension URL to its scheme', 'chrome-extension://abcdef/inject.js', 'chrome-extension:'],
    ['keeps a CSP keyword', 'inline', 'inline'],
    ['keeps another CSP keyword', 'wasm-eval', 'wasm-eval'],
    ['keeps an empty value', '', ''],
    ['drops an unparseable value', 'not a url /shared/TOKEN', ''],
  ])('%s', (_label, raw, expected) => {
    expect(redactReportUrl(raw)).toBe(expected);
  });
  it('caps the result length', () => {
    expect(redactReportUrl(`https://app.example.com/${'a'.repeat(1000)}`)).toHaveLength(MAX_CSP_FIELD_CHARS);
  });
});

describe('createCspReportLimiter', () => {
  it('allows 60 reports per key per minute, then none until the window ends', () => {
    let now = 0;
    const limiter = createCspReportLimiter({ now: () => now });
    expect(limiter.take('a', 5)).toBe(5);
    for (let i = 0; i < 10; i++) limiter.take('a', 5);
    expect(limiter.take('a', 5)).toBe(5); // 60th report
    expect(limiter.take('a', 5)).toBe(0);
    expect(limiter.take('b', 5)).toBe(5); // keys are independent
    now = 59_999;
    expect(limiter.take('a', 1)).toBe(0);
    now = 60_000;
    expect(limiter.take('a', 5)).toBe(5);
  });
  it('grants the remainder of a partly used window', () => {
    const limiter = createCspReportLimiter({ now: () => 0 });
    expect(limiter.take('a', 58)).toBe(58);
    expect(limiter.take('a', 5)).toBe(2);
    expect(limiter.take('a', 5)).toBe(0);
  });
  it('bounds memory: evicts expired keys, then the oldest, beyond maxKeys', () => {
    let now = 0;
    const limiter = createCspReportLimiter({ now: () => now, maxKeys: 3 });
    limiter.take('a', 60);
    limiter.take('b', 60);
    limiter.take('c', 60);
    limiter.take('d', 60); // evicts 'a', the oldest
    expect(limiter.size()).toBe(3);
    expect(limiter.take('a', 1)).toBe(1);
    now = 60_000;
    limiter.take('e', 1); // at capacity: the expired windows go first
    expect(limiter.size()).toBe(1);
  });
});
