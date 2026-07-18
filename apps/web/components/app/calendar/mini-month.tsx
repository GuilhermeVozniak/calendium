'use client';

import * as React from 'react';
import {
  addDays,
  addMonths,
  endOfMonth,
  endOfWeek,
  format,
  isSameDay,
  isSameMonth,
  isToday,
  startOfMonth,
  startOfWeek,
} from 'date-fns';
import { ChevronLeft, ChevronRight } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { WEEK_OPTS } from '@/lib/calendar-views';
import { cn } from '@/lib/utils';

const MAX_DENSITY_DOTS = 3;

export interface MiniMonthProps {
  /** Currently picked day, drawn with a filled circle. Pass `null` when there
   *  is no meaningful selection to show (e.g. quarter/year overview grids). */
  selected: Date | null;
  onSelect: (day: Date) => void;
  /**
   * Pins the displayed grid to a specific month, decoupled from `selected` —
   * used by quarter/year overviews that render many MiniMonths side by side,
   * each anchored to a different month. Hides the prev/next chevrons (the
   * caller owns which month is shown). Omit it to keep the existing
   * left-rail picker behavior: the grid follows `selected` and stays
   * user-navigable.
   */
  month?: Date;
  /** Event count per `yyyy-MM-dd`, rendered as 0-3 intensity dots under each day. */
  density?: Map<string, number>;
}

export function MiniMonth({ selected, onSelect, month, density }: MiniMonthProps) {
  const controlled = month !== undefined;
  const [internalMonth, setInternalMonth] = React.useState(() =>
    startOfMonth(month ?? selected ?? new Date())
  );
  React.useEffect(() => {
    if (!controlled) setInternalMonth(startOfMonth(selected ?? new Date()));
  }, [selected, controlled]);
  const displayMonth = controlled ? startOfMonth(month as Date) : internalMonth;

  const days = React.useMemo(() => {
    const list: Date[] = [];
    const end = endOfWeek(endOfMonth(displayMonth), WEEK_OPTS);
    for (let d = startOfWeek(startOfMonth(displayMonth), WEEK_OPTS); d <= end; d = addDays(d, 1)) {
      list.push(d);
    }
    return list;
  }, [displayMonth]);

  return (
    <div>
      <div className="mb-2 flex items-center justify-between px-1">
        <span className="text-sm font-semibold">{format(displayMonth, 'MMMM yyyy')}</span>
        {!controlled && (
          <div className="flex gap-0.5">
            <Button
              variant="ghost"
              size="icon"
              className="size-6"
              onClick={() => setInternalMonth((m) => addMonths(m, -1))}
              aria-label="Previous month"
            >
              <ChevronLeft className="size-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="size-6"
              onClick={() => setInternalMonth((m) => addMonths(m, 1))}
              aria-label="Next month"
            >
              <ChevronRight className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
      <div className="grid grid-cols-7 gap-y-0.5 text-center">
        {['S', 'M', 'T', 'W', 'T', 'F', 'S'].map((label, i) => (
          <span key={`${label}-${i}`} className="text-[10px] font-medium text-muted-foreground">
            {label}
          </span>
        ))}
        {days.map((day) => {
          const key = format(day, 'yyyy-MM-dd');
          const isSelected = selected != null && isSameDay(day, selected);
          const dots = Math.min(density?.get(key) ?? 0, MAX_DENSITY_DOTS);
          return (
            <div key={day.toISOString()} className="flex flex-col items-center gap-0.5">
              <button
                type="button"
                data-testid={`mini-day-${key}`}
                onClick={() => onSelect(day)}
                className={cn(
                  'mx-auto flex size-7 items-center justify-center rounded-full text-xs tabular-nums transition-colors hover:bg-accent',
                  !isSameMonth(day, displayMonth) && 'text-muted-foreground/50',
                  isToday(day) && !isSelected && 'font-semibold text-primary ring-1 ring-primary/40',
                  isSelected && 'bg-primary font-semibold text-primary-foreground hover:bg-primary'
                )}
              >
                {format(day, 'd')}
              </button>
              {density && (
                <div className="flex h-1 gap-0.5" data-testid={`mini-dots-${key}`} aria-hidden="true">
                  {Array.from({ length: dots }, (_, i) => (
                    // Fixed-count decorative dots, never reordered — index keys are safe here.
                    // biome-ignore lint/suspicious/noArrayIndexKey: dots have no identity beyond position
                    <span key={i} className="size-1 rounded-full bg-primary/70" />
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
