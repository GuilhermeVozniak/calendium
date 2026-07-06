import { CalendarCheck, Link2, Video } from 'lucide-react';

import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { cn } from '@/lib/utils';
import { SectionHeading } from './section-heading';

const DAY_START = 9;
const DAY_SPAN = 6; // visible window: 9:00 → 15:00

type EventKind = 'default' | 'focus' | 'active' | 'shared';

const events: {
  day: number;
  start: number;
  end: number;
  title: string;
  kind?: EventKind;
  meet?: boolean;
}[] = [
  { day: 0, start: 9.5, end: 10.25, title: 'Standup + triage' },
  { day: 0, start: 12, end: 13, title: 'Lunch — Sam' },
  { day: 1, start: 9.5, end: 11.5, title: 'Deep work', kind: 'focus' },
  { day: 1, start: 12, end: 13, title: 'Open', kind: 'shared' },
  { day: 1, start: 13.5, end: 14.5, title: 'Portfolio review' },
  { day: 2, start: 10, end: 10.75, title: '1:1 Maya', meet: true },
  { day: 2, start: 13, end: 14, title: 'Roadmap review', kind: 'active', meet: true },
  { day: 3, start: 11.5, end: 12.25, title: 'Board prep' },
  { day: 3, start: 13, end: 14.5, title: 'Open', kind: 'shared' },
  { day: 4, start: 9.5, end: 10.25, title: 'Ship review', meet: true },
  { day: 4, start: 11, end: 12, title: 'Open', kind: 'shared' },
];

const days = [
  { label: 'Mon', date: 6 },
  { label: 'Tue', date: 7 },
  { label: 'Wed', date: 8 },
  { label: 'Thu', date: 9, today: true },
  { label: 'Fri', date: 10 },
];

const hourLines = [10, 11, 12, 13, 14];

const kindClasses: Record<EventKind, string> = {
  default: 'border bg-background shadow-xs',
  focus: 'border border-dashed bg-muted/50 text-muted-foreground',
  active: 'bg-primary text-primary-foreground shadow-md',
  shared: 'border border-dashed border-foreground/40 bg-foreground/[0.04] text-muted-foreground',
};

function fmt(hour: number): string {
  const h = Math.floor(hour);
  const m = Math.round((hour - h) * 60);
  return `${h}:${m.toString().padStart(2, '0')}`;
}

const features = [
  {
    icon: Link2,
    title: 'Availability as text',
    copy: 'Pick slots on the grid; they paste anywhere as plain text plus a booking link.',
  },
  {
    icon: CalendarCheck,
    title: 'RSVP from the inbox',
    copy: 'Invites arrive as cards — accept, decline, or propose a new time with one key.',
  },
  {
    icon: Video,
    title: 'Conferencing, attached',
    copy: 'Meet or Teams links are added automatically the moment an event has attendees.',
  },
];

export function CalendarSection() {
  return (
    <section className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-24 md:py-32">
        <div className="grid items-end gap-10 lg:grid-cols-[1.2fr_1fr] lg:gap-20">
          <SectionHeading
            eyebrow="Calendar"
            keys={['G', 'C']}
            title="A week you can type at."
            lede="The calendar lives beside your mail, not behind another tab. Create events in plain language, move them with arrow keys, and share availability as neat, copy-pasteable slots."
          />
          <ul className="space-y-5">
            {features.map((feature) => (
              <li key={feature.title} className="flex gap-4">
                <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-md border bg-muted/40">
                  <feature.icon className="size-4" strokeWidth={1.75} />
                </span>
                <div>
                  <p className="text-sm font-medium">{feature.title}</p>
                  <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                    {feature.copy}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </div>

        {/* Week grid mock */}
        <div aria-hidden className="mt-14 overflow-hidden rounded-xl border bg-card shadow-sm">
          <div className="grid grid-cols-[2.75rem_repeat(5,minmax(0,1fr))] border-b">
            <div />
            {days.map((day) => (
              <div
                key={day.label}
                className={cn(
                  'flex items-baseline gap-1.5 border-l px-2 py-2 text-[11px]',
                  day.today ? 'font-semibold' : 'text-muted-foreground'
                )}>
                {day.label}
                <span className="font-mono text-[10px] tabular-nums">{day.date}</span>
                {day.today ? <span className="size-1 rounded-full bg-foreground" /> : null}
              </div>
            ))}
          </div>

          <div className="relative h-72 sm:h-80">
            {/* Hour lines + gutter labels */}
            {hourLines.map((hour) => {
              const top = ((hour - DAY_START) / DAY_SPAN) * 100;
              return (
                <div key={hour}>
                  <span
                    className="absolute left-0 w-10 -translate-y-1/2 pr-1 text-right font-mono text-[9px] tabular-nums text-muted-foreground"
                    style={{ top: `${top}%` }}>
                    {hour}:00
                  </span>
                  <span
                    className="absolute left-11 right-0 h-px bg-border/60"
                    style={{ top: `${top}%` }}
                  />
                </div>
              );
            })}

            {/* Day columns */}
            <div className="absolute inset-y-0 left-11 right-0 grid grid-cols-5">
              {days.map((day, dayIndex) => (
                <div key={day.label} className="relative border-l">
                  {events
                    .filter((event) => event.day === dayIndex)
                    .map((event) => {
                      const top = ((event.start - DAY_START) / DAY_SPAN) * 100;
                      const height = ((event.end - event.start) / DAY_SPAN) * 100;
                      const kind = event.kind ?? 'default';
                      return (
                        <div
                          key={event.title + event.start}
                          className={cn(
                            'absolute inset-x-1 overflow-hidden rounded-md px-1.5 py-1',
                            kindClasses[kind]
                          )}
                          style={{ top: `${top}%`, height: `${height}%` }}>
                          <p className="flex items-center gap-1 truncate text-[10px] font-medium leading-tight">
                            {event.title}
                            {event.meet ? <Video className="size-2.5 shrink-0" /> : null}
                          </p>
                          <p
                            className={cn(
                              'truncate font-mono text-[9px] tabular-nums',
                              kind === 'active'
                                ? 'text-primary-foreground/70'
                                : 'text-muted-foreground'
                            )}>
                            {fmt(event.start)}–{fmt(event.end)}
                          </p>
                        </div>
                      );
                    })}
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-2 text-xs text-muted-foreground">
          <span className="flex items-center gap-2">
            <span className="h-3 w-5 rounded-sm border border-dashed border-foreground/40 bg-foreground/[0.04]" />
            Shared availability — pastes as text + booking link
          </span>
          <span className="flex items-center gap-2">
            <KbdGroup size="sm" keys={['⌘', '⇧', 'A']} />
            share availability
          </span>
          <span className="flex items-center gap-2">
            <Kbd size="sm">←</Kbd>
            <Kbd size="sm">→</Kbd>
            move focus
          </span>
        </div>
      </div>
    </section>
  );
}
