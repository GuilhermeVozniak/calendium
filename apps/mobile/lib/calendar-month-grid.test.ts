import { monthGridDays } from './calendar-month-grid';

describe('monthGridDays', () => {
  it('returns 42 consecutive days (6 weeks) starting on a Monday', () => {
    const days = monthGridDays(new Date('2026-07-15'));
    expect(days).toHaveLength(42);
    expect(days[0]?.getDay()).toBe(1);
    for (let i = 1; i < 42; i++) {
      expect(days[i]!.getTime() - days[i - 1]!.getTime()).toBe(86_400_000);
    }
  });

  it('the grid contains every day of the target month', () => {
    const days = monthGridDays(new Date('2026-02-10')); // February 2026 (28 days)
    const daysInFeb = days.filter((d) => d.getMonth() === 1);
    expect(daysInFeb).toHaveLength(28);
    expect(daysInFeb[0]?.getDate()).toBe(1);
    expect(daysInFeb[daysInFeb.length - 1]?.getDate()).toBe(28);
  });

  it('spans a different month when navigated', () => {
    const days = monthGridDays(new Date('2026-03-01'));
    const daysInMarch = days.filter((d) => d.getMonth() === 2);
    expect(daysInMarch).toHaveLength(31);
  });
});
