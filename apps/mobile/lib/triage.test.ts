import { snoozePresets } from './triage';

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
