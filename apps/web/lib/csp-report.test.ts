// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { parseCspReports } from '@/lib/csp-report';

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
});
