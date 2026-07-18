'use client';

import * as React from 'react';
import { addMonths, format, isSameMonth, startOfQuarter } from 'date-fns';

import type { Event } from '@calendium/shared';

import { cn } from '@/lib/utils';

import { MiniMonth } from './mini-month';
import { buildDensityMap } from './time-grid';

export interface QuarterViewProps {
  anchor: Date;
  events: Event[];
  onDayClick: (day: Date) => void;
}

/**
 * Three-month overview: `MiniMonth` grids for the anchor's quarter, each
 * showing a 0-3 intensity dot per day instead of individual events (too
 * coarse a zoom level to render events directly). Clicking any day drills
 * down to day view at that date.
 */
export function QuarterView({ anchor, events, onDayClick }: QuarterViewProps) {
  const quarterStart = React.useMemo(() => startOfQuarter(anchor), [anchor]);
  const months = React.useMemo(
    () => [0, 1, 2].map((i) => addMonths(quarterStart, i)),
    [quarterStart]
  );
  const density = React.useMemo(
    () => buildDensityMap(events, quarterStart, addMonths(quarterStart, 3)),
    [events, quarterStart]
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
