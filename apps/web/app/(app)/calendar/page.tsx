'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  addDays,
  addMinutes,
  addMonths,
  endOfDay,
  endOfMonth,
  endOfWeek,
  format,
  isSameDay,
  isSameMonth,
  isToday,
  startOfDay,
  startOfMonth,
  startOfWeek,
} from 'date-fns';
import {
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Globe,
  MapPin,
  Plus,
  Sparkles,
  Video,
} from 'lucide-react';

import type { Calendar as CalendarModel, Event, EventInput } from '@calendium/shared';

import { AvailabilityDialog } from '@/components/app/availability';
import { EventDialog } from '@/components/app/event-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { fetchCalendars, fetchEvents, patchCalendar } from '@/lib/calendar-data';
import { parseQuickAdd } from '@/lib/quick-add';
import { cn } from '@/lib/utils';

const HOUR_HEIGHT = 48; // px per hour in the time grid
const WEEK_OPTS = { weekStartsOn: 0 as const };
const AGENDA_DAYS = 14;
const FALLBACK_COLOR = '#6366f1';

type CalendarView = 'day' | 'week' | 'agenda';

interface EventDialogState {
  open: boolean;
  event: Event | null;
  defaults: Partial<EventInput> | null;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function viewRange(view: CalendarView, anchor: Date): { from: Date; to: Date; days: Date[] } {
  if (view === 'day') {
    const from = startOfDay(anchor);
    return { from, to: endOfDay(anchor), days: [from] };
  }
  if (view === 'week') {
    const from = startOfWeek(anchor, WEEK_OPTS);
    return {
      from,
      to: endOfWeek(anchor, WEEK_OPTS),
      days: Array.from({ length: 7 }, (_, i) => addDays(from, i)),
    };
  }
  const from = startOfDay(anchor);
  return { from, to: endOfDay(addDays(from, AGENDA_DAYS - 1)), days: [] };
}

function eventTouchesDay(event: Event, day: Date): boolean {
  return (
    new Date(event.start) < endOfDay(day) && new Date(event.end) > startOfDay(day)
  );
}

function hourLabel(hour: number): string {
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  return `${h12} ${hour < 12 ? 'AM' : 'PM'}`;
}

function withAlpha(hex: string, alpha: number): string {
  const clean = hex.replace('#', '');
  const full = clean.length === 3 ? clean.split('').map((c) => c + c).join('') : clean;
  const n = parseInt(full, 16);
  if (Number.isNaN(n) || full.length !== 6) return hex;
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
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
// Page
// ---------------------------------------------------------------------------

export default function CalendarPage() {
  const queryClient = useQueryClient();
  const [mounted, setMounted] = React.useState(false);
  const [view, setView] = React.useState<CalendarView>('week');
  const [anchor, setAnchor] = React.useState<Date>(() => new Date());
  const [now, setNow] = React.useState<Date>(() => new Date());
  const [quickText, setQuickText] = React.useState('');
  const [dialog, setDialog] = React.useState<EventDialogState>({
    open: false,
    event: null,
    defaults: null,
  });
  const [availabilityOpen, setAvailabilityOpen] = React.useState(false);

  React.useEffect(() => setMounted(true), []);

  // ⌘K search results deep-link to a day via /calendar?d=<ISO>. Read it once
  // on mount from window.location — this page is client-only, and skipping
  // useSearchParams avoids wrapping the page in a Suspense boundary.
  React.useEffect(() => {
    const d = new URLSearchParams(window.location.search).get('d');
    if (!d) return;
    const parsed = new Date(d);
    if (!Number.isNaN(parsed.getTime())) {
      setAnchor(parsed);
      setView('day');
    }
  }, []);

  // Keep the current-time indicator moving.
  React.useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(id);
  }, []);

  const range = React.useMemo(() => viewRange(view, anchor), [view, anchor]);

  const calendarsQuery = useQuery({ queryKey: ['calendars'], queryFn: fetchCalendars });
  const eventsQuery = useQuery({
    queryKey: ['events', range.from.toISOString(), range.to.toISOString()],
    queryFn: () => fetchEvents(range.from, range.to),
  });

  const calendars = React.useMemo(
    () => calendarsQuery.data ?? [],
    [calendarsQuery.data]
  );
  const calendarById = React.useMemo(
    () => new Map(calendars.map((c) => [c.id, c])),
    [calendars]
  );
  const events = React.useMemo(
    () =>
      (eventsQuery.data ?? []).filter(
        (e) => calendarById.get(e.calendarId)?.isVisible !== false && e.status !== 'cancelled'
      ),
    [eventsQuery.data, calendarById]
  );

  const toggleCalendar = useMutation({
    mutationFn: ({ id, isVisible }: { id: string; isVisible: boolean }) =>
      patchCalendar(id, { isVisible }),
    onMutate: ({ id, isVisible }) => {
      queryClient.setQueryData<CalendarModel[]>(['calendars'], (prev) =>
        prev?.map((c) => (c.id === id ? { ...c, isVisible } : c))
      );
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ['calendars'] }),
  });

  const defaultCalendarId = React.useMemo(() => {
    const c =
      calendars.find((x) => x.isPrimary && x.canWrite) ??
      calendars.find((x) => x.canWrite) ??
      calendars[0];
    return c?.id;
  }, [calendars]);

  const openCreate = React.useCallback(
    (defaults?: Partial<EventInput>) => {
      setDialog({
        open: true,
        event: null,
        defaults: { calendarId: defaultCalendarId, ...defaults },
      });
    },
    [defaultCalendarId]
  );

  const handleSlotClick = React.useCallback(
    (slotStart: Date) => {
      openCreate({
        start: slotStart.toISOString(),
        end: addMinutes(slotStart, 30).toISOString(),
      });
    },
    [openCreate]
  );

  const handleEventClick = React.useCallback(
    (event: Event) => setDialog({ open: true, event, defaults: null }),
    []
  );

  const handleQuickAdd = () => {
    const parsed = parseQuickAdd(quickText);
    if (!parsed) return;
    openCreate({
      title: parsed.title,
      start: parsed.start.toISOString(),
      end: parsed.end.toISOString(),
      allDay: parsed.allDay,
    });
    setQuickText('');
  };

  // Navigation ---------------------------------------------------------------
  const step = view === 'day' ? 1 : 7;
  const goToday = React.useCallback(() => setAnchor(new Date()), []);
  const goPrev = React.useCallback(() => setAnchor((d) => addDays(d, -step)), [step]);
  const goNext = React.useCallback(() => setAnchor((d) => addDays(d, step)), [step]);

  // Keyboard shortcuts: t (today), n/p or arrows (navigate), d/w/a (views), c (create).
  React.useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      // Respect handlers that already consumed the key (e.g. the app-wide
      // compose shortcut) so a single keystroke never triggers two actions.
      if (e.defaultPrevented) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (target?.closest('input, textarea, select, [contenteditable="true"], [role="dialog"]')) {
        return;
      }
      switch (e.key) {
        case 't':
          goToday();
          break;
        case 'n':
        case 'ArrowRight':
          goNext();
          break;
        case 'p':
        case 'ArrowLeft':
          goPrev();
          break;
        case 'd':
          setView('day');
          break;
        case 'w':
          setView('week');
          break;
        case 'a':
          setView('agenda');
          break;
        case 'c':
          openCreate();
          break;
        default:
          return;
      }
      e.preventDefault();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [goToday, goNext, goPrev, openCreate]);

  const rangeLabel = React.useMemo(() => {
    if (view === 'day') return format(anchor, 'EEEE, MMMM d, yyyy');
    if (view === 'week') {
      const from = range.from;
      const to = addDays(range.from, 6);
      return isSameMonth(from, to)
        ? format(from, 'MMMM yyyy')
        : `${format(from, 'MMM d')} – ${format(to, 'MMM d, yyyy')}`;
    }
    return `${format(range.from, 'MMM d')} – ${format(range.to, 'MMM d, yyyy')}`;
  }, [view, anchor, range]);

  const timeZone = React.useMemo(() => {
    if (!mounted) return '';
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch {
      return 'UTC';
    }
  }, [mounted]);
  const gmtLabel = mounted ? format(now, 'O') : '';

  // Render only after mount: everything here is relative to the local clock,
  // so skipping SSR content avoids timezone hydration mismatches.
  if (!mounted) {
    return (
      <div className="flex h-full flex-1">
        <div className="hidden w-60 shrink-0 border-r p-4 lg:block">
          <Skeleton className="h-64 w-full" />
        </div>
        <div className="flex-1 p-4">
          <Skeleton className="h-full min-h-96 w-full" />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col">
      {/* Toolbar */}
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b px-4 py-2.5">
        <div className="flex items-center gap-1">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" size="sm" onClick={goToday}>
                Today
              </Button>
            </TooltipTrigger>
            <TooltipContent className="flex items-center gap-1.5">
              Jump to today <Kbd size="sm">T</Kbd>
            </TooltipContent>
          </Tooltip>
          <Button variant="ghost" size="icon" className="size-8" onClick={goPrev} aria-label="Previous">
            <ChevronLeft />
          </Button>
          <Button variant="ghost" size="icon" className="size-8" onClick={goNext} aria-label="Next">
            <ChevronRight />
          </Button>
        </div>
        <h1 className="text-sm font-semibold tracking-tight">{rangeLabel}</h1>
        <div className="relative min-w-52 max-w-md flex-1">
          <Sparkles className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={quickText}
            onChange={(e) => setQuickText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleQuickAdd();
            }}
            placeholder='Quick add — try "lunch with Ana tomorrow 12:30-1:30"'
            aria-label="Quick add event"
            className="h-8 pl-8 text-sm"
          />
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Tabs value={view} onValueChange={(v) => setView(v as CalendarView)}>
            <TabsList className="h-8">
              <TabsTrigger value="day" className="px-2.5 text-xs">
                Day
              </TabsTrigger>
              <TabsTrigger value="week" className="px-2.5 text-xs">
                Week
              </TabsTrigger>
              <TabsTrigger value="agenda" className="px-2.5 text-xs">
                Agenda
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <Button variant="outline" size="sm" onClick={() => setAvailabilityOpen(true)}>
            <Clock />
            Share availability
          </Button>
          <Button size="sm" onClick={() => openCreate()}>
            <Plus />
            New event
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {/* Left rail */}
        <aside className="hidden w-60 shrink-0 flex-col gap-5 overflow-y-auto border-r p-4 lg:flex">
          <MiniMonth selected={anchor} onSelect={setAnchor} />
          <div className="flex flex-col gap-0.5">
            <h2 className="px-2 pb-1 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
              Calendars
            </h2>
            {calendarsQuery.isLoading && (
              <div className="space-y-2 px-2">
                <Skeleton className="h-5 w-full" />
                <Skeleton className="h-5 w-3/4" />
              </div>
            )}
            {calendars.map((calendar) => (
              <button
                key={calendar.id}
                type="button"
                aria-pressed={calendar.isVisible}
                onClick={() =>
                  toggleCalendar.mutate({ id: calendar.id, isVisible: !calendar.isVisible })
                }
                className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent"
              >
                <span
                  className="flex size-4 shrink-0 items-center justify-center rounded-[4px] border"
                  style={
                    calendar.isVisible
                      ? { backgroundColor: calendar.color, borderColor: calendar.color }
                      : { borderColor: calendar.color }
                  }
                >
                  {calendar.isVisible && <Check className="size-3 text-white" />}
                </span>
                <span className="truncate">{calendar.name}</span>
                {calendar.isPrimary && (
                  <span className="ml-auto text-[10px] text-muted-foreground uppercase">
                    primary
                  </span>
                )}
              </button>
            ))}
          </div>
          <div className="mt-auto flex items-center gap-2 border-t pt-3 text-xs text-muted-foreground">
            <Globe className="size-3.5 shrink-0" />
            <span className="truncate" title={timeZone}>
              {timeZone}
            </span>
            <span className="ml-auto shrink-0 tabular-nums">{gmtLabel}</span>
          </div>
        </aside>

        {/* Main view */}
        <main className="flex min-h-0 flex-1 flex-col">
          {view === 'agenda' ? (
            <AgendaView
              events={events}
              calendarById={calendarById}
              from={range.from}
              loading={eventsQuery.isLoading}
              onEventClick={handleEventClick}
            />
          ) : (
            <TimeGrid
              days={range.days}
              events={events}
              calendarById={calendarById}
              now={now}
              gmtLabel={gmtLabel}
              onSlotClick={handleSlotClick}
              onEventClick={handleEventClick}
            />
          )}
        </main>
      </div>

      <EventDialog
        open={dialog.open}
        onOpenChange={(open) => setDialog((d) => ({ ...d, open }))}
        calendars={calendars}
        event={dialog.event}
        defaults={dialog.defaults}
      />
      <AvailabilityDialog open={availabilityOpen} onOpenChange={setAvailabilityOpen} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Mini month picker
// ---------------------------------------------------------------------------

function MiniMonth({ selected, onSelect }: { selected: Date; onSelect: (day: Date) => void }) {
  const [month, setMonth] = React.useState(() => startOfMonth(selected));
  React.useEffect(() => setMonth(startOfMonth(selected)), [selected]);

  const days = React.useMemo(() => {
    const list: Date[] = [];
    const end = endOfWeek(endOfMonth(month), WEEK_OPTS);
    for (let d = startOfWeek(startOfMonth(month), WEEK_OPTS); d <= end; d = addDays(d, 1)) {
      list.push(d);
    }
    return list;
  }, [month]);

  return (
    <div>
      <div className="mb-2 flex items-center justify-between px-1">
        <span className="text-sm font-semibold">{format(month, 'MMMM yyyy')}</span>
        <div className="flex gap-0.5">
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => setMonth((m) => addMonths(m, -1))}
            aria-label="Previous month"
          >
            <ChevronLeft className="size-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => setMonth((m) => addMonths(m, 1))}
            aria-label="Next month"
          >
            <ChevronRight className="size-3.5" />
          </Button>
        </div>
      </div>
      <div className="grid grid-cols-7 gap-y-0.5 text-center">
        {['S', 'M', 'T', 'W', 'T', 'F', 'S'].map((label, i) => (
          <span key={`${label}-${i}`} className="text-[10px] font-medium text-muted-foreground">
            {label}
          </span>
        ))}
        {days.map((day) => {
          const isSelected = isSameDay(day, selected);
          return (
            <button
              key={day.toISOString()}
              type="button"
              onClick={() => onSelect(day)}
              className={cn(
                'mx-auto flex size-7 items-center justify-center rounded-full text-xs tabular-nums transition-colors hover:bg-accent',
                !isSameMonth(day, month) && 'text-muted-foreground/50',
                isToday(day) && !isSelected && 'font-semibold text-primary ring-1 ring-primary/40',
                isSelected && 'bg-primary font-semibold text-primary-foreground hover:bg-primary'
              )}
            >
              {format(day, 'd')}
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Time grid (day + week views)
// ---------------------------------------------------------------------------

interface TimeGridProps {
  days: Date[];
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  now: Date;
  gmtLabel: string;
  onSlotClick: (start: Date) => void;
  onEventClick: (event: Event) => void;
}

function TimeGrid({ days, events, calendarById, now, gmtLabel, onSlotClick, onEventClick }: TimeGridProps) {
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

// ---------------------------------------------------------------------------
// Agenda view
// ---------------------------------------------------------------------------

interface AgendaViewProps {
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  from: Date;
  loading: boolean;
  onEventClick: (event: Event) => void;
}

function AgendaView({ events, calendarById, from, loading, onEventClick }: AgendaViewProps) {
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
