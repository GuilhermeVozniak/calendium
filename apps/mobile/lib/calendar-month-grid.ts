// Month mini-grid for date jumping (Task 20), mirroring web's mini-month.tsx
// grid math with mobile conventions (plain Date arrays instead of date-fns).
import { addDays, startOfWeek } from '@/lib/format';

/** Full 6-week (42-day) grid for the calendar month containing `month`, Monday-first. */
export function monthGridDays(month: Date): Date[] {
  const firstOfMonth = new Date(month.getFullYear(), month.getMonth(), 1);
  const gridStart = startOfWeek(firstOfMonth);
  return Array.from({ length: 42 }, (_, i) => addDays(gridStart, i));
}
