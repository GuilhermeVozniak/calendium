'use client';

import { addDays, format, isToday } from 'date-fns';
import { MapPin } from 'lucide-react';

import type { Calendar as CalendarModel, Event } from '@calendium/shared';

import { cn } from '@/lib/utils';

import { eventTouchesDay } from './time-grid';
import { FALLBACK_COLOR } from './event-render';
import { JoinButton } from './join-button';

const AGENDA_DAYS = 14;

export interface DayGroup {
  day: Date;
  items: Event[];
}

/**
 * Groups events into per-day buckets over a rolling window starting at
 * `from`, hiding days with no events. Shared by AgendaView and DayTicker (the
 * "hybrid list" reuses this same grouping engine so both agenda-style views
 * stay in sync).
 */
export function groupEventsByDay(events: Event[], from: Date, numDays: number = AGENDA_DAYS): DayGroup[] {
  const list: DayGroup[] = [];
  for (let i = 0; i < numDays; i++) {
    const day = addDays(from, i);
    const items = events
      .filter((e) => eventTouchesDay(e, day))
      .sort((a, b) => Number(b.allDay) - Number(a.allDay) || a.start.localeCompare(b.start));
    if (items.length > 0) list.push({ day, items });
  }
  return list;
}

export interface DayGroupListProps {
  groups: DayGroup[];
  calendarById: Map<string, CalendarModel>;
  onEventClick: (event: Event) => void;
  /** Registers/unregisters each day-section's DOM node, keyed by yyyy-MM-dd (for scroll-to-day). */
  sectionRef?: (key: string, node: HTMLElement | null) => void;
  className?: string;
}

/** Renders day-grouped event rows — the reusable core of the agenda engine. */
export function DayGroupList({ groups, calendarById, onEventClick, sectionRef, className }: DayGroupListProps) {
  return (
    <div className={cn('mx-auto max-w-3xl p-6', className)}>
      {groups.map(({ day, items }) => {
        const key = format(day, 'yyyy-MM-dd');
        return (
          <section key={key} ref={(node) => sectionRef?.(key, node)} className="mb-6">
            <h2 className={cn('mb-2 text-sm font-semibold', isToday(day) && 'text-primary')}>
              {isToday(day) ? 'Today' : format(day, 'EEEE, MMMM d')}
            </h2>
            <div className="overflow-hidden rounded-lg border">
              {items.map((event, i) => {
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
                    className={cn(
                      'flex w-full cursor-pointer items-center gap-3 px-4 py-2.5 text-left text-sm hover:bg-accent',
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
                    <JoinButton event={event} size="sm" />
                  </div>
                );
              })}
            </div>
          </section>
        );
      })}
    </div>
  );
}

/**
 * This file contains the shared agenda engine: groupEventsByDay and DayGroupList
 * are consumed by DayTicker and other views that need day-grouped event rendering.
 */
