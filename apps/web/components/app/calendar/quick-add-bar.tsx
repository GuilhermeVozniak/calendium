'use client';

import * as React from 'react';
import { addMinutes, format, isSameDay, startOfDay } from 'date-fns';
import { Bell, Clock, Hourglass, MapPin, Repeat, Sparkles, Users } from 'lucide-react';

import type { Calendar, Event, EventInput } from '@calendium/shared';
import { suggestFreeSlots, toBusyIntervals } from '@calendium/shared';

import { humanizeRecurrence, reminderLabel } from '@/components/app/event-dialog';
import { Input } from '@/components/ui/input';
import { parseQuickAdd, type QuickAddParse } from '@/lib/quick-add';
import { useSelfEmails } from '@/lib/use-identity';

const WORK_DAY_START_HOUR = 9;
const WORK_DAY_END_HOUR = 18;
const MAX_SUGGESTIONS = 3;
const DEFAULT_SUGGESTION_DURATION = 30;

// UI-only heuristic for deciding whether to surface free-slot suggestions and
// the date/time preview chip — deliberately separate from QuickAddParse's
// own (more thorough) date/time grammar so this component doesn't need to
// change the parser's public shape just to expose "was a time recognized?".
const EXPLICIT_TIME_HINT_RE =
  /\b\d{1,2}(?::\d{2})?\s*(?:am|pm)\b|\b\d{1,2}:\d{2}\b|\bnoon\b|\bmidnight\b|\bmidday\b|\bat\s+\d{1,2}\b/i;
const EXPLICIT_DATE_HINT_RE =
  /\b(?:today|tonight|tomorrow|tmrw?)\b|\b(?:next\s+|this\s+)?(?:sun|mon|tue|wed|thu|fri|sat)\w*\b|\b\d{1,2}\/\d{1,2}\b|\b(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)\w*\.?\s+\d{1,2}\b/i;

export interface QuickAddBarProps {
  calendars: Calendar[];
  events: Event[];
  onCreate: (defaults: Partial<EventInput>) => void;
}

function buildDefaults(parsed: QuickAddParse): Partial<EventInput> {
  return {
    title: parsed.title,
    start: parsed.start.toISOString(),
    end: parsed.end.toISOString(),
    allDay: parsed.allDay,
    location: parsed.location ?? undefined,
    recurrenceRule: parsed.recurrenceRule ?? undefined,
    reminderMinutes: parsed.reminderMinutes.length > 0 ? parsed.reminderMinutes : undefined,
    attendeeEmails: parsed.attendeeEmails.length > 0 ? parsed.attendeeEmails : undefined,
  };
}

function humanizeDuration(minutes: number): string {
  if (minutes % 60 === 0) {
    const hrs = minutes / 60;
    return `${hrs} hr${hrs === 1 ? '' : 's'}`;
  }
  if (minutes > 60) return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
  return `${minutes} min`;
}

function Chip({
  icon: Icon,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full border bg-muted/40 px-2 py-0.5 text-xs text-muted-foreground">
      <Icon className="size-3" />
      {children}
    </span>
  );
}

/**
 * Quick-add input for the calendar toolbar: parses the text on every
 * keystroke (via `parseQuickAdd`) and renders a live preview of what Enter
 * will create — one chip per recognized facet — plus, when no explicit time
 * was typed, up to 3 free-slot suggestions for today (from `suggestFreeSlots`,
 * clamped to working hours). The dialog itself stays owned by the caller;
 * this component only ever calls `onCreate` with a prefill.
 */
export function QuickAddBar({ calendars, events, onCreate }: QuickAddBarProps) {
  const [text, setText] = React.useState('');
  const selfEmails = useSelfEmails();

  const trimmed = text.trim();
  const parsed = React.useMemo(
    () => (trimmed ? parseQuickAdd(text) : null),
    [text, trimmed]
  );

  const hasExplicitTime = EXPLICIT_TIME_HINT_RE.test(text);
  const hasDateOrTimeHint = hasExplicitTime || EXPLICIT_DATE_HINT_RE.test(text);

  const suggestions = React.useMemo(() => {
    if (!trimmed || hasExplicitTime) return [];
    const now = new Date();
    const dayStart = startOfDay(now);
    const from = addMinutes(dayStart, WORK_DAY_START_HOUR * 60);
    const to = addMinutes(dayStart, WORK_DAY_END_HOUR * 60);
    const busy = toBusyIntervals(events, calendars, [...selfEmails]).filter((b) =>
      isSameDay(b.start, now)
    );
    const duration = parsed?.durationMinutes ?? DEFAULT_SUGGESTION_DURATION;
    return suggestFreeSlots(from, to, duration, busy)
      .slice(0, MAX_SUGGESTIONS)
      .map((gap) => ({
        start: gap.start,
        end: new Date(Math.min(gap.end.getTime(), gap.start.getTime() + duration * 60_000)),
      }));
  }, [trimmed, hasExplicitTime, events, calendars, selfEmails, parsed]);

  const submit = () => {
    if (!parsed) return;
    onCreate(buildDefaults(parsed));
    setText('');
  };

  const chooseSuggestion = (slot: { start: Date; end: Date }) => {
    onCreate({
      title: parsed?.title,
      start: slot.start.toISOString(),
      end: slot.end.toISOString(),
      allDay: false,
      location: parsed?.location ?? undefined,
      recurrenceRule: parsed?.recurrenceRule ?? undefined,
      reminderMinutes:
        parsed && parsed.reminderMinutes.length > 0 ? parsed.reminderMinutes : undefined,
      attendeeEmails:
        parsed && parsed.attendeeEmails.length > 0 ? parsed.attendeeEmails : undefined,
    });
    setText('');
  };

  const chips: React.ReactNode[] = [];
  if (parsed && hasDateOrTimeHint) {
    chips.push(
      <Chip key="time" icon={Clock}>
        {parsed.allDay
          ? `${format(parsed.start, 'EEE, MMM d')} · All day`
          : `${format(parsed.start, 'EEE, MMM d')} · ${format(parsed.start, 'h:mm a')}–${format(parsed.end, 'h:mm a')}`}
      </Chip>
    );
  }
  if (parsed?.durationMinutes != null) {
    chips.push(
      <Chip key="duration" icon={Hourglass}>
        {humanizeDuration(parsed.durationMinutes)}
      </Chip>
    );
  }
  if (parsed?.recurrenceRule) {
    chips.push(
      <Chip key="recurrence" icon={Repeat}>
        {humanizeRecurrence(parsed.recurrenceRule)}
      </Chip>
    );
  }
  if (parsed?.location) {
    chips.push(
      <Chip key="location" icon={MapPin}>
        {parsed.location}
      </Chip>
    );
  }
  if (parsed && parsed.reminderMinutes.length > 0) {
    chips.push(
      <Chip key="alert" icon={Bell}>
        {parsed.reminderMinutes.map(reminderLabel).join(', ')}
      </Chip>
    );
  }
  if (parsed && parsed.attendeeEmails.length > 0) {
    chips.push(
      <Chip key="attendees" icon={Users}>
        {parsed.attendeeEmails.join(', ')}
      </Chip>
    );
  }

  const showPreview = chips.length > 0 || suggestions.length > 0;

  return (
    <div className="relative min-w-52 max-w-md flex-1">
      <Sparkles className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') submit();
        }}
        placeholder='Quick add — try "lunch with Ana tomorrow 12:30-1:30"'
        aria-label="Quick add event"
        className="h-8 pl-8 text-sm"
      />
      {showPreview && (
        <div className="absolute top-full left-0 z-10 mt-1 w-full space-y-1.5 rounded-md border bg-popover p-2 shadow-md">
          {chips.length > 0 && <div className="flex flex-wrap items-center gap-1.5">{chips}</div>}
          {suggestions.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="w-full text-xs text-muted-foreground">Suggested times</span>
              {suggestions.map((slot) => (
                <button
                  key={slot.start.toISOString()}
                  type="button"
                  onClick={() => chooseSuggestion(slot)}
                  className="rounded-md border px-2 py-1 text-xs hover:bg-accent"
                >
                  {format(slot.start, 'h:mm a')} – {format(slot.end, 'h:mm a')}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
