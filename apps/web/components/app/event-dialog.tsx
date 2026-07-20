'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { addDays, addMinutes, format, startOfDay } from 'date-fns';
import {
  AlignLeft,
  Bell,
  BookmarkPlus,
  ExternalLink,
  LayoutTemplate,
  Link2,
  MapPin,
  Plus,
  Repeat,
  Trash2,
  Users,
  Video,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import type {
  Calendar,
  Event,
  EventInput,
  EventPatch,
  EventTemplateInput,
  RsvpStatus,
} from '@calendium/shared';
import { detectConference } from '@calendium/shared';

import { ConflictWarning } from '@/components/app/calendar/conflict-warning';
import { JoinButton } from '@/components/app/calendar/join-button';
import { FindATimeGrid, ProposalsList, ProposeTimeForm } from '@/components/app/find-a-time';
import { LocationField } from '@/components/app/location-field';

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
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Textarea } from '@/components/ui/textarea';
import {
  createEventApi,
  deleteEventApi,
  fetchEventNoteApi,
  putEventNoteApi,
  sendRsvpApi,
  updateEventApi,
} from '@/lib/calendar-data';
import { nextHalfHour } from '@/lib/quick-add';
import { fetchAccounts } from '@/lib/settings-data';
import {
  applyTemplate,
  createEventTemplateApi,
  fetchEventTemplates,
  recordTemplateUsage,
} from '@/lib/template-data';
import { useSelfEmails } from '@/lib/use-identity';
import { cn } from '@/lib/utils';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const DATETIME_FMT = "yyyy-MM-dd'T'HH:mm";
const DATE_FMT = 'yyyy-MM-dd';
const REMINDER_PRESETS = [0, 5, 10, 15, 30, 60, 120, 1440];

const RECURRENCE_FREQ_LABEL: Record<string, string> = {
  DAILY: 'day',
  WEEKLY: 'week',
  MONTHLY: 'month',
  YEARLY: 'year',
};

/**
 * Turns an RFC 5545 RRULE body (e.g. "FREQ=WEEKLY;BYDAY=MO,WE") into a short,
 * human-readable label. Also used by QuickAddBar's live-preview chip so both
 * surfaces describe a recurrence the same way.
 */
export function humanizeRecurrence(rule: string): string {
  const params = new Map(rule.split(';').map((part) => part.split('=') as [string, string]));
  const freq = params.get('FREQ') ?? '';
  const interval = Number(params.get('INTERVAL') ?? '1');
  const byday = params.get('BYDAY');
  const unit = RECURRENCE_FREQ_LABEL[freq] ?? 'time';
  let label = interval > 1 ? `Every ${interval} ${unit}s` : `Every ${unit}`;
  if (byday) label += ` on ${byday.split(',').join(', ')}`;
  return label;
}

/** Also used by QuickAddBar to label alert chips with the same wording. */
export function reminderLabel(minutes: number): string {
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

/**
 * Read-only detail view for ICS feed subscription events (M2.8 Task 15).
 * Feed mirrors cannot be edited, RSVP'd, or deleted — the feed owns them —
 * so this compact dialog replaces the full editor for events carrying a
 * `subscriptionId` (see the calendar page's dialog switch).
 */
export function SubscriptionEventDialog({
  open,
  onOpenChange,
  event,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  event: Event | null;
}) {
  if (!event) return null;
  const start = new Date(event.start);
  const end = new Date(event.end);
  const when = event.allDay
    ? format(start, 'EEEE, MMMM d, yyyy')
    : `${format(start, 'EEE, MMM d · h:mm a')} – ${format(end, 'h:mm a')}`;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{event.title || '(No title)'}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3 text-sm">
          <p className="text-muted-foreground">{when}</p>
          {event.location && (
            <FieldRow icon={MapPin}>
              <p className="pt-1.5">{event.location}</p>
            </FieldRow>
          )}
          {event.description && (
            <FieldRow icon={AlignLeft}>
              <p className="pt-1.5 whitespace-pre-wrap">{event.description}</p>
            </FieldRow>
          )}
          <Badge variant="outline" className="w-fit font-normal">
            Subscribed calendar · read-only
          </Badge>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
  // Set only when `location` came from an autocomplete pick; free typing
  // clears it so stale coordinates never outlive an edited location string.
  const [locationCoords, setLocationCoords] = React.useState<{ lat: number; lon: number } | null>(
    null
  );
  const [recurrenceRule, setRecurrenceRule] = React.useState<string | null>(null);
  const [description, setDescription] = React.useState('');
  const [attendees, setAttendees] = React.useState<string[]>([]);
  const [attendeeDraft, setAttendeeDraft] = React.useState('');
  const [meet, setMeet] = React.useState(false);
  const [reminders, setReminders] = React.useState<number[]>([10]);
  const [rsvpChoice, setRsvpChoice] = React.useState<RsvpStatus>('needs_action');
  // Notes tab (M2.8 Task 4) exists only in edit mode: a note is keyed by an
  // event id, which a not-yet-created event doesn't have.
  const [activeTab, setActiveTab] = React.useState<'details' | 'notes'>('details');

  const selfEmails = useSelfEmails();
  const writableCalendars = calendars.filter((c) => c.canWrite);
  const templatesQuery = useQuery({
    queryKey: ['event-templates'],
    queryFn: fetchEventTemplates,
    enabled: open && !event,
  });
  // Backs the create-mode conferencing helper text: the backend attaches a
  // Meet or Teams link depending on which provider owns the target calendar's
  // account (Google -> conferenceData.createRequest, Microsoft ->
  // isOnlineMeeting/teamsForBusiness), so the copy has to follow calendarId.
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });
  const selectedCalendarAccount = calendars.find((c) => c.id === calendarId)?.accountId;
  const selectedProvider = accountsQuery.data?.find((a) => a.id === selectedCalendarAccount)?.provider;
  const organizer = event?.attendees.find((a) => a.organizer);
  const isInvite =
    !!event &&
    event.attendees.length > 0 &&
    !(organizer && selfEmails.has(organizer.email.toLowerCase()));

  // Find-a-Time grid drives start/end while creating/editing (organizer
  // side); a duration-sized column pick just overwrites the current fields.
  const startForGrid = parseInput(start, allDay);
  const endForGrid = parseInput(end, allDay);
  const gridDurationMinutes =
    startForGrid && endForGrid && endForGrid > startForGrid
      ? Math.round((endForGrid.getTime() - startForGrid.getTime()) / 60_000)
      : 30;

  React.useEffect(() => {
    if (!open) return;
    setActiveTab('details');
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
      setLocationCoords(
        event.locationLat != null && event.locationLon != null
          ? { lat: event.locationLat, lon: event.locationLon }
          : null
      );
      setRecurrenceRule(event.recurrenceRule);
      setDescription(event.description ?? '');
      setAttendees(event.attendees.filter((a) => !a.organizer).map((a) => a.email));
      setMeet(event.conferencing != null);
      setReminders([...event.reminderMinutes]);
      const self =
        event.attendees.find((a) => selfEmails.has(a.email.toLowerCase())) ??
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
      setLocationCoords(
        d.locationLat != null && d.locationLon != null
          ? { lat: d.locationLat, lon: d.locationLon }
          : null
      );
      setRecurrenceRule(d.recurrenceRule ?? null);
      setDescription(d.description ?? '');
      setAttendees(d.attendeeEmails ?? []);
      setMeet(d.addConferencing ?? false);
      setReminders(d.reminderMinutes ?? [10]);
      setRsvpChoice('needs_action');
    }
    setAttendeeDraft('');
  }, [open, event, defaults, calendars, selfEmails]);

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
      // TODO(M2.2-final): same tri-state clear gap as recurrenceRule had — see Task 8 review.
      // `|| undefined` means an edit that clears these fields omits them from the
      // PATCH instead of sending '' to actually clear them on the backend.
      description: description.trim() || undefined,
      location: location.trim() || undefined,
      // Coordinates only ever accompany an autocomplete-picked location;
      // free-typed text sends none (travel features skip such events).
      ...(location.trim() && locationCoords
        ? { locationLat: locationCoords.lat, locationLon: locationCoords.lon }
        : {}),
      start: startIso,
      end: endIso,
      allDay,
      recurrenceRule: recurrenceRule ?? undefined,
      attendeeEmails: allAttendees.length > 0 ? allAttendees : undefined,
      addConferencing: meet || undefined,
      reminderMinutes:
        reminders.length > 0 ? [...reminders].sort((a, b) => a - b) : undefined,
    };
  };

  /**
   * Applying a template replaces the create-form's fields wholesale, anchored
   * at whatever slot the dialog is already pending on (the click/quick-add
   * time), not "now" - so applying a template never moves the event off the
   * time the user meant to create it at.
   */
  const handleTemplateSelect = (templateId: string) => {
    const template = templatesQuery.data?.find((t) => t.id === templateId);
    if (!template) return;
    const anchor = parseInput(start, allDay) ?? new Date();
    const applied = applyTemplate(template, anchor);
    setTitle(applied.title ?? '');
    setDescription(applied.description ?? '');
    setLocation(applied.location ?? '');
    setLocationCoords(null); // templates store plain location text only
    setAllDay(applied.allDay ?? false);
    if (applied.start) setStart(formatInput(new Date(applied.start), applied.allDay ?? false));
    if (applied.end) setEnd(formatInput(new Date(applied.end), applied.allDay ?? false));
    setRecurrenceRule(applied.recurrenceRule ?? null);
    setAttendees(applied.attendeeEmails ?? []);
    setMeet(applied.addConferencing ?? false);
    setReminders(applied.reminderMinutes ?? []);
    if (applied.calendarId) setCalendarId(applied.calendarId);
    recordTemplateUsage(template.id);
  };

  const save = useMutation({
    mutationFn: (input: EventInput) => {
      if (!event) return createEventApi(input);
      // PATCH cannot move calendars or toggle conferencing; send only the
      // fields the backend's EventPatch accepts instead of silently dropping them.
      //
      // recurrenceRule is tri-state on the backend: an absent key means
      // "unchanged", while an explicit "" means "clear the recurrence" (see
      // EventPatch.RecurrenceRule, a *string, in backend/internal/domain).
      // JSON.stringify drops `undefined` keys entirely, so `input.recurrenceRule`
      // (which is `recurrenceRule ?? undefined`) is only correct for two of the
      // three states — it can't distinguish "dismissed an existing rule" from
      // "never had one" since both end up `undefined`. Compare against the
      // event's original rule to recover the third state explicitly.
      const patch: EventPatch = {
        title: input.title,
        description: input.description,
        location: input.location,
        ...(input.locationLat !== undefined
          ? { locationLat: input.locationLat, locationLon: input.locationLon }
          : {}),
        start: input.start,
        end: input.end,
        allDay: input.allDay,
        recurrenceRule: event.recurrenceRule && !recurrenceRule ? '' : input.recurrenceRule,
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

  const saveAsTemplate = useMutation({
    mutationFn: () => {
      const startDate = parseInput(start, allDay);
      const endDate = parseInput(end, allDay);
      const durationMinutes =
        startDate && endDate
          ? Math.max(5, Math.round((endDate.getTime() - startDate.getTime()) / 60_000))
          : 30;
      const input: EventTemplateInput = {
        name: title.trim() || '(No title)',
        title: title.trim() || '(No title)',
        description: description.trim() || undefined,
        location: location.trim() || undefined,
        durationMinutes,
        allDay,
        calendarId: calendarId || null,
        attendeeEmails: attendees.length > 0 ? attendees : undefined,
        addConferencing: meet || undefined,
        reminderMinutes:
          reminders.length > 0 ? [...reminders].sort((a, b) => a - b) : undefined,
        recurrenceRule: recurrenceRule ?? undefined,
      };
      return createEventTemplateApi(input);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['event-templates'] });
      toast.success('Saved as template');
    },
    onError: () => toast.error('Could not save the template'),
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
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
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

        {event && (
          <Tabs
            value={activeTab}
            onValueChange={(v) => setActiveTab(v as 'details' | 'notes')}
          >
            <TabsList className="grid w-full grid-cols-2">
              <TabsTrigger value="details">Details</TabsTrigger>
              <TabsTrigger value="notes">Notes</TabsTrigger>
            </TabsList>
          </Tabs>
        )}

        {/* Hidden (not unmounted) on the Notes tab so in-progress edits survive tab switches. */}
        <div className={cn('grid gap-3', event != null && activeTab !== 'details' && 'hidden')}>
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Add title"
            aria-label="Event title"
            className="h-10 text-base font-medium"
          />

          {!event && (templatesQuery.data?.length ?? 0) > 0 && (
            <FieldRow icon={LayoutTemplate}>
              <Select value="" onValueChange={handleTemplateSelect}>
                <SelectTrigger
                  size="sm"
                  className="h-8 w-full"
                  aria-label="Start from template"
                >
                  <SelectValue placeholder="Start from template (optional)" />
                </SelectTrigger>
                <SelectContent>
                  {templatesQuery.data?.map((t) => (
                    <SelectItem key={t.id} value={t.id}>
                      {t.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FieldRow>
          )}

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

          <FieldRow icon={Repeat}>
            <div className="flex min-h-8 items-center">
              {recurrenceRule ? (
                <Badge variant="outline" className="gap-1 font-normal">
                  {humanizeRecurrence(recurrenceRule)}
                  <button
                    type="button"
                    aria-label="Remove recurrence"
                    className="opacity-60 hover:opacity-100"
                    onClick={() => setRecurrenceRule(null)}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ) : (
                <span className="text-sm text-muted-foreground">Does not repeat</span>
              )}
            </div>
          </FieldRow>

          <ConflictWarning
            calendars={calendars}
            start={parseInput(start, allDay)}
            end={parseInput(end, allDay)}
            allDay={allDay}
            ignoreEventId={event?.id}
          />

          <FieldRow icon={MapPin}>
            <LocationField
              value={location}
              onChange={(next, coords) => {
                setLocation(next);
                setLocationCoords(coords);
              }}
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

          {attendees.length > 0 && !isInvite && (
            <FindATimeGrid
              attendeeEmails={attendees}
              durationMinutes={gridDurationMinutes}
              initialDate={startForGrid ?? new Date()}
              onPick={(pickedStart, pickedEnd) => {
                setStart(formatInput(pickedStart, allDay));
                setEnd(formatInput(pickedEnd, allDay));
              }}
            />
          )}

          {event && isInvite && (
            <ProposeTimeForm
              eventId={event.id}
              initialStart={startForGrid ?? new Date(event.start)}
              initialEnd={endForGrid ?? new Date(event.end)}
              onProposed={() => toast.success('Proposed a new time')}
            />
          )}

          {event && !isInvite && (
            <ProposalsList
              eventId={event.id}
              onAccepted={() => {
                void queryClient.invalidateQueries({ queryKey: ['events'] });
                toast.success('Proposal accepted');
                onOpenChange(false);
              }}
            />
          )}

          <FieldRow icon={Video}>
            {!event ? (
              <div className="flex flex-col gap-1">
                <div className="flex h-8 items-center justify-between">
                  <Label htmlFor="event-meet" className="font-normal">
                    Add video conferencing
                  </Label>
                  <Switch id="event-meet" checked={meet} onCheckedChange={setMeet} />
                </div>
                {meet && accountsQuery.data && (
                  <span className="text-xs text-muted-foreground">
                    {selectedProvider === 'microsoft'
                      ? 'Teams meeting will be added'
                      : 'Google Meet link will be added'}
                  </span>
                )}
              </div>
            ) : detectConference(event) ? (
              <div className="flex h-8 items-center">
                <JoinButton event={event} size="sm" />
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                Video conferencing can only be added when creating an event — the backend
                attaches it once, at creation time.
              </p>
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

        {event && activeTab === 'notes' && <EventNotesPanel eventId={event.id} />}

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
          <Button
            variant="outline"
            onClick={() => saveAsTemplate.mutate()}
            disabled={saveAsTemplate.isPending}
          >
            <BookmarkPlus />
            Save as template
          </Button>
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

// ---------------------------------------------------------------------------
// Notes tab (M2.8 Task 4)
// ---------------------------------------------------------------------------

const NOTE_AUTOSAVE_MS = 800;

/**
 * Local-only markdown notes + doc links attached to the event. Edits are
 * optimistic (local state is the source of truth while typing, same spirit as
 * the thread-action pattern) and autosave with an 800ms debounce; notes never
 * reach the calendar provider or the attendees.
 */
function EventNotesPanel({ eventId }: { eventId: string }) {
  const queryClient = useQueryClient();
  const noteQuery = useQuery({
    queryKey: ['event-note', eventId],
    queryFn: () => fetchEventNoteApi(eventId),
  });

  const [bodyMd, setBodyMd] = React.useState('');
  const [links, setLinks] = React.useState<string[]>([]);
  const [linkDraft, setLinkDraft] = React.useState('');
  const [hydratedFor, setHydratedFor] = React.useState<string | null>(null);
  const saveTimer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  // Hydrate the editable state once per event from the fetched note; later
  // refetches must not clobber in-progress typing.
  React.useEffect(() => {
    if (noteQuery.data && hydratedFor !== eventId) {
      setBodyMd(noteQuery.data.bodyMd);
      setLinks(noteQuery.data.links);
      setHydratedFor(eventId);
    }
  }, [noteQuery.data, eventId, hydratedFor]);

  React.useEffect(
    () => () => {
      if (saveTimer.current) clearTimeout(saveTimer.current);
    },
    []
  );

  const save = useMutation({
    mutationFn: (next: { bodyMd: string; links: string[] }) =>
      putEventNoteApi(eventId, next.bodyMd, next.links),
    onSuccess: (saved) => queryClient.setQueryData(['event-note', eventId], saved),
    onError: () => toast.error('Could not save the note'),
  });

  const scheduleSave = (nextBody: string, nextLinks: string[]) => {
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      save.mutate({ bodyMd: nextBody, links: nextLinks });
    }, NOTE_AUTOSAVE_MS);
  };

  const addLink = () => {
    const url = linkDraft.trim();
    if (!url) return;
    if (!/^https?:\/\/\S+$/i.test(url)) {
      toast.error('Links must be full http(s) URLs');
      return;
    }
    setLinkDraft('');
    if (links.includes(url)) return;
    const next = [...links, url];
    setLinks(next);
    scheduleSave(bodyMd, next);
  };

  const removeLink = (url: string) => {
    const next = links.filter((l) => l !== url);
    setLinks(next);
    scheduleSave(bodyMd, next);
  };

  return (
    <div className="grid gap-3">
      <Textarea
        value={bodyMd}
        onChange={(e) => {
          setBodyMd(e.target.value);
          scheduleSave(e.target.value, links);
        }}
        placeholder="Add meeting notes (markdown)"
        aria-label="Event notes"
        className="min-h-40"
        disabled={noteQuery.isLoading}
      />

      <FieldRow icon={Link2}>
        <div className="grid gap-1.5">
          {links.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              {links.map((url) => (
                <Badge key={url} variant="secondary" className="max-w-full gap-1 font-normal">
                  <a
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex min-w-0 items-center gap-1 hover:underline"
                    aria-label={`Open ${url}`}
                  >
                    <span className="truncate">{url.replace(/^https?:\/\//i, '')}</span>
                    <ExternalLink className="size-3 shrink-0 opacity-60" />
                  </a>
                  <button
                    type="button"
                    aria-label={`Remove link ${url}`}
                    className="opacity-60 hover:opacity-100"
                    onClick={() => removeLink(url)}
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              ))}
            </div>
          )}
          <div className="flex items-center gap-1.5">
            <Input
              value={linkDraft}
              onChange={(e) => setLinkDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  addLink();
                }
              }}
              placeholder="Attach a doc link (https://...)"
              aria-label="Add doc link"
              className="h-8"
            />
            <Button type="button" variant="outline" size="sm" className="h-8" onClick={addLink}>
              Add
            </Button>
          </div>
        </div>
      </FieldRow>

      <p className="text-xs text-muted-foreground" aria-live="polite">
        {save.isPending
          ? 'Saving…'
          : 'Notes save automatically and stay in Calendium — never sent to attendees.'}
      </p>
    </div>
  );
}
