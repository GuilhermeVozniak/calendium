'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { addMinutes, format } from 'date-fns';
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  Globe,
  Plus,
  X,
} from 'lucide-react';

import type { Calendar as CalendarModel, Event, EventInput } from '@calendium/shared';

import { AvailabilityDialog } from '@/components/app/availability';
import { DayTicker } from '@/components/app/calendar/day-ticker';
import { MiniMonth } from '@/components/app/calendar/mini-month';
import { MonthView } from '@/components/app/calendar/month-view';
import { QuarterView } from '@/components/app/calendar/quarter-view';
import { QuickAddBar } from '@/components/app/calendar/quick-add-bar';
import { TimeGrid } from '@/components/app/calendar/time-grid';
import { YearView } from '@/components/app/calendar/year-view';
import { EventDialog } from '@/components/app/event-dialog';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Kbd } from '@/components/ui/kbd';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { groupCalendarsByAccount } from '@/lib/calendar-accounts';
import { fetchBusyEvents, fetchCalendars, fetchEvents, patchCalendar } from '@/lib/calendar-data';
import type { CalendarView } from '@/lib/calendar-views';
import { rangeLabel, stepAnchor, viewRange } from '@/lib/calendar-views';
import { fetchAccounts } from '@/lib/settings-data';
import { getPinnedTimeZones, setPinnedTimeZones, zoneCaption } from '@/lib/timezones';

interface EventDialogState {
  open: boolean;
  event: Event | null;
  defaults: Partial<EventInput> | null;
}

export default function CalendarPage() {
  const queryClient = useQueryClient();
  const [mounted, setMounted] = React.useState(false);
  const [view, setView] = React.useState<CalendarView>('week');
  const [anchor, setAnchor] = React.useState<Date>(() => new Date());
  const [now, setNow] = React.useState<Date>(() => new Date());
  const [dialog, setDialog] = React.useState<EventDialogState>({
    open: false,
    event: null,
    defaults: null,
  });
  const [availabilityOpen, setAvailabilityOpen] = React.useState(false);

  // Pinned world-clock timezones for the day/week grid gutters. Persisted to
  // localStorage (lib/timezones.ts) rather than /v1/prefs, which today only
  // carries splitOrder — see that module's header comment.
  const [pinnedZones, setPinnedZones] = React.useState<string[]>([]);
  const [tzQuery, setTzQuery] = React.useState('');

  React.useEffect(() => setMounted(true), []);

  React.useEffect(() => {
    setPinnedZones(getPinnedTimeZones());
  }, []);

  const allTimeZones = React.useMemo<string[]>(() => {
    try {
      return Intl.supportedValuesOf('timeZone');
    } catch {
      return [];
    }
  }, []);

  const addPinnedZone = React.useCallback((zone: string) => {
    setPinnedZones((prev) => {
      if (prev.includes(zone) || prev.length >= 3) return prev;
      const next = [...prev, zone];
      setPinnedTimeZones(next);
      return next;
    });
    setTzQuery('');
  }, []);

  const removePinnedZone = React.useCallback((zone: string) => {
    setPinnedZones((prev) => {
      const next = prev.filter((z) => z !== zone);
      setPinnedTimeZones(next);
      return next;
    });
  }, []);

  const tzResults = React.useMemo(() => {
    const query = tzQuery.trim().toLowerCase();
    const candidates = allTimeZones.filter((z) => !pinnedZones.includes(z));
    const matched = query ? candidates.filter((z) => z.toLowerCase().includes(query)) : candidates;
    return matched.slice(0, 50);
  }, [allTimeZones, pinnedZones, tzQuery]);

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
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const eventsQuery = useQuery({
    queryKey: ['events', range.from.toISOString(), range.to.toISOString()],
    queryFn: () => fetchEvents(range.from, range.to),
  });
  // Unfiltered fetch (hidden calendars + every connected account included) so
  // quick-add slot suggestions see the true cross-account busy picture, not
  // just what's currently visible (Task 12 - cross-account conflict blocking).
  const busyEventsQuery = useQuery({
    queryKey: ['busy-events', range.from.toISOString(), range.to.toISOString()],
    queryFn: () => fetchBusyEvents(range.from, range.to),
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
  const accountGroups = React.useMemo(
    () => groupCalendarsByAccount(calendars, accountsQuery.data ?? []),
    [calendars, accountsQuery.data]
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

  // Navigation ---------------------------------------------------------------
  const goToday = React.useCallback(() => setAnchor(new Date()), []);
  const goPrev = React.useCallback(
    () => setAnchor((d) => stepAnchor(view, d, -1)),
    [view]
  );
  const goNext = React.useCallback(
    () => setAnchor((d) => stepAnchor(view, d, 1)),
    [view]
  );

  // Keyboard shortcuts: t (today), n/p or arrows (navigate), d/w/m/q/y/a (views), c (create).
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
        case 'm':
          setView('month');
          break;
        case 'q':
          setView('quarter');
          break;
        case 'y':
          setView('year');
          break;
        case 'a':
          setView('ticker');
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
        <h1 className="text-sm font-semibold tracking-tight">{rangeLabel(view, anchor)}</h1>
        <QuickAddBar
          calendars={calendars}
          events={busyEventsQuery.data ?? []}
          onCreate={openCreate}
        />
        <div className="ml-auto flex items-center gap-2">
          {(view === 'day' || view === 'week') && (
            <div className="flex items-center gap-1.5">
              {pinnedZones.map((zone) => {
                const { city, gmt } = zoneCaption(zone, now);
                return (
                  <span
                    key={zone}
                    className="inline-flex items-center gap-1 rounded-full border bg-muted/40 py-0.5 pr-1 pl-2 text-xs"
                  >
                    <span className="font-medium">{city}</span>
                    <span className="text-muted-foreground">{gmt}</span>
                    <button
                      type="button"
                      onClick={() => removePinnedZone(zone)}
                      aria-label={`Unpin ${zone}`}
                      className="rounded-full p-0.5 hover:bg-accent"
                    >
                      <X className="size-3" />
                    </button>
                  </span>
                );
              })}
              <Popover>
                <PopoverTrigger asChild>
                  <Button variant="outline" size="sm" disabled={pinnedZones.length >= 3}>
                    <Globe />+ TZ
                  </Button>
                </PopoverTrigger>
                <PopoverContent className="w-64 p-0" align="end">
                  <Command shouldFilter={false}>
                    <CommandInput
                      placeholder="Search timezone…"
                      value={tzQuery}
                      onValueChange={setTzQuery}
                    />
                    <CommandList>
                      <CommandEmpty>No matching timezone.</CommandEmpty>
                      <CommandGroup>
                        {tzResults.map((zone) => (
                          <CommandItem key={zone} value={zone} onSelect={() => addPinnedZone(zone)}>
                            {zone.replace(/_/g, ' ')}
                          </CommandItem>
                        ))}
                      </CommandGroup>
                    </CommandList>
                  </Command>
                </PopoverContent>
              </Popover>
            </div>
          )}
          <Tabs value={view} onValueChange={(v) => setView(v as CalendarView)}>
            <TabsList className="h-8">
              <TabsTrigger value="day" className="px-2.5 text-xs">
                Day
              </TabsTrigger>
              <TabsTrigger value="week" className="px-2.5 text-xs">
                Week
              </TabsTrigger>
              <TabsTrigger value="month" className="px-2.5 text-xs">
                Month
              </TabsTrigger>
              <TabsTrigger value="quarter" className="px-2.5 text-xs">
                Quarter
              </TabsTrigger>
              <TabsTrigger value="year" className="px-2.5 text-xs">
                Year
              </TabsTrigger>
              <TabsTrigger value="ticker" className="px-2.5 text-xs">
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
            <div className="flex flex-col gap-2.5">
              {accountGroups.map((group) => (
                <div key={group.accountId} className="flex flex-col gap-0.5">
                  {/* One heading per connected account (Task 12 - multi-account
                      overlay) so calendars from different Google/Microsoft
                      accounts are never confused for one another. */}
                  <h3
                    className="truncate px-2 text-[11px] font-medium text-muted-foreground"
                    title={group.email}
                  >
                    {group.email}
                  </h3>
                  {group.calendars.map((calendar) => (
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
              ))}
            </div>
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
          {view === 'ticker' ? (
            <DayTicker
              anchor={anchor}
              events={events}
              calendarById={calendarById}
              onAnchorChange={setAnchor}
              onEventClick={handleEventClick}
            />
          ) : view === 'month' ? (
            <MonthView
              anchor={anchor}
              days={range.days}
              events={events}
              calendarById={calendarById}
              onDayClick={(day) => {
                setAnchor(day);
                setView('day');
              }}
              onEventClick={handleEventClick}
            />
          ) : view === 'quarter' ? (
            <QuarterView
              from={range.from}
              to={range.to}
              events={events}
              onDayClick={(day) => {
                setAnchor(day);
                setView('day');
              }}
            />
          ) : view === 'year' ? (
            <YearView
              from={range.from}
              to={range.to}
              events={events}
              onDayClick={(day) => {
                setAnchor(day);
                setView('day');
              }}
            />
          ) : (
            <TimeGrid
              days={range.days}
              events={events}
              calendarById={calendarById}
              now={now}
              gmtLabel={gmtLabel}
              pinnedZones={pinnedZones}
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
