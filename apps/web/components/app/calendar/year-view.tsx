'use client';

import * as React from 'react';
import { addMonths, format, isSameMonth, startOfYear } from 'date-fns';

import type { Event } from '@calendium/shared';

import { cn } from '@/lib/utils';

import { MiniMonth } from './mini-month';
import { buildDensityMap } from './time-grid';

export interface YearViewProps {
  anchor: Date;
  events: Event[];
  onDayClick: (day: Date) => void;
}

/**
 * Twelve-month overview: `MiniMonth` grids for the anchor's year, each
 * showing a 0-3 intensity dot per day instead of individual events (too
 * coarse a zoom level to render events directly). Clicking any day drills
 * down to day view at that date.
 */
export function YearView({ anchor, events, onDayClick }: YearViewProps) {
  const yearStart = React.useMemo(() => startOfYear(anchor), [anchor]);
  const months = React.useMemo(
    () => Array.from({ length: 12 }, (_, i) => addMonths(yearStart, i)),
    [yearStart]
  );
  const density = React.useMemo(
    () => buildDensityMap(events, yearStart, addMonths(yearStart, 12)),
    [events, yearStart]
  );

  return (
    <div className="flex flex-1 flex-col overflow-y-auto p-4">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
        {months.map((month) => {
          const current = isSameMonth(month, new Date());
          return (
            <div
              key={month.toISOString()}
              data-testid={`year-month-${format(month, 'yyyy-MM')}`}
              data-current-month={current}
              className={cn('rounded-lg border p-3', current && 'border-primary ring-1 ring-primary/40')}
            >
              <MiniMonth selected={null} onSelect={onDayClick} month={month} density={density} />
            </div>
          );
        })}
      </div>
    </div>
  );
}
