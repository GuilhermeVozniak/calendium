'use client';

import * as React from 'react';
import { addDays, format, isSameDay, isToday } from 'date-fns';
import { CalendarDays } from 'lucide-react';

import type { Calendar as CalendarModel, DayForecast, Event } from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { WeatherChip } from '@/components/app/calendar/weather-chip';
import { Kbd } from '@/components/ui/kbd';
import { TICKER_DAYS } from '@/lib/calendar-views';
import { cn } from '@/lib/utils';

import { DayGroupList, groupEventsByDay } from './agenda-view';

/** Strip window: days shown before/after the anchor, roughly centering it. */
const STRIP_DAYS = 14;
const STRIP_DAYS_BEFORE = 6;

export interface DayTickerProps {
  anchor: Date;
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  onAnchorChange: (day: Date) => void;
  onEventClick: (event: Event) => void;
  /** Day-keyed (yyyy-MM-dd) forecasts (M2.8 Task 13); absent → no weather chips. */
  weatherByDate?: Map<string, DayForecast>;
}

/**
 * Fantastical-style hybrid: a horizontally scrollable day strip above a
 * grouped agenda list (reusing AgendaView's grouping + row-rendering engine)
 * that auto-scrolls to whichever day is selected in the strip.
 */
export function DayTicker({ anchor, events, calendarById, onAnchorChange, onEventClick, weatherByDate }: DayTickerProps) {
  const stripDays = React.useMemo(
    () => Array.from({ length: STRIP_DAYS }, (_, i) => addDays(anchor, i - STRIP_DAYS_BEFORE)),
    [anchor]
  );
  const groups = React.useMemo(() => groupEventsByDay(events, anchor, TICKER_DAYS), [events, anchor]);

  const sectionNodes = React.useRef(new Map<string, HTMLElement>());
  const registerSection = React.useCallback((key: string, node: HTMLElement | null) => {
    if (node) sectionNodes.current.set(key, node);
    else sectionNodes.current.delete(key);
  }, []);

  const selectedKey = format(anchor, 'yyyy-MM-dd');
  React.useEffect(() => {
    sectionNodes.current.get(selectedKey)?.scrollIntoView({ block: 'start' });
  }, [selectedKey, groups]);

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <div className="flex gap-1 overflow-x-auto border-b p-2">
        {stripDays.map((day) => {
          const key = format(day, 'yyyy-MM-dd');
          const selected = isSameDay(day, anchor);
          const today = isToday(day);
          return (
            <button
              key={key}
              type="button"
              data-testid={`ticker-day-${key}`}
              aria-pressed={selected}
              onClick={() => onAnchorChange(day)}
              className={cn(
                'flex w-12 shrink-0 flex-col items-center gap-0.5 rounded-lg px-1 py-1.5 text-center transition-colors hover:bg-accent',
                selected && 'bg-primary text-primary-foreground hover:bg-primary'
              )}
            >
              <span
                className={cn(
                  'text-[10px] font-medium uppercase',
                  selected ? 'text-primary-foreground/80' : 'text-muted-foreground'
                )}
              >
                {format(day, 'EEEEE')}
              </span>
              <span className="text-sm font-semibold tabular-nums">{format(day, 'd')}</span>
              <WeatherChip
                compact
                forecast={weatherByDate?.get(key)}
                className={selected ? 'text-primary-foreground/80' : undefined}
              />
              {today && (
                <Badge
                  variant={selected ? 'secondary' : 'default'}
                  className="h-3.5 px-1 text-[8px] leading-none"
                >
                  Today
                </Badge>
              )}
            </button>
          );
        })}
      </div>

      {groups.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
          <CalendarDays className="size-8 text-muted-foreground" />
          <p className="text-sm font-medium">No events in the next {TICKER_DAYS} days</p>
          <p className="text-xs text-muted-foreground">
            Press <Kbd size="sm">C</Kbd> or use quick add to create one.
          </p>
        </div>
      ) : (
        <div className="flex-1 overflow-y-auto">
          <DayGroupList
            groups={groups}
            calendarById={calendarById}
            onEventClick={onEventClick}
            sectionRef={registerSection}
          />
        </div>
      )}
    </div>
  );
}
