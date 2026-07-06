import type { Event as CalendarEvent } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { addDays, addWeeks, format, isSameDay, isToday, startOfWeek, subWeeks } from 'date-fns';
import { ChevronLeft, ChevronRight, Video } from 'lucide-react';
import { useMemo, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockCalendars, mockEvents } from '@/lib/mock';
import { cn } from '@/lib/utils';
import { Button } from '@/ui/button';
import { Tooltip } from '@/ui/tooltip';

export function CalendarView() {
  const [weekStart, setWeekStart] = useState(() => startOfWeek(new Date(), { weekStartsOn: 1 }));
  const from = weekStart.toISOString();
  const to = addDays(weekStart, 7).toISOString();

  const { data: events = [] } = useQuery({
    queryKey: ['events', from],
    queryFn: () =>
      orMock(
        () => api.listEvents(from, to),
        () => mockEvents(from, to)
      ),
  });
  const { data: calendars = [] } = useQuery({
    queryKey: ['calendars'],
    queryFn: () =>
      orMock(
        () => api.listCalendars(),
        () => mockCalendars
      ),
  });

  const colorByCalendar = useMemo(
    () => new Map(calendars.map((c) => [c.id, c.color])),
    [calendars]
  );
  const days = useMemo(() => Array.from({ length: 7 }, (_, i) => addDays(weekStart, i)), [weekStart]);

  const eventsForDay = (day: Date): CalendarEvent[] =>
    events
      .filter((e) => isSameDay(new Date(e.start), day))
      .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime());

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <h1 className="text-sm font-semibold">{format(weekStart, 'MMMM yyyy')}</h1>
        <span className="text-xs text-muted-foreground">
          Week of {format(weekStart, 'MMM d')}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <Tooltip label="Previous week">
            <Button
              variant="ghost"
              size="icon"
              aria-label="Previous week"
              onClick={() => setWeekStart((w) => subWeeks(w, 1))}
            >
              <ChevronLeft />
            </Button>
          </Tooltip>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setWeekStart(startOfWeek(new Date(), { weekStartsOn: 1 }))}
          >
            Today
          </Button>
          <Tooltip label="Next week">
            <Button
              variant="ghost"
              size="icon"
              aria-label="Next week"
              onClick={() => setWeekStart((w) => addWeeks(w, 1))}
            >
              <ChevronRight />
            </Button>
          </Tooltip>
        </div>
      </header>

      {/* Week strip: one column per day, events stacked chronologically. */}
      <div className="grid min-h-0 flex-1 grid-cols-7 divide-x overflow-y-auto">
        {days.map((day) => {
          const dayEvents = eventsForDay(day);
          const today = isToday(day);
          return (
            <div key={day.toISOString()} className="flex min-w-0 flex-col">
              <div
                className={cn(
                  'sticky top-0 z-10 flex select-none items-center gap-1.5 border-b bg-background px-2 py-1.5',
                  today && 'bg-accent'
                )}
              >
                <span className="text-[11px] font-medium uppercase text-muted-foreground">
                  {format(day, 'EEE')}
                </span>
                <span
                  className={cn(
                    'inline-flex size-5 items-center justify-center rounded-full text-xs font-semibold tabular-nums',
                    today && 'bg-primary text-primary-foreground'
                  )}
                >
                  {format(day, 'd')}
                </span>
              </div>
              <div className="flex flex-col gap-1 p-1.5">
                {dayEvents.length === 0 ? (
                  <span className="px-1 py-2 text-center text-[11px] text-muted-foreground/60">
                    —
                  </span>
                ) : (
                  dayEvents.map((event) => (
                    <div
                      key={event.id}
                      className="rounded-md border bg-card p-1.5 shadow-sm"
                      style={{
                        borderLeftWidth: 3,
                        borderLeftColor: colorByCalendar.get(event.calendarId) ?? 'hsl(var(--primary))',
                      }}
                    >
                      <div className="flex items-center gap-1 text-[11px] tabular-nums text-muted-foreground">
                        {format(new Date(event.start), 'HH:mm')}
                        {event.conferencing && <Video className="size-3" />}
                      </div>
                      <div className="truncate text-xs font-medium">{event.title}</div>
                    </div>
                  ))
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
