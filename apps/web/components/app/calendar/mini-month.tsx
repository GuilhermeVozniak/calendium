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

export interface MiniMonthProps {
  selected: Date;
  onSelect: (day: Date) => void;
}

export function MiniMonth({ selected, onSelect }: MiniMonthProps) {
  const [month, setMonth] = React.useState(() => startOfMonth(selected));
  React.useEffect(() => setMonth(startOfMonth(selected)), [selected]);

  const days = React.useMemo(() => {
    const list: Date[] = [];
    const end = endOfWeek(endOfMonth(month), WEEK_OPTS);
    for (let d = startOfWeek(startOfMonth(month), WEEK_OPTS); d <= end; d = addDays(d, 1)) {
      list.push(d);
    }
    return list;
  }, [month]);

  return (
    <div>
      <div className="mb-2 flex items-center justify-between px-1">
        <span className="text-sm font-semibold">{format(month, 'MMMM yyyy')}</span>
        <div className="flex gap-0.5">
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => setMonth((m) => addMonths(m, -1))}
            aria-label="Previous month"
          >
            <ChevronLeft className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => setMonth((m) => addMonths(m, 1))}
            aria-label="Next month"
          >
            <ChevronRight className="size-3.5" />
          </Button>
        </div>
      </div>
      <div className="grid grid-cols-7 gap-y-0.5 text-center">
        {['S', 'M', 'T', 'W', 'T', 'F', 'S'].map((label, i) => (
          <span key={`${label}-${i}`} className="text-[10px] font-medium text-muted-foreground">
            {label}
          </span>
        ))}
        {days.map((day) => {
          const isSelected = isSameDay(day, selected);
          return (
            <button
              key={day.toISOString()}
              type="button"
              onClick={() => onSelect(day)}
              className={cn(
                'mx-auto flex size-7 items-center justify-center rounded-full text-xs tabular-nums transition-colors hover:bg-accent',
                !isSameMonth(day, month) && 'text-muted-foreground/50',
                isToday(day) && !isSelected && 'font-semibold text-primary ring-1 ring-primary/40',
                isSelected && 'bg-primary font-semibold text-primary-foreground hover:bg-primary'
              )}
            >
              {format(day, 'd')}
            </button>
          );
        })}
      </div>
    </div>
  );
}
