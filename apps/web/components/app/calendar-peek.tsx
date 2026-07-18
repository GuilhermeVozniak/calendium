'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { addDays, addMinutes, format, isSameDay, startOfDay, startOfWeek } from 'date-fns';
import { CalendarDays, Plus, Video, X } from 'lucide-react';

import type { Event, EventInput } from '@calendium/shared';
import { detectConference, isJoinable } from '@calendium/shared';

import { EventDialog } from '@/components/app/event-dialog';
import { eventTouchesDay, TimeGrid } from '@/components/app/calendar/time-grid';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { fetchCalendars, fetchEvents } from '@/lib/calendar-data';
import { WEEK_OPTS } from '@/lib/calendar-views';
import { cn } from '@/lib/utils';

const STORAGE_KEY = 'calendium.calendarPeek';

/** Human label per detected conference provider — a small local stand-in for
 *  the shared `JoinButton` (Task 13), which hadn't landed yet when this panel
 *  was built. Replace this inline affordance with `<JoinButton>` once it's
 *  available; the provider taxonomy here intentionally matches its contract. */
const PROVIDER_LABEL: Record<string, string> = {
  meet: 'Google Meet',
  zoom: 'Zoom',
  teams: 'Teams',
  webex: 'Webex',
  other: 'video call',
};

/** Reads the persisted open/closed preference (SSR-safe; defaults closed). */
export function readStoredCalendarPeekOpen(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

interface DialogState {
  open: boolean;
  event: Event | null;
  defaults: Partial<EventInput> | null;
}

type PeekMode = 'day' | 'week';

export interface CalendarPeekProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * ~320px right-side panel for the mail route: today's single-day `TimeGrid`
 * (or, in Week mode, a 7-day strip above the selected day's grid), a
 * next-event card with a Join affordance, and a New event button. Controlled
 * by `open`/`onOpenChange` (toggled from the mail page's shortcut/palette);
 * persists its own open/closed preference to localStorage so it survives
 * reloads — read the initial value via `readStoredCalendarPeekOpen()`.
 */
export function CalendarPeek({ open, onOpenChange }: CalendarPeekProps) {
  const [mounted, setMounted] = React.useState(false);
  const [mode, setMode] = React.useState<PeekMode>('day');
  const [selectedDay, setSelectedDay] = React.useState<Date>(() => startOfDay(new Date()));
  const [now, setNow] = React.useState<Date>(() => new Date());
  const [dialog, setDialog] = React.useState<DialogState>({ open: false, event: null, defaults: null });

  React.useEffect(() => setMounted(true), []);

  React.useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(id);
  }, []);

  // Persist the open/closed preference. Runs even while collapsed so an
  // external close (e.g. a future toolbar button) is remembered too.
  React.useEffect(() => {
    try {
      window.localStorage.setItem(STORAGE_KEY, open ? '1' : '0');
    } catch {
      // localStorage unavailable (private mode, etc.) — peek still works
      // for this session.
    }
  }, [open]);

  const today = React.useMemo(() => startOfDay(now), [now]);
  const displayDay = mode === 'week' ? selectedDay : today;
  const weekStart = React.useMemo(() => startOfWeek(displayDay, WEEK_OPTS), [displayDay]);
  const weekDays = React.useMemo(
    () => Array.from({ length: 7 }, (_, i) => addDays(weekStart, i)),
    [weekStart]
  );

  // Fetch just the visible window — a single day, or the full week strip in
  // Week mode — using the same ['events', from, to] key shape as the
  // calendar page so both share the TanStack Query cache.
  const range = React.useMemo(
    () =>
      mode === 'week'
        ? { from: weekDays[0]!, to: addDays(weekDays[6]!, 1) }
        : { from: displayDay, to: addDays(displayDay, 1) },
    [mode, weekDays, displayDay]
  );

  const calendarsQuery = useQuery({
    queryKey: ['calendars'],
    queryFn: fetchCalendars,
    enabled: open,
  });
  const eventsQuery = useQuery({
    queryKey: ['events', range.from.toISOString(), range.to.toISOString()],
    queryFn: () => fetchEvents(range.from, range.to),
    enabled: open,
  });

  const calendars = React.useMemo(() => calendarsQuery.data ?? [], [calendarsQuery.data]);
  const calendarById = React.useMemo(() => new Map(calendars.map((c) => [c.id, c])), [calendars]);
  const events = React.useMemo(
    () =>
      (eventsQuery.data ?? []).filter(
        (e) => calendarById.get(e.calendarId)?.isVisible !== false && e.status !== 'cancelled'
      ),
    [eventsQuery.data, calendarById]
  );
  const dayEvents = React.useMemo(
    () => events.filter((e) => eventTouchesDay(e, displayDay)),
    [events, displayDay]
  );

  const nextEvent = React.useMemo(() => {
    return (
      events
        .filter((e) => !e.allDay && new Date(e.end) > now)
        .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime())[0] ?? null
    );
  }, [events, now]);

  const nextConference = nextEvent ? detectConference(nextEvent) : null;
  const nextJoinable =
    !!nextEvent && !!nextConference && isJoinable(now, new Date(nextEvent.start), new Date(nextEvent.end));

  const defaultCalendarId = React.useMemo(() => {
    const c =
      calendars.find((x) => x.isPrimary && x.canWrite) ??
      calendars.find((x) => x.canWrite) ??
      calendars[0];
    return c?.id;
  }, [calendars]);

  const openCreate = React.useCallback(
    (defaults?: Partial<EventInput>) => {
      setDialog({ open: true, event: null, defaults: { calendarId: defaultCalendarId, ...defaults } });
    },
    [defaultCalendarId]
  );

  const handleSlotClick = React.useCallback(
    (slotStart: Date) => {
      openCreate({ start: slotStart.toISOString(), end: addMinutes(slotStart, 30).toISOString() });
    },
    [openCreate]
  );

  const handleEventClick = React.useCallback(
    (event: Event) => setDialog({ open: true, event, defaults: null }),
    []
  );

  const gmtLabel = mounted ? format(now, 'O') : '';

  if (!open) return null;

  return (
    <aside
      data-testid="calendar-peek"
      className="hidden h-full w-80 shrink-0 flex-col border-l xl:flex"
    >
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <CalendarDays className="text-muted-foreground size-4" />
        <h2 className="text-sm font-semibold">Calendar</h2>
        <Tabs value={mode} onValueChange={(v) => setMode(v as PeekMode)} className="ml-auto">
          <TabsList className="h-7">
            <TabsTrigger value="day" className="px-2 text-xs">
              Day
            </TabsTrigger>
            <TabsTrigger value="week" className="px-2 text-xs">
              Week
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          onClick={() => onOpenChange(false)}
          aria-label="Close calendar peek"
        >
          <X className="size-4" />
        </Button>
      </div>

      {!mounted || calendarsQuery.isLoading || eventsQuery.isLoading ? (
        <div className="flex flex-1 flex-col gap-2 p-3">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-full w-full" />
        </div>
      ) : (
        <>
          {nextEvent && (
            <div data-testid="peek-next-event" className="shrink-0 border-b p-3">
              <p className="text-muted-foreground text-xs font-medium">Next up</p>
              <p className="truncate text-sm font-semibold">{nextEvent.title}</p>
              <p className="text-muted-foreground text-xs">
                {format(new Date(nextEvent.start), 'h:mm a')} –{' '}
                {format(new Date(nextEvent.end), 'h:mm a')}
              </p>
              {nextConference && nextJoinable && (
                <a
                  href={nextConference.url}
                  target="_blank"
                  rel="noreferrer"
                  className="bg-primary text-primary-foreground mt-2 inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium"
                >
                  <Video className="size-3.5" />
                  Join {PROVIDER_LABEL[nextConference.provider] ?? 'call'}
                </a>
              )}
            </div>
          )}

          {mode === 'week' && (
            <div className="flex shrink-0 gap-1 border-b p-2">
              {weekDays.map((day) => {
                const key = format(day, 'yyyy-MM-dd');
                const selected = isSameDay(day, selectedDay);
                return (
                  <button
                    key={key}
                    type="button"
                    data-testid={`peek-day-${key}`}
                    aria-pressed={selected}
                    onClick={() => setSelectedDay(day)}
                    className={cn(
                      'flex flex-1 flex-col items-center gap-0.5 rounded-lg px-1 py-1 text-center transition-colors hover:bg-accent',
                      selected && 'bg-primary text-primary-foreground hover:bg-primary'
                    )}
                  >
                    <span className="text-[10px] font-medium uppercase">{format(day, 'EEEEE')}</span>
                    <span className="text-xs font-semibold tabular-nums">{format(day, 'd')}</span>
                  </button>
                );
              })}
            </div>
          )}

          <TimeGrid
            days={[displayDay]}
            events={dayEvents}
            calendarById={calendarById}
            now={now}
            gmtLabel={gmtLabel}
            onSlotClick={handleSlotClick}
            onEventClick={handleEventClick}
          />
        </>
      )}

      <div className="shrink-0 border-t p-2">
        <Button size="sm" className="w-full" onClick={() => openCreate()}>
          <Plus />
          New event
        </Button>
      </div>

      <EventDialog
        open={dialog.open}
        onOpenChange={(next) => setDialog((d) => ({ ...d, open: next }))}
        calendars={calendars}
        event={dialog.event}
        defaults={dialog.defaults}
      />
    </aside>
  );
}
