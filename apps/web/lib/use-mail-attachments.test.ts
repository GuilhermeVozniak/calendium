import { describe, expect, it, vi } from 'vitest';

// use-mail.ts pulls in react-query hooks, the API client, auth, etc. — mock
// the pieces that would otherwise touch the network/browser APIs at import
// time, matching the pattern in use-mail-reactions.test.tsx. Only
// parseContentDispositionFilename (a pure function) is exercised here.
vi.mock('@/lib/demo', () => ({ DEMO_MODE: false }));
vi.mock('@/lib/api', () => ({ getApiClient: () => ({}) }));
vi.mock('@/lib/auth-client', () => ({ getAccessToken: async () => null }));
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

import { parseContentDispositionFilename } from '@/lib/use-mail';

/**
 * M2.5 review fix (MINOR c): parseContentDispositionFilename had no direct
 * unit tests — only indirectly exercised through fetchAttachmentBlob. It
 * mirrors the backend's escapeQuotedString (handleGetAttachmentContent), so
 * the quoted-string and backslash-escaping edge cases are worth pinning
 * directly.
 */
describe('parseContentDispositionFilename', () => {
  it('parses a plain quoted filename', () => {
    expect(parseContentDispositionFilename('inline; filename="report.pdf"')).toBe('report.pdf');
  });

  it('unescapes backslash-escaped quotes inside the filename', () => {
    // Mirrors the backend's escapeQuotedString: a literal `"` in the filename
    // is escaped as \" when the header is built, and must be reversed here.
    expect(parseContentDispositionFilename('inline; filename="my \\"favorite\\" file.pdf"')).toBe(
      'my "favorite" file.pdf'
    );
  });

  it('unescapes a backslash-escaped backslash inside the filename', () => {
    expect(parseContentDispositionFilename('inline; filename="a\\\\b.pdf"')).toBe('a\\b.pdf');
  });

  it('falls back to null when the header is missing entirely', () => {
    expect(parseContentDispositionFilename(null)).toBeNull();
  });

  it('falls back to null when the header has no filename parameter', () => {
    expect(parseContentDispositionFilename('inline')).toBeNull();
  });

  it('falls back to null when the header is present but empty', () => {
    expect(parseContentDispositionFilename('')).toBeNull();
  });
});
