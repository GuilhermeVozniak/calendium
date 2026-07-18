import { startOfWeek } from './format';
import { tickerDays, tickerSelectedIndex } from './calendar-ticker';

describe('tickerDays', () => {
  it('returns 7 consecutive days starting at weekStart', () => {
    const weekStart = startOfWeek(new Date('2026-07-15T12:00:00'));
    const days = tickerDays(weekStart);
    expect(days).toHaveLength(7);
    expect(days[0]?.getTime()).toBe(weekStart.getTime());
    for (let i = 1; i < 7; i++) {
      expect(days[i]!.getTime() - days[i - 1]!.getTime()).toBe(86_400_000);
    }
  });
});

describe('tickerSelectedIndex', () => {
  const weekStart = startOfWeek(new Date('2026-07-15T12:00:00'));

  it('finds the index of a day within the visible week', () => {
    expect(tickerSelectedIndex(weekStart, weekStart)).toBe(0);
    const wednesday = new Date(weekStart);
    wednesday.setDate(wednesday.getDate() + 2);
    expect(tickerSelectedIndex(weekStart, wednesday)).toBe(2);
    const sunday = new Date(weekStart);
    sunday.setDate(sunday.getDate() + 6);
    expect(tickerSelectedIndex(weekStart, sunday)).toBe(6);
  });

  it('returns -1 for a day outside the visible week', () => {
    const nextWeek = new Date(weekStart);
    nextWeek.setDate(nextWeek.getDate() + 7);
    expect(tickerSelectedIndex(weekStart, nextWeek)).toBe(-1);
    const prevDay = new Date(weekStart);
    prevDay.setDate(prevDay.getDate() - 1);
    expect(tickerSelectedIndex(weekStart, prevDay)).toBe(-1);
  });
});
