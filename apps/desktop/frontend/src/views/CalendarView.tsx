import type {
  Calendar,
  ConferenceProvider,
  Event as CalendarEvent,
  EventInput,
  EventPatch,
  RsvpStatus,
} from '@calendium/shared';
import { detectConference, findConflicts, isJoinable, toBusyIntervals } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  addDays,
  addMonths,
  eachDayOfInterval,
  endOfMonth,
  format,
  isSameDay,
  isSameMonth,
  isToday,
  startOfDay,
  startOfMonth,
  startOfWeek,
  subMonths,
} from 'date-fns';
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Loader2,
  Plus,
  Trash2,
  TriangleAlert,
  Video,
  X,
} from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockCalendars, mockEvents, mockUser } from '@/lib/mock';
import { isDemoMode } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Button } from '@/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/ui/dialog';
import { Input } from '@/ui/input';

const FOCUS_DATE_EVENT = 'calendium:focus-date';
const WEEK_OPTS = { weekStartsOn: 1 as const };

export type CalendarViewMode = 'day' | 'week' | 'month';

/** Jumps the calendar to the date containing a date (⌘K event results). */
export function emitFocusDate(iso: string) {
  window.dispatchEvent(new CustomEvent<string>(FOCUS_DATE_EVENT, { detail: iso }));
}

/** ISO -> value for <input type="datetime-local"> (local wall-clock time). */
function toLocalInput(iso: string): string {
  return format(new Date(iso), "yyyy-MM-dd'T'HH:mm");
}
function fromLocalInput(value: string): string {
  return new Date(value).toISOString();
}

const fieldClass =
  'flex h-8 w-full rounded-md border border-input bg-transparent px-2.5 py-1 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';

const PROVIDER_LABEL: Record<ConferenceProvider, string> = {
  meet: 'Meet',
  zoom: 'Zoom',
  teams: 'Teams',
  webex: 'Webex',
  other: 'call',
};

/**
 * One-click join affordance — desktop parity with web's JoinButton (Task 13).
 * Renders nothing when no conference link is detected on the event (checks
 * the structured `conferencing` field, then `location`, then `description`,
 * via the shared `detectConference`). Solid/primary from 5 minutes before
 * start until the event ends (`isJoinable`); a quieter ghost style outside
 * that window.
 */
function JoinButton({
  event,
  now,
  size = 'default',
}: {
  event: CalendarEvent;
  now?: Date;
  size?: 'sm' | 'default';
}) {
  const conference = detectConference(event);
  if (!conference) return null;
  const joinable = isJoinable(now ?? new Date(), new Date(event.start), new Date(event.end));
  return (
    <Button
      type="button"
      variant={joinable ? 'default' : 'ghost'}
      size={size}
      data-joinable={joinable}
      onClick={(e) => {
        e.stopPropagation();
        window.open(conference.url, '_blank', 'noopener,noreferrer');
      }}
    >
      <Video /> Join {PROVIDER_LABEL[conference.provider]}
    </Button>
  );
}

/**
 * Date-range math for the three supported views. Intentionally duplicated
 * from apps/web/lib/calendar-views.ts's `viewRange` (the month arithmetic is
 * ~15 lines of date-fns) rather than imported — the desktop frontend does
 * not depend on apps/web.
 */
function viewRange(view: CalendarViewMode, anchor: Date): { from: Date; to: Date; days: Date[] } {
  switch (view) {
    case 'day': {
      const from = startOfDay(anchor);
      return { from, to: addDays(from, 1), days: [from] };
    }
    case 'week': {
      const from = startOfWeek(anchor, WEEK_OPTS);
      return {
        from,
        to: addDays(from, 7),
        days: Array.from({ length: 7 }, (_, i) => addDays(from, i)),
      };
    }
    case 'month': {
      // Full leading/trailing weeks: 4-6 rows x 7, always whole weeks.
      const from = startOfWeek(startOfMonth(anchor), WEEK_OPTS);
      const to = addDays(startOfWeek(endOfMonth(anchor), WEEK_OPTS), 7);
      return { from, to, days: eachDayOfInterval({ start: from, end: addDays(to, -1) }) };
    }
  }
}

/** j/k step per view: day +/-1d, week +/-7d, month +/-1 month. */
function stepAnchor(view: CalendarViewMode, anchor: Date, dir: 1 | -1): Date {
  switch (view) {
    case 'day':
      return addDays(anchor, dir);
    case 'week':
      return addDays(anchor, dir * 7);
    case 'month':
      return dir === 1 ? addMonths(anchor, 1) : subMonths(anchor, 1);
  }
}

function rangeLabel(view: CalendarViewMode, anchor: Date): string {
  if (view === 'month') return format(anchor, 'MMMM yyyy');
  if (view === 'day') return format(anchor, 'EEEE, MMM d');
  return `Week of ${format(startOfWeek(anchor, WEEK_OPTS), 'MMM d')}`;
}

export function CalendarView() {
  const queryClient = useQueryClient();
  const [view, setView] = useState<CalendarViewMode>('week');
  const [anchor, setAnchor] = useState(() => new Date());
  const [editing, setEditing] = useState<CalendarEvent | null>(null);
  const [creating, setCreating] = useState(false);

  const { from, to, days } = useMemo(() => viewRange(view, anchor), [view, anchor]);
  const fromIso = from.toISOString();
  const toIso = to.toISOString();

  const {
    data: events = [],
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['events', fromIso, toIso],
    queryFn: () =>
      orMock(
        () => api.listEvents(fromIso, toIso),
        () => mockEvents(fromIso, toIso)
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

  useEffect(() => {
    const onFocus = (e: Event) => {
      const iso = (e as CustomEvent<string>).detail;
      setAnchor(new Date(iso));
    };
    window.addEventListener(FOCUS_DATE_EVENT, onFocus);
    return () => window.removeEventListener(FOCUS_DATE_EVENT, onFocus);
  }, []);

  // d/w/m switch the view, t jumps to today, j/k step the anchor by one
  // view-unit — mirrors the app's existing global-shortcut pattern (App.tsx's
  // ⌘K/compose handling): ignore modifier chords and typing targets so this
  // never fights with a form field.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      ) {
        return;
      }
      switch (e.key.toLowerCase()) {
        case 'd':
          setView('day');
          break;
        case 'w':
          setView('week');
          break;
        case 'm':
          setView('month');
          break;
        case 't':
          setAnchor(new Date());
          break;
        case 'j':
          setAnchor((a) => stepAnchor(view, a, 1));
          break;
        case 'k':
          setAnchor((a) => stepAnchor(view, a, -1));
          break;
        default:
          return;
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [view]);

  const colorByCalendar = useMemo(
    () => new Map(calendars.map((c) => [c.id, c.color])),
    [calendars]
  );

  const eventsForDay = (day: Date): CalendarEvent[] =>
    events
      .filter((e) => isSameDay(new Date(e.start), day))
      .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime());

  function closeDialog() {
    setCreating(false);
    setEditing(null);
  }

  function goToDay(day: Date) {
    setAnchor(day);
    setView('day');
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <h1 className="text-sm font-semibold">{rangeLabel(view, anchor)}</h1>
        <div className="ml-auto flex items-center gap-1">
          <div className="mr-1 flex items-center gap-0.5 rounded-md border p-0.5">
            {(['day', 'week', 'month'] as const).map((v) => (
              <Button
                key={v}
                variant={view === v ? 'secondary' : 'ghost'}
                size="sm"
                aria-pressed={view === v}
                onClick={() => setView(v)}
              >
                {v === 'day' ? 'Day' : v === 'week' ? 'Week' : 'Month'}
              </Button>
            ))}
          </div>
          <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
            <Plus /> New event
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Previous"
            onClick={() => setAnchor((a) => stepAnchor(view, a, -1))}
          >
            <ChevronLeft />
          </Button>
          <Button variant="outline" size="sm" onClick={() => setAnchor(new Date())}>
            Today
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Next"
            onClick={() => setAnchor((a) => stepAnchor(view, a, 1))}
          >
            <ChevronRight />
          </Button>
        </div>
      </header>

      {isError ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
          <p className="text-sm font-medium">Couldn't load your calendar</p>
          <p className="text-xs text-muted-foreground">{errorMessage(error)}</p>
          <Button variant="outline" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        </div>
      ) : view === 'month' ? (
        <MonthGrid
          anchor={anchor}
          days={days}
          events={events}
          onDayClick={goToDay}
          onEventClick={setEditing}
        />
      ) : (
        <div
          className={cn(
            'grid min-h-0 flex-1 divide-x overflow-y-auto',
            view === 'day' ? 'grid-cols-1' : 'grid-cols-7'
          )}
        >
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
                      // biome-ignore lint/a11y/useSemanticElements: hosts a real <button> (JoinButton) inline — nesting a button inside a button is invalid HTML, so this is a div with button semantics instead.
                      <div
                        key={event.id}
                        role="button"
                        tabIndex={0}
                        onClick={() => setEditing(event)}
                        onKeyDown={(e) => {
                          if (e.target !== e.currentTarget) return;
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            setEditing(event);
                          }
                        }}
                        className="w-full cursor-pointer rounded-md border bg-card p-1.5 text-left shadow-sm transition-colors hover:bg-accent"
                        style={{
                          borderLeftWidth: 3,
                          borderLeftColor:
                            colorByCalendar.get(event.calendarId) ?? 'hsl(var(--primary))',
                        }}
                      >
                        <div className="flex items-center gap-1 text-[11px] tabular-nums text-muted-foreground">
                          {format(new Date(event.start), 'HH:mm')}
                        </div>
                        <div className="truncate text-xs font-medium">{event.title}</div>
                        <JoinButton event={event} size="sm" />
                      </div>
                    ))
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {(creating || editing) && (
        <EventDialog
          event={editing}
          calendars={calendars}
          events={events}
          onClose={closeDialog}
          onSaved={() => void queryClient.invalidateQueries({ queryKey: ['events'] })}
        />
      )}
    </div>
  );
}

const MAX_VISIBLE_MONTH_EVENTS = 3;
const WEEKDAY_LABELS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

interface MonthGridProps {
  anchor: Date;
  days: Date[];
  events: CalendarEvent[];
  onDayClick: (day: Date) => void;
  onEventClick: (event: CalendarEvent) => void;
}

function MonthGrid({ anchor, days, events, onDayClick, onEventClick }: MonthGridProps) {
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
        {days.map((day) => {
          const dayEvents = events
            .filter((e) => isSameDay(new Date(e.start), day))
            .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime());
          const visible = dayEvents.slice(0, MAX_VISIBLE_MONTH_EVENTS);
          const overflow = dayEvents.length - visible.length;
          const inMonth = isSameMonth(day, anchor);
          const today = isToday(day);
          return (
            // biome-ignore lint/a11y/useSemanticElements: CSS Grid month calendar, not a data table — role="gridcell" on a div is the correct ARIA pattern here.
            <div
              key={day.toISOString()}
              role="gridcell"
              tabIndex={0}
              data-outside-month={!inMonth}
              data-today={today}
              onClick={() => onDayClick(day)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') onDayClick(day);
              }}
              className={cn(
                'flex min-h-20 cursor-pointer flex-col gap-0.5 border-r border-b p-1 last:border-r-0 hover:bg-accent/40',
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
                {visible.map((event) => (
                  // biome-ignore lint/a11y/useSemanticElements: hosts a real <button> (JoinButton) inline — nesting a button inside a button is invalid HTML, so this is a div with button semantics instead.
                  <div
                    key={event.id}
                    role="button"
                    tabIndex={0}
                    onClick={(e) => {
                      e.stopPropagation();
                      onEventClick(event);
                    }}
                    onKeyDown={(e) => {
                      if (e.target !== e.currentTarget) return;
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onEventClick(event);
                      }
                    }}
                    title={event.title}
                    className="flex items-center justify-between gap-1 truncate rounded px-1 py-0.5 text-left text-[10px] font-medium hover:bg-accent"
                  >
                    <span className="truncate">{event.title}</span>
                    <JoinButton event={event} size="sm" />
                  </div>
                ))}
                {overflow > 0 && (
                  <span className="truncate px-1 py-0.5 text-[10px] text-muted-foreground">
                    +{overflow} more
                  </span>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function defaultStart(): string {
  const d = new Date();
  d.setMinutes(0, 0, 0);
  d.setHours(d.getHours() + 1);
  return d.toISOString();
}

type Busy = null | 'save' | 'delete' | RsvpStatus;

function EventDialog({
  event,
  calendars,
  events,
  onClose,
  onSaved,
}: {
  event: CalendarEvent | null;
  calendars: Calendar[];
  events: CalendarEvent[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = event !== null;
  const writable = calendars.filter((c) => c.canWrite);
  const start0 = event?.start ?? defaultStart();
  const end0 = event?.end ?? new Date(new Date(start0).getTime() + 30 * 60_000).toISOString();

  const [title, setTitle] = useState(event?.title ?? '');
  const [calendarId, setCalendarId] = useState(
    event?.calendarId ?? writable[0]?.id ?? calendars[0]?.id ?? ''
  );
  const [location, setLocation] = useState(event?.location ?? '');
  const [description, setDescription] = useState(event?.description ?? '');
  const [start, setStart] = useState(() => toLocalInput(start0));
  const [end, setEnd] = useState(() => toLocalInput(end0));
  const [attendees, setAttendees] = useState(
    (event?.attendees ?? []).map((a) => a.email).join(', ')
  );
  const [addConferencing, setAddConferencing] = useState(false);
  const [busy, setBusy] = useState<Busy>(null);

  // Self identity for the conflict check below (an event the signed-in user
  // declined isn't "busy" — mirrors toBusyIntervals' ownEmails contract).
  const { data: me } = useQuery({
    queryKey: ['me'],
    queryFn: () => orMock(() => api.getMe(), () => mockUser),
    staleTime: 5 * 60_000,
  });

  // Double-booking warning (Task 12 parity): checked against the events
  // already loaded for the currently visible range. Lighter than web's
  // ConflictWarning (which fetches an unfiltered window around the candidate
  // slot across every account) — a deliberate desktop simplification, so a
  // candidate time far outside the visible range won't be checked here.
  const conflicts = useMemo(() => {
    const startDate = new Date(fromLocalInput(start));
    const endDate = new Date(fromLocalInput(end));
    if (Number.isNaN(startDate.getTime()) || Number.isNaN(endDate.getTime())) return [];
    const busyIntervals = toBusyIntervals(events, calendars, me ? [me.email] : [], {
      ignoreEventId: event?.id,
    });
    return findConflicts(startDate, endDate, busyIntervals);
  }, [start, end, events, calendars, me, event?.id]);

  function attendeeEmails(): string[] {
    return attendees
      .split(/[,;\n]/)
      .map((s) => s.trim())
      .filter(Boolean);
  }

  async function handleSave() {
    if (!title.trim()) {
      toast({ title: 'Add a title', variant: 'destructive' });
      return;
    }
    setBusy('save');
    try {
      if (isEdit) {
        const patch: EventPatch = {
          title,
          location,
          description,
          start: fromLocalInput(start),
          end: fromLocalInput(end),
          attendeeEmails: attendeeEmails(),
        };
        if (!isDemoMode()) await api.updateEvent(event!.id, patch);
      } else {
        if (!calendarId) {
          toast({ title: 'No writable calendar', variant: 'destructive' });
          setBusy(null);
          return;
        }
        const input: EventInput = {
          calendarId,
          title,
          location,
          description,
          start: fromLocalInput(start),
          end: fromLocalInput(end),
          attendeeEmails: attendeeEmails(),
          addConferencing,
        };
        if (!isDemoMode()) await api.createEvent(input);
      }
      toast({ title: isEdit ? 'Event updated' : 'Event created' });
      onSaved();
      onClose();
    } catch (e) {
      setBusy(null);
      toast({ title: 'Could not save event', description: errorMessage(e), variant: 'destructive' });
    }
  }

  async function handleDelete() {
    if (!event) return;
    setBusy('delete');
    try {
      if (!isDemoMode()) await api.deleteEvent(event.id);
      toast({ title: 'Event deleted' });
      onSaved();
      onClose();
    } catch (e) {
      setBusy(null);
      toast({ title: 'Could not delete event', description: errorMessage(e), variant: 'destructive' });
    }
  }

  async function handleRsvp(response: RsvpStatus) {
    if (!event) return;
    setBusy(response);
    try {
      if (!isDemoMode()) await api.rsvp(event.id, response);
      toast({ title: 'RSVP sent' });
      onSaved();
      onClose();
    } catch (e) {
      setBusy(null);
      toast({ title: 'Could not RSVP', description: errorMessage(e), variant: 'destructive' });
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-w-md gap-3">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {isEdit ? 'Edit event' : 'New event'}
            {isEdit && <JoinButton event={event} size="sm" />}
          </DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Input
            aria-label="Title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Title"
          />
          {!isEdit && writable.length > 0 && (
            <select
              aria-label="Calendar"
              value={calendarId}
              onChange={(e) => setCalendarId(e.target.value)}
              className={cn(fieldClass, 'cursor-default')}
            >
              {writable.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
          <div className="flex items-center gap-2">
            <input
              type="datetime-local"
              aria-label="Start"
              value={start}
              onChange={(e) => setStart(e.target.value)}
              className={fieldClass}
            />
            <span className="text-xs text-muted-foreground">to</span>
            <input
              type="datetime-local"
              aria-label="End"
              value={end}
              onChange={(e) => setEnd(e.target.value)}
              className={fieldClass}
            />
          </div>
          <Input
            aria-label="Location"
            value={location}
            onChange={(e) => setLocation(e.target.value)}
            placeholder="Location"
          />
          <Input
            aria-label="Guests"
            value={attendees}
            onChange={(e) => setAttendees(e.target.value)}
            placeholder="Guests (comma-separated emails)"
            autoCapitalize="none"
            spellCheck={false}
          />
          <textarea
            aria-label="Description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Description"
            rows={3}
            className="w-full resize-none rounded-md border border-input bg-transparent px-2.5 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />

          {!isEdit && (
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <input
                type="checkbox"
                checked={addConferencing}
                onChange={(e) => setAddConferencing(e.target.checked)}
                className="size-3.5 rounded border-input"
              />
              Add video conferencing
            </label>
          )}

          {conflicts.length > 0 && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-400"
            >
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <div className="space-y-0.5">
                {conflicts.slice(0, 3).map((conflict) => (
                  <p key={conflict.eventId}>
                    Conflicts with «{conflict.title}» ({format(conflict.start, 'h:mm a')}–
                    {format(conflict.end, 'h:mm a')})
                  </p>
                ))}
                {conflicts.length > 3 && (
                  <p className="text-xs opacity-80">+{conflicts.length - 3} more</p>
                )}
              </div>
            </div>
          )}

          {isEdit && event!.attendees.length > 0 && (
            <div className="flex items-center gap-2 border-t pt-2">
              <span className="text-xs text-muted-foreground">RSVP</span>
              <Button
                variant="outline"
                size="sm"
                disabled={busy !== null}
                onClick={() => handleRsvp('accepted')}
              >
                <Check /> Yes
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={busy !== null}
                onClick={() => handleRsvp('tentative')}
              >
                Maybe
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={busy !== null}
                onClick={() => handleRsvp('declined')}
              >
                <X /> No
              </Button>
            </div>
          )}
        </div>

        <DialogFooter>
          {isEdit && (
            <Button
              variant="ghost"
              size="sm"
              className="mr-auto text-destructive hover:text-destructive"
              disabled={busy !== null}
              onClick={handleDelete}
            >
              {busy === 'delete' ? <Loader2 className="animate-spin" /> : <Trash2 />} Delete
            </Button>
          )}
          <Button variant="ghost" size="sm" disabled={busy !== null} onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={busy !== null} onClick={handleSave}>
            {busy === 'save' ? <Loader2 className="animate-spin" /> : null}
            {isEdit ? 'Save' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
