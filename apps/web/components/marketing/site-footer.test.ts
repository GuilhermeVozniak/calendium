import { describe, expect, it } from 'vitest';

import { copyrightLine } from '@/components/marketing/site-footer';

describe('copyrightLine', () => {
  it('renders © <year> Calendium with no legal suffix', () => {
    expect(copyrightLine(2026)).toBe('© 2026 Calendium');
    expect(copyrightLine(2031)).toBe('© 2031 Calendium');
  });

  it('defaults to the current year so the footer never goes stale', () => {
    expect(copyrightLine()).toBe(`© ${new Date().getFullYear()} Calendium`);
  });
});
