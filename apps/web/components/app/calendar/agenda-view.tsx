'use client';

import * as React from 'react';
import { addDays, format, isToday } from 'date-fns';
import { CalendarDays, MapPin, Video } from 'lucide-react';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { Kbd } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';

import { eventTouchesDay } from './time-grid';

const AGENDA_DAYS = 14;
const FALLBACK_COLOR = '#6366f1';

export interface AgendaViewProps {
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  from: Date;
  loading: boolean;
  onEventClick: (event: Event) => void;
}

export function AgendaView({ events, calendarById, from, loading, onEventClick }: AgendaViewProps) {
  const groups = React.useMemo(() => {
    const list: Array<{ day: Date; items: Event[] }> = [];
    for (let i = 0; i < AGENDA_DAYS; i++) {
      const day = addDays(from, i);
      const items = events
        .filter((e) => eventTouchesDay(e, day))
        .sort((a, b) => Number(b.allDay) - Number(a.allDay) || a.start.localeCompare(b.start));
      if (items.length > 0) list.push({ day, items });
    }
    return list;
  }, [events, from]);

  if (loading) {
    return (
      <div className="flex-1 space-y-3 overflow-y-auto p-6">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-6 w-40" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (groups.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
        <CalendarDays className="size-8 text-muted-foreground" />
        <p className="text-sm font-medium">No events in the next {AGENDA_DAYS} days</p>
        <p className="text-xs text-muted-foreground">
          Press <Kbd size="sm">C</Kbd> or use quick add to create one.
        </p>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl p-6">
        {groups.map(({ day, items }) => (
          <section key={day.toISOString()} className="mb-6">
            <h2 className={cn('mb-2 text-sm font-semibold', isToday(day) && 'text-primary')}>
              {isToday(day) ? 'Today' : format(day, 'EEEE, MMMM d')}
            </h2>
            <div className="overflow-hidden rounded-lg border">
              {items.map((event, i) => {
                const color = calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR;
                return (
                  <button
                    key={event.id}
                    type="button"
                    onClick={() => onEventClick(event)}
                    className={cn(
                      'flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent',
                      i > 0 && 'border-t'
                    )}
                  >
                    <span
                      className="h-8 w-1 shrink-0 rounded-full"
                      style={{ backgroundColor: color }}
                    />
                    <span className="w-32 shrink-0 text-xs text-muted-foreground tabular-nums">
                      {event.allDay
                        ? 'All day'
                        : `${format(new Date(event.start), 'h:mm a')} – ${format(new Date(event.end), 'h:mm a')}`}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-medium">{event.title}</span>
                      {event.location && (
                        <span className="mt-0.5 flex items-center gap-1 truncate text-xs text-muted-foreground">
                          <MapPin className="size-3 shrink-0" />
                          {event.location}
                        </span>
                      )}
                    </span>
                    {event.conferencing && (
                      <Video className="size-4 shrink-0 text-muted-foreground" />
                    )}
                  </button>
                );
              })}
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}
