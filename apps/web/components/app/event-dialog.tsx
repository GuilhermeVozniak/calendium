'use client';

import * as React from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { addDays, addMinutes, format, startOfDay } from 'date-fns';
import {
  AlignLeft,
  Bell,
  ExternalLink,
  MapPin,
  Plus,
  Trash2,
  Users,
  Video,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import type { Calendar, Event, EventInput, EventPatch, RsvpStatus } from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  createEventApi,
  deleteEventApi,
  sendRsvpApi,
  updateEventApi,
} from '@/lib/calendar-data';
import { MOCK_SELF_EMAIL } from '@/lib/calendar-mock';
import { nextHalfHour } from '@/lib/quick-add';
import { cn } from '@/lib/utils';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const DATETIME_FMT = "yyyy-MM-dd'T'HH:mm";
const DATE_FMT = 'yyyy-MM-dd';
const REMINDER_PRESETS = [0, 5, 10, 15, 30, 60, 120, 1440];

function reminderLabel(minutes: number): string {
  if (minutes === 0) return 'At start';
  if (minutes < 60) return `${minutes} min before`;
  if (minutes < 1440 && minutes % 60 === 0) return `${minutes / 60} ${minutes === 60 ? 'hour' : 'hours'} before`;
  if (minutes < 1440) return `${minutes} min before`;
  return `${minutes / 1440} ${minutes === 1440 ? 'day' : 'days'} before`;
}

function parseInput(value: string, allDay: boolean): Date | null {
  if (!value) return null;
  const date = new Date(allDay ? `${value}T00:00` : value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatInput(date: Date, allDay: boolean): string {
  return format(date, allDay ? DATE_FMT : DATETIME_FMT);
}

function FieldRow({
  icon: Icon,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[1.5rem_1fr] items-start gap-2">
      <Icon className="mt-2 size-4 text-muted-foreground" />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

export interface EventDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  calendars: Calendar[];
  /** When set, the dialog edits this event; otherwise it creates a new one. */
  event: Event | null;
  /** Prefill for the create flow (slot click / quick add). */
  defaults: Partial<EventInput> | null;
}

export function EventDialog({ open, onOpenChange, calendars, event, defaults }: EventDialogProps) {
  const queryClient = useQueryClient();

  const [title, setTitle] = React.useState('');
  const [calendarId, setCalendarId] = React.useState('');
  const [allDay, setAllDay] = React.useState(false);
  const [start, setStart] = React.useState('');
  const [end, setEnd] = React.useState('');
  const [location, setLocation] = React.useState('');
  const [description, setDescription] = React.useState('');
  const [attendees, setAttendees] = React.useState<string[]>([]);
  const [attendeeDraft, setAttendeeDraft] = React.useState('');
  const [meet, setMeet] = React.useState(false);
  const [reminders, setReminders] = React.useState<number[]>([10]);
  const [rsvpChoice, setRsvpChoice] = React.useState<RsvpStatus>('needs_action');

  const writableCalendars = calendars.filter((c) => c.canWrite);
  const organizer = event?.attendees.find((a) => a.organizer);
  const isInvite = !!event && event.attendees.length > 0 && organizer?.email !== MOCK_SELF_EMAIL;

  React.useEffect(() => {
    if (!open) return;
    if (event) {
      const startDate = new Date(event.start);
      const endDate = new Date(event.end);
      setTitle(event.title);
      setCalendarId(event.calendarId);
      setAllDay(event.allDay);
      setStart(formatInput(startDate, event.allDay));
      // All-day events store an exclusive end (next midnight); show the inclusive last day.
      setEnd(formatInput(event.allDay ? new Date(endDate.getTime() - 1) : endDate, event.allDay));
      setLocation(event.location ?? '');
      setDescription(event.description ?? '');
      setAttendees(event.attendees.filter((a) => !a.organizer).map((a) => a.email));
      setMeet(event.conferencing != null);
      setReminders([...event.reminderMinutes]);
      const self =
        event.attendees.find((a) => a.email === MOCK_SELF_EMAIL) ??
        event.attendees.find((a) => !a.organizer);
      setRsvpChoice(self?.response ?? 'needs_action');
    } else {
      const d = defaults ?? {};
      const isAllDay = d.allDay ?? false;
      const startDate = d.start ? new Date(d.start) : nextHalfHour();
      const endDate = d.end ? new Date(d.end) : addMinutes(startDate, 30);
      const fallbackCalendar =
        calendars.find((c) => c.isPrimary && c.canWrite) ??
        calendars.find((c) => c.canWrite) ??
        calendars[0];
      setTitle(d.title ?? '');
      setCalendarId(d.calendarId ?? fallbackCalendar?.id ?? '');
      setAllDay(isAllDay);
      setStart(formatInput(startDate, isAllDay));
      setEnd(formatInput(endDate, isAllDay));
      setLocation(d.location ?? '');
      setDescription(d.description ?? '');
      setAttendees(d.attendeeEmails ?? []);
      setMeet(d.addConferencing ?? false);
      setReminders(d.reminderMinutes ?? [10]);
      setRsvpChoice('needs_action');
    }
    setAttendeeDraft('');
  }, [open, event, defaults, calendars]);

  /** Shifting the start keeps the event duration by moving the end with it. */
  const handleStartChange = (value: string) => {
    const prevStart = parseInput(start, allDay);
    const prevEnd = parseInput(end, allDay);
    setStart(value);
    const nextStart = parseInput(value, allDay);
    if (prevStart && prevEnd && nextStart) {
      const duration = prevEnd.getTime() - prevStart.getTime();
      setEnd(formatInput(new Date(nextStart.getTime() + duration), allDay));
    }
  };

  const handleAllDayChange = (next: boolean) => {
    const startDate = parseInput(start, allDay);
    const endDate = parseInput(end, allDay);
    setAllDay(next);
    if (!startDate) return;
    if (next) {
      setStart(format(startDate, DATE_FMT));
      setEnd(format(endDate ?? startDate, DATE_FMT));
    } else {
      const timed = new Date(startDate);
      timed.setHours(9, 0, 0, 0);
      setStart(format(timed, DATETIME_FMT));
      setEnd(format(addMinutes(timed, 60), DATETIME_FMT));
    }
  };

  const commitAttendee = () => {
    const email = attendeeDraft.trim().replace(/,+$/, '');
    if (!email) return;
    if (!EMAIL_RE.test(email)) {
      toast.error(`"${email}" is not a valid email address`);
      return;
    }
    if (!attendees.includes(email)) setAttendees((prev) => [...prev, email]);
    setAttendeeDraft('');
  };

  const handleAttendeeKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      commitAttendee();
    } else if (e.key === 'Backspace' && !attendeeDraft && attendees.length > 0) {
      setAttendees((prev) => prev.slice(0, -1));
    }
  };

  const buildInput = (): EventInput | null => {
    const startDate = parseInput(start, allDay);
    const endDate = parseInput(end, allDay);
    if (!calendarId || !startDate || !endDate) return null;

    let startIso: string;
    let endIso: string;
    if (allDay) {
      const first = startOfDay(startDate);
      const last = startOfDay(endDate < startDate ? startDate : endDate);
      startIso = first.toISOString();
      endIso = addDays(last, 1).toISOString(); // exclusive end
    } else {
      startIso = startDate.toISOString();
      endIso = (endDate > startDate ? endDate : addMinutes(startDate, 30)).toISOString();
    }

    const allAttendees = [...attendees];
    const draft = attendeeDraft.trim();
    if (draft && EMAIL_RE.test(draft) && !allAttendees.includes(draft)) allAttendees.push(draft);

    return {
      calendarId,
      title: title.trim() || '(No title)',
      description: description.trim() || undefined,
      location: location.trim() || undefined,
      start: startIso,
      end: endIso,
      allDay,
      attendeeEmails: allAttendees.length > 0 ? allAttendees : undefined,
      addConferencing: meet || undefined,
      reminderMinutes:
        reminders.length > 0 ? [...reminders].sort((a, b) => a - b) : undefined,
    };
  };

  const save = useMutation({
    mutationFn: (input: EventInput) => {
      if (!event) return createEventApi(input);
      // PATCH cannot move calendars or toggle conferencing; send only the
      // fields the backend's EventPatch accepts instead of silently dropping them.
      const patch: EventPatch = {
        title: input.title,
        description: input.description,
        location: input.location,
        start: input.start,
        end: input.end,
        allDay: input.allDay,
        attendeeEmails: input.attendeeEmails,
        reminderMinutes: input.reminderMinutes,
      };
      return updateEventApi(event.id, patch);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['events'] });
      toast.success(event ? 'Event updated' : 'Event created');
      onOpenChange(false);
    },
    onError: () => toast.error('Could not save the event'),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteEventApi(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['events'] });
      toast.success('Event deleted');
      onOpenChange(false);
    },
    onError: () => toast.error('Could not delete the event'),
  });

  const rsvp = useMutation({
    mutationFn: (response: RsvpStatus) => {
      if (!event) throw new Error('No event to RSVP to');
      return sendRsvpApi(event.id, response);
    },
    onMutate: (response) => setRsvpChoice(response),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['events'] });
      toast.success('RSVP sent');
    },
    onError: () => toast.error('Could not send the RSVP'),
  });

  const handleSave = () => {
    const input = buildInput();
    if (!input) {
      toast.error('Pick a calendar and valid start/end times');
      return;
    }
    save.mutate(input);
  };

  const rsvpOptions: Array<{ value: RsvpStatus; label: string }> = [
    { value: 'accepted', label: 'Yes' },
    { value: 'tentative', label: 'Maybe' },
    { value: 'declined', label: 'No' },
  ];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{event ? 'Edit event' : 'New event'}</DialogTitle>
        </DialogHeader>

        {isInvite && (
          <div className="flex items-center justify-between gap-3 rounded-md border bg-muted/40 px-3 py-2">
            <span className="text-sm font-medium">
              Going?
              {organizer && (
                <span className="ml-1.5 font-normal text-muted-foreground">
                  Invited by {organizer.name ?? organizer.email}
                </span>
              )}
            </span>
            <div className="flex gap-1.5">
              {rsvpOptions.map((option) => (
                <Button
                  key={option.value}
                  size="sm"
                  variant={rsvpChoice === option.value ? 'default' : 'outline'}
                  className="h-7 px-2.5 text-xs"
                  onClick={() => rsvp.mutate(option.value)}
                  disabled={rsvp.isPending}
                >
                  {option.label}
                </Button>
              ))}
            </div>
          </div>
        )}

        <div className="grid gap-3">
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Add title"
            aria-label="Event title"
            className="h-10 text-base font-medium"
          />

          <div className="grid grid-cols-2 gap-2">
            <div className="grid gap-1.5">
              <Label htmlFor="event-start">Start</Label>
              <Input
                id="event-start"
                type={allDay ? 'date' : 'datetime-local'}
                value={start}
                onChange={(e) => handleStartChange(e.target.value)}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="event-end">End</Label>
              <Input
                id="event-end"
                type={allDay ? 'date' : 'datetime-local'}
                value={end}
                onChange={(e) => setEnd(e.target.value)}
              />
            </div>
          </div>

          <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-2">
              <Switch id="event-all-day" checked={allDay} onCheckedChange={handleAllDayChange} />
              <Label htmlFor="event-all-day" className="font-normal">
                All day
              </Label>
            </div>
            <Select value={calendarId} onValueChange={setCalendarId}>
              <SelectTrigger size="sm" className="w-44">
                <SelectValue placeholder="Calendar" />
              </SelectTrigger>
              <SelectContent>
                {writableCalendars.map((calendar) => (
                  <SelectItem key={calendar.id} value={calendar.id}>
                    <span
                      className="size-2.5 shrink-0 rounded-full"
                      style={{ backgroundColor: calendar.color }}
                    />
                    {calendar.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <FieldRow icon={MapPin}>
            <Input
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              placeholder="Add location"
              aria-label="Location"
              className="h-8"
            />
          </FieldRow>

          <FieldRow icon={Users}>
            <div
              className={cn(
                'border-input dark:bg-input/30 flex min-h-8 w-full flex-wrap items-center gap-1 rounded-md border bg-transparent px-2 py-1 shadow-xs transition-[color,box-shadow]',
                'focus-within:border-ring focus-within:ring-ring/50 focus-within:ring-[3px]'
              )}
            >
              {attendees.map((email) => (
                <Badge key={email} variant="secondary" className="gap-1 font-normal">
                  {email}
                  <button
                    type="button"
                    aria-label={`Remove ${email}`}
                    className="opacity-60 hover:opacity-100"
                    onClick={() => setAttendees((prev) => prev.filter((a) => a !== email))}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
              <input
                value={attendeeDraft}
                onChange={(e) => setAttendeeDraft(e.target.value)}
                onKeyDown={handleAttendeeKeyDown}
                onBlur={commitAttendee}
                placeholder={attendees.length === 0 ? 'Add attendees (email, Enter)' : ''}
                aria-label="Add attendee"
                className="h-6 min-w-28 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              />
            </div>
          </FieldRow>

          <FieldRow icon={Video}>
            <div className="flex h-8 items-center justify-between">
              <Label htmlFor="event-meet" className="font-normal">
                Add Google Meet
              </Label>
              <Switch id="event-meet" checked={meet} onCheckedChange={setMeet} />
            </div>
            {event?.conferencing && (
              <a
                href={event.conferencing.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-2 hover:underline"
              >
                <ExternalLink className="size-3" />
                {event.conferencing.url}
              </a>
            )}
          </FieldRow>

          <FieldRow icon={Bell}>
            <div className="flex min-h-8 flex-wrap items-center gap-1.5">
              {reminders.map((minutes) => (
                <Badge key={minutes} variant="outline" className="gap-1 font-normal">
                  {reminderLabel(minutes)}
                  <button
                    type="button"
                    aria-label={`Remove reminder ${reminderLabel(minutes)}`}
                    className="opacity-60 hover:opacity-100"
                    onClick={() => setReminders((prev) => prev.filter((m) => m !== minutes))}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
              <Select
                value=""
                onValueChange={(v) =>
                  setReminders((prev) => [...prev, Number(v)].sort((a, b) => a - b))
                }
              >
                <SelectTrigger
                  size="sm"
                  className="h-7 gap-1 border-dashed px-2 text-xs text-muted-foreground"
                  aria-label="Add reminder"
                >
                  <Plus className="size-3.5" />
                  Add reminder
                </SelectTrigger>
                <SelectContent>
                  {REMINDER_PRESETS.filter((m) => !reminders.includes(m)).map((minutes) => (
                    <SelectItem key={minutes} value={String(minutes)}>
                      {reminderLabel(minutes)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </FieldRow>

          <FieldRow icon={AlignLeft}>
            <Textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Add description"
              aria-label="Description"
              className="min-h-16"
            />
          </FieldRow>
        </div>

        <DialogFooter>
          {event && (
            <Button
              variant="ghost"
              className="mr-auto text-destructive hover:text-destructive"
              onClick={() => remove.mutate(event.id)}
              disabled={remove.isPending}
            >
              <Trash2 />
              Delete
            </Button>
          )}
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={save.isPending || !calendarId}>
            {event ? 'Save changes' : 'Create event'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
