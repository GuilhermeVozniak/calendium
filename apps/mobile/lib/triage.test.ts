import { canUnsubscribe, nextAfterRemoval, snoozePresets, unsubscribeMessage, zeroCutoffs } from './triage';

describe('snoozePresets', () => {
  const now = new Date('2026-07-17T15:30:00');

  it('returns later-today, tomorrow 8am, next-week 8am', () => {
    const [later, tomorrow, nextWeek] = snoozePresets(now);
    expect(later.until.getTime() - now.getTime()).toBe(3 * 60 * 60 * 1000);
    expect(tomorrow.until.getDate()).toBe(18);
    expect(tomorrow.until.getHours()).toBe(8);
    expect(tomorrow.until.getMinutes()).toBe(0);
    expect(nextWeek.until.getDate()).toBe(24);
    expect(nextWeek.until.getHours()).toBe(8);
    expect([later.label, tomorrow.label, nextWeek.label]).toEqual([
      'Later today',
      'Tomorrow 8 AM',
      'Next week',
    ]);
  });

  it('rolls tomorrow/next-week across month boundaries', () => {
    const endOfMonth = new Date('2026-07-31T22:00:00');
    const [, tomorrow, nextWeek] = snoozePresets(endOfMonth);
    expect(tomorrow.until.getMonth()).toBe(7); // August
    expect(tomorrow.until.getDate()).toBe(1);
    expect(nextWeek.until.getMonth()).toBe(7);
    expect(nextWeek.until.getDate()).toBe(7);
  });

  it('does not mutate the input date', () => {
    const input = new Date('2026-07-17T15:30:00');
    const before = input.getTime();
    snoozePresets(input);
    expect(input.getTime()).toBe(before);
  });
});

describe('mobile triage helpers', () => {
  it('zeroCutoffs produces four descending cutoffs in ISO form', () => {
    const now = new Date('2026-07-17T12:00:00Z');
    const cutoffs = zeroCutoffs(now);
    expect(cutoffs.map((c) => c.id)).toEqual(['week', 'two-weeks', 'month', 'quarter']);
    expect(cutoffs[0]!.iso).toBe('2026-07-10T12:00:00.000Z');
    for (let i = 1; i < cutoffs.length; i++) {
      expect(Date.parse(cutoffs[i]!.iso)).toBeLessThan(Date.parse(cutoffs[i - 1]!.iso));
    }
  });

  it('canUnsubscribe requires a mailto or url', () => {
    expect(canUnsubscribe({ unsubscribeMailto: null, unsubscribeUrl: null })).toBe(false);
    expect(canUnsubscribe({ unsubscribeMailto: 'mailto:u@x.y', unsubscribeUrl: null })).toBe(true);
    expect(canUnsubscribe({ unsubscribeMailto: null, unsubscribeUrl: 'https://x.y/u' })).toBe(true);
  });

  it('unsubscribeMessage distinguishes the link method', () => {
    expect(unsubscribeMessage({ method: 'link', url: 'https://x.y/u' })).toContain('Opening');
    expect(unsubscribeMessage({ method: 'one_click' })).toContain('Unsubscribed');
  });

  it('re-exports auto-advance', () => {
    expect(nextAfterRemoval(['a', 'b'], 'a')).toBe('b');
  });
});
