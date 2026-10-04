import {
  CalendarDays,
  Clock3,
  FileText,
  Inbox,
  Search,
  Send,
  Settings,
  Star,
  Video,
} from 'lucide-react';

import { Kbd } from '@/components/ui/kbd';
import { cn } from '@/lib/utils';

const splits = [
  { label: 'Important', count: 3, active: true },
  { label: 'VIP', count: 1 },
  { label: 'Team', count: 4 },
  { label: 'Calendar', count: 2 },
  { label: 'News', count: 12 },
];

const threads = [
  {
    sender: 'Maya Chen',
    subject: 'Q3 board deck',
    snippet: 'Final numbers are in — worth a look at slide 12 before we lock it.',
    time: '9:41',
    unread: true,
    selected: true,
  },
  {
    sender: 'Priya Natarajan',
    subject: 'Re: Offer — senior design',
    snippet: 'She said yes. Signed letter attached, start date Aug 3.',
    time: '9:12',
    unread: true,
    starred: true,
  },
  {
    sender: 'Mercury',
    subject: 'Invoice paid: $12,400',
    snippet: 'Meridian Labs paid invoice #1042.',
    time: '8:56',
  },
  {
    sender: 'Daniel Ruiz',
    subject: 'API latency regression',
    snippet: 'p95 crept to 140 ms after the deploy — bisecting now.',
    time: '8:31',
    unread: true,
  },
  {
    sender: 'Google Calendar',
    subject: 'Invite: Roadmap review',
    snippet: 'Thursday 13:00–14:00 · Google Meet',
    time: '8:02',
  },
];

const peekEvents = [
  { time: '9:00', duration: '25 min', title: 'Standup', bar: 'bg-foreground/70', meet: true },
  { time: '11:30', duration: '45 min', title: 'Design review', bar: 'bg-foreground/45' },
  { time: '13:00', duration: '60 min', title: 'Roadmap review', bar: 'bg-foreground/25', meet: true },
];

const railIcons = [Inbox, Send, FileText, CalendarDays, Search, Settings];

/**
 * Hand-built HTML/CSS mock of the Calendium client: icon rail, split inbox,
 * calendar peek, and command bar. Pure tokens — renders in light and dark.
 */
export function ProductMock({ className }: { className?: string }) {
  return (
    <div className={cn('relative', className)} aria-hidden>
      {/* Glow behind the frame */}
      <div className="pointer-events-none absolute -inset-x-10 -top-16 bottom-0 bg-[radial-gradient(55%_45%_at_50%_0%,hsl(var(--foreground)/0.08),transparent_70%)]" />

      {/* Gradient/vignette frame */}
      <div className="relative rounded-2xl border bg-gradient-to-b from-foreground/[0.06] via-foreground/[0.02] to-transparent p-1.5 shadow-2xl shadow-foreground/10">
        <div className="overflow-hidden rounded-xl border bg-card text-card-foreground">
          {/* Window chrome */}
          <div className="flex items-center gap-3 border-b px-3.5 py-2.5">
            <div className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-foreground/15" />
              <span className="size-2 rounded-full bg-foreground/15" />
              <span className="size-2 rounded-full bg-foreground/15" />
            </div>
            <span className="text-[11px] font-medium text-muted-foreground">
              Important — 3 unread
            </span>
            <span className="ml-auto font-mono text-[10px] text-muted-foreground">Thu · 9:41</span>
          </div>

          <div className="grid grid-cols-[2.5rem_minmax(0,1fr)] md:grid-cols-[2.5rem_minmax(0,1fr)_15rem]">
            {/* Icon rail */}
            <div className="flex flex-col items-center gap-1.5 border-r py-3">
              {railIcons.map((Icon, index) => (
                <span
                  key={index}
                  className={cn(
                    'grid size-7 place-items-center rounded-md',
                    index === 0 ? 'bg-accent text-foreground' : 'text-muted-foreground/60'
                  )}>
                  <Icon className="size-3.5" strokeWidth={1.75} />
                </span>
              ))}
            </div>

            {/* Thread list */}
            <div className="flex min-w-0 flex-col">
              <div className="flex items-center gap-1 overflow-x-auto border-b px-2 py-1.5">
                {splits.map((split) => (
                  <span
                    key={split.label}
                    className={cn(
                      'flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-[11px]',
                      split.active
                        ? 'bg-accent font-medium text-foreground'
                        : 'text-muted-foreground'
                    )}>
                    {split.label}
                    <span className="font-mono text-[10px] text-muted-foreground">
                      {split.count}
                    </span>
                  </span>
                ))}
              </div>

              <div className="flex-1 divide-y divide-border/60">
                {threads.map((thread) => (
                  <div
                    key={thread.subject}
                    className={cn(
                      'relative flex items-center gap-3 px-3.5 py-2.5',
                      thread.selected && 'bg-accent/70'
                    )}>
                    {thread.selected ? (
                      <span className="absolute inset-y-0 left-0 w-0.5 bg-foreground" />
                    ) : null}
                    <span
                      className={cn(
                        'size-1.5 shrink-0 rounded-full',
                        thread.unread ? 'bg-foreground' : 'bg-transparent'
                      )}
                    />
                    <span
                      className={cn(
                        'w-24 shrink-0 truncate text-[11px] sm:w-32',
                        thread.unread ? 'font-semibold' : 'text-muted-foreground'
                      )}>
                      {thread.sender}
                    </span>
                    <span className="min-w-0 flex-1 truncate text-[11px]">
                      <span className={cn(thread.unread && 'font-medium')}>{thread.subject}</span>
                      <span className="text-muted-foreground"> — {thread.snippet}</span>
                    </span>
                    {thread.selected ? (
                      <span className="hidden shrink-0 items-center gap-1 sm:flex">
                        <Kbd size="sm">E</Kbd>
                        <Kbd size="sm">H</Kbd>
                        <Kbd size="sm">↵</Kbd>
                      </span>
                    ) : null}
                    {thread.starred ? (
                      <Star className="size-3 shrink-0 fill-foreground/80 text-foreground/80" />
                    ) : null}
                    <span className="shrink-0 font-mono text-[10px] tabular-nums text-muted-foreground">
                      {thread.time}
                    </span>
                  </div>
                ))}
              </div>

              {/* Command bar */}
              <div className="flex items-center justify-between border-t px-3.5 py-2">
                <span className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                  <Kbd size="sm">⌘</Kbd>
                  <Kbd size="sm">K</Kbd>
                  for anything
                </span>
                <span className="font-mono text-[10px] text-muted-foreground">
                  38 ms · synced just now
                </span>
              </div>
            </div>

            {/* Calendar peek */}
            <div className="hidden min-w-0 flex-col gap-2 border-l p-3 md:flex">
              <div className="flex items-baseline justify-between">
                <span className="text-[11px] font-semibold">Today</span>
                <span className="font-mono text-[10px] text-muted-foreground">Thu · Jul 9</span>
              </div>
              {peekEvents.map((event) => (
                <div
                  key={event.title}
                  className="flex gap-2 rounded-md border bg-background/60 p-2">
                  <span className={cn('w-0.5 shrink-0 rounded-full', event.bar)} />
                  <div className="min-w-0">
                    <p className="truncate text-[11px] font-medium">{event.title}</p>
                    <p className="flex items-center gap-1 font-mono text-[10px] text-muted-foreground">
                      <Clock3 className="size-2.5" />
                      {event.time} · {event.duration}
                      {event.meet ? <Video className="ml-0.5 size-2.5" /> : null}
                    </p>
                  </div>
                </div>
              ))}
              <div className="mt-auto rounded-md border border-dashed p-2">
                <p className="flex items-center justify-between text-[11px] font-medium">
                  Share availability
                  <span className="flex items-center gap-1">
                    <Kbd size="sm">⌘</Kbd>
                    <Kbd size="sm">⇧</Kbd>
                    <Kbd size="sm">A</Kbd>
                  </span>
                </p>
                <p className="mt-1 font-mono text-[10px] leading-relaxed text-muted-foreground">
                  3 slots copied — Tue 14:00, Wed 9:30, Thu 16:15
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
