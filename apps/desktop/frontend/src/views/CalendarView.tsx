import type {
  Calendar,
  Event as CalendarEvent,
  EventInput,
  EventPatch,
  RsvpStatus,
} from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { addDays, addWeeks, format, isSameDay, isToday, startOfWeek, subWeeks } from 'date-fns';
import { Check, ChevronLeft, ChevronRight, Loader2, Plus, Trash2, Video, X } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockCalendars, mockEvents } from '@/lib/mock';
import { isDemoMode } from '@/lib/server-config';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Button } from '@/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/ui/dialog';
import { Input } from '@/ui/input';

const FOCUS_DATE_EVENT = 'calendium:focus-date';

/** Jumps the calendar to the week containing a date (⌘K event results). */
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

export function CalendarView() {
  const queryClient = useQueryClient();
  const [weekStart, setWeekStart] = useState(() => startOfWeek(new Date(), { weekStartsOn: 1 }));
  const [editing, setEditing] = useState<CalendarEvent | null>(null);
  const [creating, setCreating] = useState(false);

  const from = weekStart.toISOString();
  const to = addDays(weekStart, 7).toISOString();

  const {
    data: events = [],
    isError,
    error,
    refetch,
  } = useQuery({
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

  useEffect(() => {
    const onFocus = (e: Event) => {
      const iso = (e as CustomEvent<string>).detail;
      setWeekStart(startOfWeek(new Date(iso), { weekStartsOn: 1 }));
    };
    window.addEventListener(FOCUS_DATE_EVENT, onFocus);
    return () => window.removeEventListener(FOCUS_DATE_EVENT, onFocus);
  }, []);

  const colorByCalendar = useMemo(
    () => new Map(calendars.map((c) => [c.id, c.color])),
    [calendars]
  );
  const days = useMemo(() => Array.from({ length: 7 }, (_, i) => addDays(weekStart, i)), [weekStart]);

  const eventsForDay = (day: Date): CalendarEvent[] =>
    events
      .filter((e) => isSameDay(new Date(e.start), day))
      .sort((a, b) => new Date(a.start).getTime() - new Date(b.start).getTime());

  function closeDialog() {
    setCreating(false);
    setEditing(null);
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <h1 className="text-sm font-semibold">{format(weekStart, 'MMMM yyyy')}</h1>
        <span className="text-xs text-muted-foreground">Week of {format(weekStart, 'MMM d')}</span>
        <div className="ml-auto flex items-center gap-1">
          <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
            <Plus /> New event
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Previous week"
            onClick={() => setWeekStart((w) => subWeeks(w, 1))}
          >
            <ChevronLeft />
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setWeekStart(startOfWeek(new Date(), { weekStartsOn: 1 }))}
          >
            Today
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Next week"
            onClick={() => setWeekStart((w) => addWeeks(w, 1))}
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
      ) : (
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
                    <span className="px-1 py-2 text-center text-[11px] text-muted-foreground/60">—</span>
                  ) : (
                    dayEvents.map((event) => (
                      <button
                        type="button"
                        key={event.id}
                        onClick={() => setEditing(event)}
                        className="w-full rounded-md border bg-card p-1.5 text-left shadow-sm transition-colors hover:bg-accent"
                        style={{
                          borderLeftWidth: 3,
                          borderLeftColor:
                            colorByCalendar.get(event.calendarId) ?? 'hsl(var(--primary))',
                        }}
                      >
                        <div className="flex items-center gap-1 text-[11px] tabular-nums text-muted-foreground">
                          {format(new Date(event.start), 'HH:mm')}
                          {event.conferencing && <Video className="size-3" />}
                        </div>
                        <div className="truncate text-xs font-medium">{event.title}</div>
                      </button>
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
          onClose={closeDialog}
          onSaved={() => void queryClient.invalidateQueries({ queryKey: ['events'] })}
        />
      )}
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
  onClose,
  onSaved,
}: {
  event: CalendarEvent | null;
  calendars: Calendar[];
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
  const [busy, setBusy] = useState<Busy>(null);

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
          <DialogTitle>{isEdit ? 'Edit event' : 'New event'}</DialogTitle>
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
