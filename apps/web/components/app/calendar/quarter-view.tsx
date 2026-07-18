'use client';

import * as React from 'react';
import { addMonths, format, isSameMonth } from 'date-fns';

import type { Event } from '@calendium/shared';

import { cn } from '@/lib/utils';

import { MiniMonth } from './mini-month';
import { buildDensityMap } from './time-grid';

export interface QuarterViewProps {
  from: Date;
  to: Date;
  events: Event[];
  onDayClick: (day: Date) => void;
}

/**
 * Three-month overview: `MiniMonth` grids for the anchor's quarter, each
 * showing a 0-3 intensity dot per day instead of individual events (too
 * coarse a zoom level to render events directly). Clicking any day drills
 * down to day view at that date.
 */
export function QuarterView({ from, to, events, onDayClick }: QuarterViewProps) {
  const months = React.useMemo(
    () => [0, 1, 2].map((i) => addMonths(from, i)),
    [from]
  );
  const density = React.useMemo(
    () => buildDensityMap(events, from, to),
    [events, from, to]
  );

  return (
    <div className="flex flex-1 flex-col overflow-y-auto p-4">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {months.map((month) => {
          const current = isSameMonth(month, new Date());
          return (
            <div
              key={month.toISOString()}
              data-testid={`quarter-month-${format(month, 'yyyy-MM')}`}
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
