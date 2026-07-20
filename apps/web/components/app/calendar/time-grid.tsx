'use client';

import * as React from 'react';
import { addDays, addMinutes, endOfDay, format, isSameDay, isToday, startOfDay } from 'date-fns';
import { Check, X } from 'lucide-react';

import type { Calendar as CalendarModel, DayForecast, Event, Task } from '@calendium/shared';

import { hasTaskDrag, readTaskDragId, setTaskDragData, timeblockFromDrop } from '@/lib/task-drag';
import {
  decodeThreadDrag,
  hasThreadDrag,
  threadDropTime,
  type ThreadDragPayload,
} from '@/lib/thread-drag';
import { hourLabelInZone, tzAbbrev, zoneCaption } from '@/lib/timezones';
import { cn } from '@/lib/utils';

import { FALLBACK_COLOR, withAlpha } from './event-render';
import { JoinButton } from './join-button';
import { WeatherChip } from './weather-chip';

/** Event blocks at/above this height (px) have room for a second text row (time range + Join). */
const JOIN_BUTTON_MIN_HEIGHT = 40;

const HOUR_HEIGHT = 48; // px per hour in the time grid

export function eventTouchesDay(event: Event, day: Date): boolean {
  return new Date(event.start) < endOfDay(day) && new Date(event.end) > startOfDay(day);
}

/**
 * Event count per `yyyy-MM-dd` across `[from, to)`. Feeds the 0-3 intensity
 * dots MiniMonth renders under each day at quarter/year zoom levels, where
 * individual events are too numerous to render directly.
 */
export function buildDensityMap(events: Event[], from: Date, to: Date): Map<string, number> {
  const map = new Map<string, number>();
  for (let day = startOfDay(from); day < to; day = addDays(day, 1)) {
    const count = events.filter((e) => eventTouchesDay(e, day)).length;
    if (count > 0) map.set(format(day, 'yyyy-MM-dd'), count);
  }
  return map;
}

function hourLabel(hour: number): string {
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  return `${h12} ${hour < 12 ? 'AM' : 'PM'}`;
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
// Time grid (day + week views)
// ---------------------------------------------------------------------------

export interface TimeGridProps {
  days: Date[];
  events: Event[];
  calendarById: Map<string, CalendarModel>;
  now: Date;
  gmtLabel: string;
  /** Extra pinned IANA zones rendered as hour-label gutters left of the primary one. */
  pinnedZones?: string[];
  /** Time Travel (Task 16): an extra overlay TZ gutter, distinct from pinnedZones — transient, toggled from the calendar toolbar, not "pinned" world-clock state. */
  timeTravelZone?: string | null;
  /** Clears the Time Travel overlay from its gutter's own exit control (mirrors Esc, wired in components/app/time-travel.tsx). */
  onExitTimeTravel?: () => void;
  /** Day-keyed (yyyy-MM-dd) forecasts (M2.8 Task 13); absent → no weather chips. */
  weatherByDate?: Map<string, DayForecast>;
  onSlotClick: (start: Date) => void;
  onEventClick: (event: Event) => void;
  /** subscriptionId → feed color for read-only ICS mirrors (M2.8 Task 15). */
  subscriptionColors?: Map<string, string>;
  /** Timeblocked tasks to render as grid blocks (M2.8 Task 3). */
  tasks?: Task[];
  /** Fires when a task is dropped on a day column (drag contract in lib/task-drag.ts). */
  onTaskDrop?: (taskId: string, block: { scheduledStart: string; scheduledEnd: string }) => void;
  /** In-place check-off / reopen for a grid task block. */
  onTaskToggle?: (task: Task) => void;
  /**
   * Fires when an email thread is dropped on the grid (M2.8 Task 18; drag
   * contract in lib/thread-drag.ts). Timed drops on a day column pass the
   * 15-minute-snapped `start` with `allDay: false`; drops on a day header
   * pass local midnight with `allDay: true`.
   */
  onThreadDrop?: (payload: ThreadDragPayload, start: Date, allDay: boolean) => void;
}

export function TimeGrid({
  days,
  events,
  calendarById,
  now,
  gmtLabel,
  pinnedZones = [],
  timeTravelZone = null,
  onExitTimeTravel,
  weatherByDate,
  onSlotClick,
  onEventClick,
  subscriptionColors,
  tasks,
  onTaskDrop,
  onTaskToggle,
  onThreadDrop,
}: TimeGridProps) {
  const scrollRef = React.useRef<HTMLDivElement | null>(null);

  // Start the viewport around 7:30 AM, like every calendar app.
  React.useEffect(() => {
    scrollRef.current?.scrollTo({ top: HOUR_HEIGHT * 7.5 });
  }, []);

  const timed = React.useMemo(() => events.filter((e) => !e.allDay), [events]);
  const allDay = React.useMemo(() => events.filter((e) => e.allDay), [events]);
  const gridTemplateColumns = `repeat(${days.length}, minmax(0, 1fr))`;
  // Anchor for pinned-zone label math: the first displayed day. In day view
  // this is the only day, so labels are exact by construction. In week view
  // the shared gutter can only show one label per hour, so it's rendered
  // from this single reference day — but that anchoring is only *silently*
  // correct when every displayed day agrees with it. `pinnedZoneNonUniform`
  // below checks that agreement per zone and, when a mid-week DST transition
  // in that zone breaks it, flags the header caption instead of letting the
  // gutter show a wall-clock label that's wrong for the later (or earlier)
  // days it's shared with.
  const tzReferenceDay = days[0] ?? now;
  const pinnedZoneNonUniform = React.useMemo(() => {
    const flags = new Map<string, boolean>();
    for (const zone of pinnedZones) {
      flags.set(zone, !zoneLabelsUniformAcrossDays(zone, days, tzReferenceDay));
    }
    return flags;
  }, [pinnedZones, days, tzReferenceDay]);

  return (
    <div ref={scrollRef} className="relative flex-1 overflow-y-auto">
      {/* Sticky day header */}
      <div className="sticky top-0 z-30 border-b bg-background">
        <div className="flex">
          {timeTravelZone && (
            <TimeTravelHeaderCaption
              zone={timeTravelZone}
              at={now}
              onExit={onExitTimeTravel}
            />
          )}
          {pinnedZones.map((zone) => (
            <TzHeaderCaption
              key={zone}
              zone={zone}
              at={now}
              nonUniform={pinnedZoneNonUniform.get(zone) ?? false}
            />
          ))}
          <div className="flex w-16 shrink-0 items-end justify-end pr-2 pb-1.5">
            <span className="text-[10px] font-medium text-muted-foreground tabular-nums">
              {gmtLabel}
            </span>
          </div>
          <div className="grid flex-1" style={{ gridTemplateColumns }}>
            {days.map((day) => {
              const dayAllDay = allDay.filter((e) => eventTouchesDay(e, day));
              return (
                // biome-ignore lint/a11y/noStaticElementInteractions: drop-only target for the email-to-event drag; keyboard users create all-day events via the dialog's All day switch.
                <div
                  key={day.toISOString()}
                  data-testid={`day-header-${format(day, 'yyyy-MM-dd')}`}
                  className="border-l px-1.5 pt-2 pb-1.5 text-center"
                  onDragOver={(e) => {
                    // All-day drop target for email threads (Task 18).
                    if (onThreadDrop && hasThreadDrag(e.dataTransfer)) {
                      e.preventDefault();
                      e.dataTransfer.dropEffect = 'copy';
                    }
                  }}
                  onDrop={(e) => {
                    if (!onThreadDrop) return;
                    const payload = decodeThreadDrag(e.dataTransfer);
                    if (!payload) return;
                    e.preventDefault();
                    e.stopPropagation();
                    onThreadDrop(payload, startOfDay(day), true);
                  }}
                >
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
                  <WeatherChip
                    forecast={weatherByDate?.get(format(day, 'yyyy-MM-dd'))}
                    className="mt-0.5"
                  />
                  {dayAllDay.slice(0, 2).map((event) => {
                    const color = event.subscriptionId
                      ? (subscriptionColors?.get(event.subscriptionId) ?? FALLBACK_COLOR)
                      : (calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR);
                    return (
                      <button
                        key={event.id}
                        type="button"
                        onClick={() => onEventClick(event)}
                        className={cn(
                          'mt-1 block w-full truncate rounded px-1.5 py-0.5 text-left text-[10px] font-medium',
                          event.subscriptionId && 'border border-dashed'
                        )}
                        style={{
                          backgroundColor: withAlpha(color, 0.18),
                          color,
                          ...(event.subscriptionId ? { borderColor: color } : null),
                        }}
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
        {timeTravelZone && (
          <TzGutter key={`time-travel-${timeTravelZone}`} zone={timeTravelZone} referenceDay={tzReferenceDay} testId="tz-gutter-timetravel" />
        )}
        {pinnedZones.map((zone) => (
          <TzGutter key={zone} zone={zone} referenceDay={tzReferenceDay} />
        ))}
        {/* 24h time gutter (primary/local zone) */}
        <div
          data-testid="time-gutter-primary"
          className="relative w-16 shrink-0 select-none"
          style={{ height: 24 * HOUR_HEIGHT }}
        >
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
              subscriptionColors={subscriptionColors}
              tasks={tasks}
              onTaskDrop={onTaskDrop}
              onTaskToggle={onTaskToggle}
              onThreadDrop={onThreadDrop}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Pinned-timezone gutters
// ---------------------------------------------------------------------------

/**
 * True when `hourLabelInZone(day, hour, zone)` agrees, for every displayed
 * `day` and every rendered hour, with what it returns for `referenceDay` —
 * i.e. the single reference-day gutter is a faithful stand-in for every day
 * it's shared with. This is false only when a DST transition inside `zone`
 * falls somewhere in the displayed range, shifting its UTC offset partway
 * through the week.
 */
function zoneLabelsUniformAcrossDays(zone: string, days: Date[], referenceDay: Date): boolean {
  for (let hour = 1; hour <= 23; hour++) {
    const reference = hourLabelInZone(referenceDay, hour, zone);
    for (const day of days) {
      const candidate = hourLabelInZone(day, hour, zone);
      if (candidate.label !== reference.label || candidate.dayShift !== reference.dayShift) {
        return false;
      }
    }
  }
  return true;
}

function TzHeaderCaption({
  zone,
  at,
  nonUniform,
}: {
  zone: string;
  at: Date;
  nonUniform: boolean;
}) {
  const { city, gmt } = zoneCaption(zone, at);
  return (
    <div
      data-testid={`tz-header-${zone}`}
      className="flex w-14 shrink-0 flex-col items-end justify-end pr-2 pb-1.5 text-right"
    >
      <span className="truncate text-[10px] font-medium text-muted-foreground">
        {city}
        {nonUniform && (
          <sup
            data-testid={`tz-dst-marker-${zone}`}
            title="This zone's offset changes during the displayed week — hour labels are anchored to the first day and may be off by one hour on later days."
            className="ml-0.5 font-semibold text-muted-foreground/70"
          >
            {' '}
            ±DST
          </sup>
        )}
      </span>
      <span className="text-[9px] text-muted-foreground/70 tabular-nums">{gmt}</span>
    </div>
  );
}

/**
 * Time Travel's own gutter header: same city/GMT caption as a pinned zone's
 * TzHeaderCaption, plus the abbreviation ("EDT") and an inline exit control
 * (brief: "an 'exit' chip") — kept separate from TzHeaderCaption since pinned
 * zones have no such affordance.
 */
function TimeTravelHeaderCaption({
  zone,
  at,
  onExit,
}: {
  zone: string;
  at: Date;
  onExit?: () => void;
}) {
  const { city, gmt } = zoneCaption(zone, at);
  const abbrev = tzAbbrev(zone, at);
  return (
    <div
      data-testid="tz-header-timetravel"
      className="flex w-16 shrink-0 flex-col items-end justify-end gap-0.5 pr-2 pb-1.5 text-right"
    >
      <span className="flex items-center gap-1 truncate text-[10px] font-medium text-muted-foreground">
        {city} {abbrev}
        <button
          type="button"
          onClick={onExit}
          aria-label="Exit Time Travel"
          className="rounded-full p-0.5 hover:bg-accent"
        >
          <X className="size-2.5" />
        </button>
      </span>
      <span className="text-[9px] text-muted-foreground/70 tabular-nums">{gmt}</span>
    </div>
  );
}

function TzGutter({
  zone,
  referenceDay,
  testId,
}: {
  zone: string;
  referenceDay: Date;
  testId?: string;
}) {
  return (
    <div
      data-testid={testId ?? `tz-gutter-${zone}`}
      className="relative w-14 shrink-0 select-none border-r border-border/60"
      style={{ height: 24 * HOUR_HEIGHT }}
    >
      {Array.from({ length: 23 }, (_, i) => i + 1).map((hour) => {
        const { label, dayShift } = hourLabelInZone(referenceDay, hour, zone);
        return (
          <span
            key={hour}
            className="absolute right-2 -translate-y-1/2 text-[10px] text-muted-foreground tabular-nums"
            style={{ top: hour * HOUR_HEIGHT }}
          >
            {label}
            {dayShift !== 0 && (
              <sup
                data-testid={`tz-dayshift-${hour}`}
                className="ml-0.5 text-[8px] font-semibold text-muted-foreground/70"
              >
                {dayShift > 0 ? '+1' : '-1'}
              </sup>
            )}
          </span>
        );
      })}
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
  subscriptionColors?: Map<string, string>;
  tasks?: Task[];
  onTaskDrop?: (taskId: string, block: { scheduledStart: string; scheduledEnd: string }) => void;
  onTaskToggle?: (task: Task) => void;
  onThreadDrop?: (payload: ThreadDragPayload, start: Date, allDay: boolean) => void;
}

function DayColumn({
  day,
  events,
  calendarById,
  now,
  onSlotClick,
  onEventClick,
  subscriptionColors,
  tasks,
  onTaskDrop,
  onTaskToggle,
  onThreadDrop,
}: DayColumnProps) {
  const positioned = React.useMemo(
    () => layoutDayEvents(day, events.filter((e) => eventTouchesDay(e, day))),
    [day, events]
  );
  const showNow = isSameDay(day, now);
  const nowTop = ((now.getHours() * 60 + now.getMinutes()) / 60) * HOUR_HEIGHT;

  // Timeblocked tasks for this day (M2.8 Task 3), positioned like events.
  const dayTasks = React.useMemo(
    () =>
      (tasks ?? []).filter(
        (t) => t.scheduledStart && isSameDay(new Date(t.scheduledStart), day)
      ),
    [tasks, day]
  );

  // Click an empty slot -> quick create, snapped to 30 minutes.
  const handleBackgroundClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const minutes = ((e.clientY - rect.top) / HOUR_HEIGHT) * 60;
    const snapped = Math.max(0, Math.min(23.5 * 60, Math.floor(minutes / 30) * 30));
    onSlotClick(addMinutes(startOfDay(day), snapped));
  };

  return (
    <div
      data-testid={`day-column-${format(day, 'yyyy-MM-dd')}`}
      className="relative cursor-pointer border-l"
      style={{ height: 24 * HOUR_HEIGHT }}
      onClick={handleBackgroundClick}
      onDragOver={(e) => {
        // Same drop affordance for both drag sources (tasks: Task 3,
        // email threads: Task 18) — accept + a matching drop cursor.
        if (onTaskDrop && hasTaskDrag(e.dataTransfer)) {
          e.preventDefault();
          e.dataTransfer.dropEffect = 'move';
        } else if (onThreadDrop && hasThreadDrag(e.dataTransfer)) {
          e.preventDefault();
          e.dataTransfer.dropEffect = 'copy';
        }
      }}
      onDrop={(e) => {
        const rect = e.currentTarget.getBoundingClientRect();
        const taskId = onTaskDrop ? readTaskDragId(e.dataTransfer) : null;
        if (taskId) {
          e.preventDefault();
          e.stopPropagation();
          onTaskDrop!(taskId, timeblockFromDrop(day, e.clientY, rect.top));
          return;
        }
        const payload = onThreadDrop ? decodeThreadDrag(e.dataTransfer) : null;
        if (payload) {
          e.preventDefault();
          e.stopPropagation();
          onThreadDrop!(payload, threadDropTime(day, e.clientY, rect.top), false);
        }
      }}
    >
      {Array.from({ length: 24 }, (_, hour) => (
        <div
          key={hour}
          className="pointer-events-none absolute inset-x-0 border-t border-border/60"
          style={{ top: hour * HOUR_HEIGHT }}
        />
      ))}

      {positioned.map(({ event, top, height, leftPct, widthPct }) => {
        const color = event.subscriptionId
          ? (subscriptionColors?.get(event.subscriptionId) ?? FALLBACK_COLOR)
          : (calendarById.get(event.calendarId)?.color ?? FALLBACK_COLOR);
        return (
          // biome-ignore lint/a11y/useSemanticElements: hosts a real <button> (JoinButton) inline — nesting a button inside a button is invalid HTML, so this outer element is a div with button semantics instead.
          <div
            key={event.id}
            role="button"
            tabIndex={0}
            aria-label={event.title}
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
            className={cn(
              'absolute z-10 flex cursor-pointer flex-col overflow-hidden rounded-md border-l-[3px] px-1.5 py-1 text-left leading-tight shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-ring',
              event.status === 'tentative' && 'opacity-70',
              // ICS feed mirrors are visually distinct: dashed outline in the feed color.
              event.subscriptionId && 'border border-dashed'
            )}
            style={{
              top: top + 1,
              height: Math.max(height - 2, 18),
              left: `calc(${leftPct}% + 1px)`,
              width: `calc(${widthPct}% - 3px)`,
              backgroundColor: withAlpha(color, 0.16),
              borderLeftColor: color,
              ...(event.subscriptionId ? { borderColor: color, borderLeftStyle: 'solid' } : null),
            }}
          >
            <span className="truncate text-xs font-medium" style={{ color }}>
              {event.title}
            </span>
            {height >= JOIN_BUTTON_MIN_HEIGHT && (
              <span className="flex items-center gap-1.5 text-[10px]" style={{ color: withAlpha(color, 0.75) }}>
                <span className="truncate">
                  {format(new Date(event.start), 'h:mm')} –{' '}
                  {format(new Date(event.end), 'h:mm a')}
                </span>
                <JoinButton event={event} now={now} size="sm" />
              </span>
            )}
          </div>
        );
      })}

      {dayTasks.map((task) => {
        const start = new Date(task.scheduledStart as string);
        const end = new Date(task.scheduledEnd as string);
        const startMinutes = start.getHours() * 60 + start.getMinutes();
        const durationMinutes = Math.max(15, (end.getTime() - start.getTime()) / 60000);
        const completed = task.completedAt !== null;
        return (
          // Distinct rounded task block: draggable (reschedule / drag back to
          // the rail) with in-place check-off — clicking never opens a dialog.
          // biome-ignore lint/a11y/noStaticElementInteractions: drag source whose only click handler stops slot-create propagation; the real control is the button inside.
          // biome-ignore lint/a11y/useKeyWithClickEvents: the click handler only stops propagation — keyboard users act on the inner checkbox button.
          <div
            key={task.id}
            data-testid={`grid-task-${task.id}`}
            draggable
            onDragStart={(e) => setTaskDragData(e.dataTransfer, task.id)}
            onClick={(e) => e.stopPropagation()}
            className={cn(
              'absolute inset-x-0.5 z-10 flex cursor-grab items-start gap-1.5 overflow-hidden rounded-lg border border-primary/40 bg-primary/10 px-1.5 py-1 leading-tight shadow-sm',
              completed && 'opacity-60'
            )}
            style={{
              top: (startMinutes / 60) * HOUR_HEIGHT + 1,
              height: Math.max((durationMinutes / 60) * HOUR_HEIGHT - 2, 18),
            }}
          >
            {/* biome-ignore lint/a11y/useSemanticElements: styled round check control; the surrounding block must stay a draggable div. */}
            <button
              type="button"
              role="checkbox"
              aria-checked={completed}
              aria-label={completed ? `Reopen "${task.title}"` : `Complete "${task.title}"`}
              onClick={(e) => {
                e.stopPropagation();
                onTaskToggle?.(task);
              }}
              className={cn(
                'mt-px flex size-3.5 shrink-0 items-center justify-center rounded-full border transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring',
                completed
                  ? 'border-primary bg-primary text-primary-foreground'
                  : 'border-primary/60 hover:bg-primary/20'
              )}
            >
              {completed && <Check className="size-2.5" />}
            </button>
            <span
              className={cn(
                'truncate text-xs font-medium text-primary',
                completed && 'line-through'
              )}
            >
              {task.title}
            </span>
          </div>
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

