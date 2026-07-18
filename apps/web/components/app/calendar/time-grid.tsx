'use client';

import * as React from 'react';
import { addDays, addMinutes, endOfDay, format, isSameDay, isToday, startOfDay } from 'date-fns';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { cn } from '@/lib/utils';

import { FALLBACK_COLOR, withAlpha } from './event-render';

const HOUR_HEIGHT = 48; // px per hour in the time grid

export function eventTouchesDay(event: Event, day: Date): boolean {
  return new Date(event.start) < endOfDay(day) && new Date(event.end) > startOfDay(day);
}

function hourLabel(hour: number): string {
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  return `${h12} ${hour < 12 ? 'AM' : 'PM'}`;
}

interface PositionedEvent {
  event: Event;
  top: number;
  height: number;
  leftPct: number;
  widthPct: number;
}

/** Absolute positions for one day column; overlapping events share columns. */
function layoutDayEvents(day: Date, events: Event[]): PositionedEvent[] {
  const dayStart = startOfDay(day).getTime();
  const nextDayStart = startOfDay(addDays(day, 1)).getTime();
  // Position events by local wall-clock time (hours + minutes), consistent with
  // the hour gridlines and the current-time indicator. Using elapsed ms since
  // midnight would draw events an hour off on a DST-transition day.
  const clockMinutes = (t: number) => {
    if (t <= dayStart) return 0;
    if (t >= nextDayStart) return 1440;
    const d = new Date(t);
    return d.getHours() * 60 + d.getMinutes() + d.getSeconds() / 60;
  };
  const items = events
    .map((event) => {
      const start = clockMinutes(new Date(event.start).getTime());
      return {
        event,
        start,
        end: Math.max(start + 15, clockMinutes(new Date(event.end).getTime())),
      };
    })
    .sort((a, b) => a.start - b.start || b.end - a.end);

  const positioned: PositionedEvent[] = [];
  let cluster: typeof items = [];
  let assignments: number[] = [];
  let columns: number[] = []; // end minute per column
  let clusterEnd = -1;

  const flush = () => {
    const colCount = Math.max(columns.length, 1);
    cluster.forEach((item, i) => {
      positioned.push({
        event: item.event,
        top: (item.start / 60) * HOUR_HEIGHT,
        height: ((item.end - item.start) / 60) * HOUR_HEIGHT,
        leftPct: (assignments[i] / colCount) * 100,
        widthPct: 100 / colCount,
      });
    });
    cluster = [];
    assignments = [];
    columns = [];
    clusterEnd = -1;
  };

  for (const item of items) {
    if (cluster.length > 0 && item.start >= clusterEnd) flush();
    let col = columns.findIndex((end) => end <= item.start);
    if (col === -1) {
      col = columns.length;
      columns.push(item.end);
    } else {
      columns[col] = item.end;
    }
    cluster.push(item);
    assignments.push(col);
    clusterEnd = Math.max(clusterEnd, item.end);
  }
  flush();
  return positioned;
}

// ---------------------------------------------------------------------------
// Time grid (day + week views)
// ---------------------------------------------------------------------------

export interface TimeGridProps {
  days: Date[];
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  now: Date;
  gmtLabel: string;
  onSlotClick: (start: Date) => void;
  onEventClick: (event: Event) => void;
}

export function TimeGrid({
  days,
  events,
  calendarById,
  now,
  gmtLabel,
  onSlotClick,
  onEventClick,
}: TimeGridProps) {
  const scrollRef = React.useRef<HTMLDivElement | null>(null);

  // Start the viewport around 7:30 AM, like every calendar app.
  React.useEffect(() => {
    scrollRef.current?.scrollTo({ top: HOUR_HEIGHT * 7.5 });
  }, []);

  const timed = React.useMemo(() => events.filter((e) => !e.allDay), [events]);
  const allDay = React.useMemo(() => events.filter((e) => e.allDay), [events]);
  const gridTemplateColumns = `repeat(${days.length}, minmax(0, 1fr))`;

  return (
    <div ref={scrollRef} className="relative flex-1 overflow-y-auto">
      {/* Sticky day header */}
      <div className="sticky top-0 z-30 border-b bg-background">
        <div className="flex">
          <div className="flex w-16 shrink-0 items-end justify-end pr-2 pb-1.5">
            <span className="text-[10px] font-medium text-muted-foreground tabular-nums">
              {gmtLabel}
            </span>
          </div>
          <div className="grid flex-1" style={{ gridTemplateColumns }}>
            {days.map((day) => {
              const dayAllDay = allDay.filter((e) => eventTouchesDay(e, day));
              return (
                <div key={day.toISOString()} className="border-l px-1.5 pt-2 pb-1.5 text-center">
                  <div className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
                    {format(day, 'EEE')}
                  </div>
                  <div
                    className={cn(
                      'mx-auto mt-0.5 flex size-7 items-center justify-center rounded-full text-sm font-semibold',
                      isToday(day) && 'bg-primary text-primary-foreground'
                    )}
                  >
                    {format(day, 'd')}
                  </div>
                  {dayAllDay.slice(0, 2).map((event) => {
                    const color = calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR;
                    return (
                      <button
                        key={event.id}
                        type="button"
                        onClick={() => onEventClick(event)}
                        className="mt-1 block w-full truncate rounded px-1.5 py-0.5 text-left text-[10px] font-medium"
                        style={{ backgroundColor: withAlpha(color, 0.18), color }}
                      >
                        {event.title}
                      </button>
                    );
                  })}
                  {dayAllDay.length > 2 && (
                    <div className="mt-0.5 text-[10px] text-muted-foreground">
                      +{dayAllDay.length - 2} more
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </div>

      {/* Grid body */}
      <div className="flex">
        {/* 24h time gutter */}
        <div className="relative w-16 shrink-0 select-none" style={{ height: 24 * HOUR_HEIGHT }}>
          {Array.from({ length: 23 }, (_, i) => i + 1).map((hour) => (
            <span
              key={hour}
              className="absolute right-2 -translate-y-1/2 text-[10px] text-muted-foreground tabular-nums"
              style={{ top: hour * HOUR_HEIGHT }}
            >
              {hourLabel(hour)}
            </span>
          ))}
        </div>
        <div className="grid flex-1" style={{ gridTemplateColumns }}>
          {days.map((day) => (
            <DayColumn
              key={day.toISOString()}
              day={day}
              events={timed}
              calendarById={calendarById}
              now={now}
              onSlotClick={onSlotClick}
              onEventClick={onEventClick}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

interface DayColumnProps {
  day: Date;
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  now: Date;
  onSlotClick: (start: Date) => void;
  onEventClick: (event: Event) => void;
}

function DayColumn({ day, events, calendarById, now, onSlotClick, onEventClick }: DayColumnProps) {
  const positioned = React.useMemo(
    () => layoutDayEvents(day, events.filter((e) => eventTouchesDay(e, day))),
    [day, events]
  );
  const showNow = isSameDay(day, now);
  const nowTop = ((now.getHours() * 60 + now.getMinutes()) / 60) * HOUR_HEIGHT;

  // Click an empty slot -> quick create, snapped to 30 minutes.
  const handleBackgroundClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const minutes = ((e.clientY - rect.top) / HOUR_HEIGHT) * 60;
    const snapped = Math.max(0, Math.min(23.5 * 60, Math.floor(minutes / 30) * 30));
    onSlotClick(addMinutes(startOfDay(day), snapped));
  };

  return (
    <div
      className="relative cursor-pointer border-l"
      style={{ height: 24 * HOUR_HEIGHT }}
      onClick={handleBackgroundClick}
    >
      {Array.from({ length: 24 }, (_, hour) => (
        <div
          key={hour}
          className="pointer-events-none absolute inset-x-0 border-t border-border/60"
          style={{ top: hour * HOUR_HEIGHT }}
        />
      ))}

      {positioned.map(({ event, top, height, leftPct, widthPct }) => {
        const color = calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR;
        return (
          <button
            key={event.id}
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onEventClick(event);
            }}
            className={cn(
              'absolute z-10 flex flex-col overflow-hidden rounded-md border-l-[3px] px-1.5 py-1 text-left leading-tight shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-ring',
              event.status === 'tentative' && 'opacity-70'
            )}
            style={{
              top: top + 1,
              height: Math.max(height - 2, 18),
              left: `calc(${leftPct}% + 1px)`,
              width: `calc(${widthPct}% - 3px)`,
              backgroundColor: withAlpha(color, 0.16),
              borderLeftColor: color,
            }}
          >
            <span className="truncate text-xs font-medium" style={{ color }}>
              {event.title}
            </span>
            {height >= 40 && (
              <span className="truncate text-[10px]" style={{ color: withAlpha(color, 0.75) }}>
                {format(new Date(event.start), 'h:mm')} –{' '}
                {format(new Date(event.end), 'h:mm a')}
                {event.conferencing ? ' · Meet' : ''}
              </span>
            )}
          </button>
        );
      })}

      {showNow && (
        <div className="pointer-events-none absolute inset-x-0 z-20" style={{ top: nowTop }}>
          <div className="relative h-0.5 bg-destructive">
            <span className="absolute top-1/2 -left-1 size-2 -translate-y-1/2 rounded-full bg-destructive" />
          </div>
        </div>
      )}
    </div>
  );
}

