// Horizontal day-strip (DayTicker-style, Task 10's semantics) for the
// calendar tab's week picker.
import { addDays, isSameDay } from '@/lib/format';

/** The 7 days of the ticker strip for the week starting at `weekStart`. */
export function tickerDays(weekStart: Date): Date[] {
  return Array.from({ length: 7 }, (_, i) => addDays(weekStart, i));
}

/** Index of `day` within the ticker strip for `weekStart`, or -1 if outside that week. */
export function tickerSelectedIndex(weekStart: Date, day: Date): number {
  return tickerDays(weekStart).findIndex((d) => isSameDay(d, day));
}
