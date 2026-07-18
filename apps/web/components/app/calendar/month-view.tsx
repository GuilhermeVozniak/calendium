'use client';

import * as React from 'react';
import { format, isSameMonth, isToday } from 'date-fns';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

import { eventTouchesDay } from './time-grid';
import { FALLBACK_COLOR, sortByAllDayThenStart, withAlpha } from './event-render';
import { JoinButton } from './join-button';

const MAX_VISIBLE_PILLS = 3;
const WEEKDAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export interface MonthViewProps {
  anchor: Date;
  days: Date[];
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  onDayClick: (day: Date) => void;
  onEventClick: (event: Event) => void;
}

export function MonthView({ anchor, days, events, calendarById, onDayClick, onEventClick }: MonthViewProps) {
  return (
    <div className="flex flex-1 flex-col overflow-y-auto">
      <div className="grid grid-cols-7 border-b">
        {WEEKDAY_LABELS.map((label) => (
          <div
            key={label}
            className="px-2 py-1.5 text-center text-[11px] font-medium tracking-wide text-muted-foreground uppercase"
          >
            {label}
          </div>
        ))}
      </div>
      <div className="grid flex-1 auto-rows-fr grid-cols-7">
        {days.map((day) => (
          <MonthCell
            key={day.toISOString()}
            day={day}
            anchor={anchor}
            events={events}
            calendarById={calendarById}
            onDayClick={onDayClick}
            onEventClick={onEventClick}
          />
        ))}
      </div>
    </div>
  );
}

interface MonthCellProps {
  day: Date;
  anchor: Date;
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  onDayClick: (day: Date) => void;
  onEventClick: (event: Event) => void;
}

function MonthCell({ day, anchor, events, calendarById, onDayClick, onEventClick }: MonthCellProps) {
  const dayEvents = React.useMemo(
    () => events.filter((e) => eventTouchesDay(e, day)).sort(sortByAllDayThenStart),
    [events, day]
  );
  const visible = dayEvents.slice(0, MAX_VISIBLE_PILLS);
  const overflow = dayEvents.slice(MAX_VISIBLE_PILLS);
  const inMonth = isSameMonth(day, anchor);
  const today = isToday(day);

  return (
    // biome-ignore lint/a11y/useSemanticElements: CSS Grid month calendar, not a data table — role="gridcell" on a div is the correct ARIA pattern here.
    <div
      role="gridcell"
      tabIndex={0}
      data-outside-month={!inMonth}
      data-today={today}
      onClick={() => onDayClick(day)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') onDayClick(day);
      }}
      className={cn(
        'flex min-h-24 cursor-pointer flex-col gap-0.5 border-r border-b p-1 last:border-r-0 hover:bg-accent/40',
        !inMonth && 'bg-muted/30 text-muted-foreground'
      )}
    >
      <span
        className={cn(
          'flex size-6 items-center justify-center self-end rounded-full text-xs font-medium tabular-nums',
          today && 'bg-primary text-primary-foreground ring-2 ring-primary/40'
        )}
      >
        {format(day, 'd')}
      </span>
      <div className="flex flex-col gap-0.5">
        {visible.map((event) => {
          const color = calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR;
          return (
            <button
              key={event.id}
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onEventClick(event);
              }}
              title={event.title}
              className="truncate rounded px-1 py-0.5 text-left text-[10px] font-medium"
              style={{ backgroundColor: withAlpha(color, 0.18), color }}
            >
              {event.title}
            </button>
          );
        })}
        {overflow.length > 0 && (
          <Popover>
            <PopoverTrigger asChild>
              <button
                type="button"
                onClick={(e) => e.stopPropagation()}
                className="truncate rounded px-1 py-0.5 text-left text-[10px] font-medium text-muted-foreground hover:bg-accent"
              >
                +{overflow.length} more
              </button>
            </PopoverTrigger>
            <PopoverContent className="w-64 p-2" onClick={(e) => e.stopPropagation()}>
              <div className="mb-1 text-xs font-semibold">{format(day, 'EEEE, MMMM d')}</div>
              <div className="flex flex-col gap-0.5">
                {overflow.map((event) => {
                  const color = calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR;
                  return (
                    // biome-ignore lint/a11y/useSemanticElements: hosts a real <button> (JoinButton) inline — nesting a button inside a button is invalid HTML, so this outer element is a div with button semantics instead.
                    <div
                      key={event.id}
                      role="button"
                      tabIndex={0}
                      aria-label={event.title}
                      onClick={() => onEventClick(event)}
                      onKeyDown={(e) => {
                        if (e.target !== e.currentTarget) return;
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          onEventClick(event);
                        }
                      }}
                      className="flex cursor-pointer items-center justify-between gap-2 rounded px-1.5 py-1 text-left text-xs hover:bg-accent"
                    >
                      <span className="truncate" style={{ color }}>
                        {event.title}
                      </span>
                      <JoinButton event={event} size="sm" />
                    </div>
                  );
                })}
              </div>
            </PopoverContent>
          </Popover>
        )}
      </div>
    </div>
  );
}
