'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { format } from 'date-fns';
import { X } from 'lucide-react';

import type { TimeInsights } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';

/**
 * Time insights panel (M2.8 Task 17): a right-side sheet over the calendar
 * showing how [from, to) was spent — meeting-vs-focus split bar, stat tiles,
 * per-day mini bars, and top people. Built with plain divs per the app's
 * dataviz guidance (thin marks, 2px gaps between fills, text in text tokens,
 * series color only on swatches/marks); no chart library.
 *
 * Series colors are the design system's categorical chart tokens in fixed
 * order: chart-1 = meetings, chart-2 = focus — never reassigned by rank.
 */

export interface InsightsPanelProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  from: Date;
  to: Date;
}

/** "45m", "2h", "2h 30m". */
export function formatMinutes(mins: number): string {
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  if (h === 0) return `${m}m`;
  if (m === 0) return `${h}h`;
  return `${h}h ${m}m`;
}

/** Deterministic sample document for explicit demo mode (lib/demo.ts). */
export function demoTimeInsights(from: Date, to: Date): TimeInsights {
  const byDay: TimeInsights['byDay'] = [];
  let meetingMinutes = 0;
  let focusMinutes = 0;
  let meetingCount = 0;
  for (let d = new Date(from); d < to; d.setDate(d.getDate() + 1)) {
    const weekday = d.getDay();
    const working = weekday !== 0 && weekday !== 6;
    const meeting = working ? 60 + (weekday % 3) * 45 : 0;
    const focus = working ? 90 + (weekday % 2) * 30 : 0;
    byDay.push({ date: format(d, 'yyyy-MM-dd'), meetingMinutes: meeting, focusMinutes: focus });
    meetingMinutes += meeting;
    focusMinutes += focus;
    if (working) meetingCount += 2;
  }
  return {
    from: from.toISOString(),
    to: to.toISOString(),
    meetingMinutes,
    focusMinutes,
    taskMinutes: Math.round(focusMinutes / 3),
    meetingCount,
    focusGoalMinutes: 600,
    topPeople: [
      { email: 'alice@example.com', name: 'Alice Chen', meetings: 4, minutes: 240 },
      { email: 'bob@example.com', name: 'Bob Iyer', meetings: 3, minutes: 135 },
      { email: 'carol@example.com', name: 'Carol Diaz', meetings: 1, minutes: 30 },
    ],
    byDay,
  };
}

/**
 * Fetches insights for [from, to); in explicit demo mode only, a failure
 * falls back to the deterministic sample document (never outside it).
 */
export async function fetchTimeInsights(from: Date, to: Date): Promise<TimeInsights> {
  try {
    return await getApiClient().getTimeInsights(from.toISOString(), to.toISOString());
  } catch (err) {
    if (DEMO_MODE) return demoTimeInsights(from, to);
    throw err;
  }
}

function initials(name: string, email: string): string {
  const source = name.trim() || email;
  const parts = source.split(/[\s._@-]+/).filter(Boolean);
  return parts
    .slice(0, 2)
    .map((p) => p[0]!.toUpperCase())
    .join('');
}

function Tile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/30 px-3 py-2">
      <div className="text-lg font-semibold tabular-nums tracking-tight">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

export function InsightsPanel({ open, onOpenChange, from, to }: InsightsPanelProps) {
  // Escape closes the sheet (keyboard parity with the ⇧I toggle).
  React.useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onOpenChange(false);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onOpenChange]);

  const query = useQuery({
    queryKey: ['time-insights', from.toISOString(), to.toISOString()],
    queryFn: () => fetchTimeInsights(from, to),
    enabled: open,
    retry: false,
  });

  if (!open) return null;
  const insights = query.data;

  const total = (insights?.meetingMinutes ?? 0) + (insights?.focusMinutes ?? 0);
  const maxDay = Math.max(
    1,
    ...(insights?.byDay ?? []).map((d) => Math.max(d.meetingMinutes, d.focusMinutes))
  );

  return (
    <div
      role="dialog"
      aria-label="Time insights"
      data-state="open"
      className="fixed inset-y-0 right-0 z-50 flex w-[380px] max-w-[92vw] flex-col overflow-y-auto border-l bg-background shadow-lg"
    >
      <header className="flex items-start justify-between gap-2 border-b px-4 py-3">
        <div>
          <h2 className="text-sm font-semibold tracking-tight">Time insights</h2>
          <p className="text-xs text-muted-foreground">
            {format(from, 'MMM d')} – {format(new Date(to.getTime() - 1), 'MMM d, yyyy')}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          aria-label="Close insights"
          onClick={() => onOpenChange(false)}
        >
          <X className="size-4" />
        </Button>
      </header>

      <div className="flex flex-col gap-5 px-4 py-4">
        {query.isLoading && (
          <p className="text-sm text-muted-foreground">Crunching your calendar…</p>
        )}
        {query.isError && (
          <p className="text-sm text-muted-foreground">Couldn&apos;t load insights.</p>
        )}

        {insights && (
          <>
            {/* Stat tiles */}
            <div className="grid grid-cols-2 gap-2">
              <Tile label="Meeting time" value={formatMinutes(insights.meetingMinutes)} />
              <Tile label="Meetings" value={String(insights.meetingCount)} />
              <Tile label="Focus time" value={formatMinutes(insights.focusMinutes)} />
              <Tile label="Task blocks" value={formatMinutes(insights.taskMinutes)} />
            </div>

            {/* Meetings vs focus split bar */}
            <section aria-label="Meetings vs focus">
              <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                Meetings vs focus
              </h3>
              {total > 0 ? (
                <>
                  <div className="flex h-3 w-full gap-0.5 overflow-hidden rounded-full bg-muted/40">
                    {insights.meetingMinutes > 0 && (
                      <div
                        data-testid="split-meeting"
                        className="rounded-full bg-chart-1"
                        style={{ width: `${(insights.meetingMinutes / total) * 100}%` }}
                        title={`Meetings: ${formatMinutes(insights.meetingMinutes)}`}
                      />
                    )}
                    {insights.focusMinutes > 0 && (
                      <div
                        data-testid="split-focus"
                        className="rounded-full bg-chart-2"
                        style={{ width: `${(insights.focusMinutes / total) * 100}%` }}
                        title={`Focus: ${formatMinutes(insights.focusMinutes)}`}
                      />
                    )}
                  </div>
                  <div className="mt-2 flex items-center gap-4 text-xs">
                    <span className="inline-flex items-center gap-1.5">
                      <span aria-hidden className="size-2 rounded-full bg-chart-1" />
                      Meetings
                      <span className="tabular-nums text-muted-foreground">
                        {formatMinutes(insights.meetingMinutes)}
                      </span>
                    </span>
                    <span className="inline-flex items-center gap-1.5">
                      <span aria-hidden className="size-2 rounded-full bg-chart-2" />
                      Focus
                      <span className="tabular-nums text-muted-foreground">
                        {formatMinutes(insights.focusMinutes)}
                      </span>
                    </span>
                  </div>
                </>
              ) : (
                <p className="text-sm text-muted-foreground">
                  No meetings or focus time in this range.
                </p>
              )}
            </section>

            {/* Focus goal progress */}
            {insights.focusGoalMinutes > 0 && (
              <section aria-label="Focus goal">
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">Focus goal</h3>
                <div className="h-2 w-full overflow-hidden rounded-full bg-muted/40">
                  <div
                    className="h-full rounded-full bg-chart-2"
                    style={{
                      width: `${Math.min(
                        100,
                        (insights.focusMinutes / insights.focusGoalMinutes) * 100
                      )}%`,
                    }}
                  />
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {formatMinutes(insights.focusMinutes)} of{' '}
                  {formatMinutes(insights.focusGoalMinutes)} goal
                </p>
              </section>
            )}

            {/* Per-day mini bars */}
            {insights.byDay.length > 0 && (
              <section aria-label="By day">
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">By day</h3>
                <div className="flex h-20 items-end gap-1">
                  {insights.byDay.map((d) => {
                    const date = new Date(`${d.date}T00:00:00`);
                    return (
                      <div
                        key={d.date}
                        className="flex min-w-0 flex-1 flex-col items-center gap-1"
                        title={`${format(date, 'EEE MMM d')} — ${formatMinutes(
                          d.meetingMinutes
                        )} meetings, ${formatMinutes(d.focusMinutes)} focus`}
                      >
                        <div className="flex h-14 w-full items-end justify-center gap-0.5">
                          <div
                            className="w-1.5 rounded-t-sm bg-chart-1"
                            style={{ height: `${(d.meetingMinutes / maxDay) * 100}%` }}
                          />
                          <div
                            className="w-1.5 rounded-t-sm bg-chart-2"
                            style={{ height: `${(d.focusMinutes / maxDay) * 100}%` }}
                          />
                        </div>
                        {insights.byDay.length <= 14 && (
                          <span className="text-[10px] text-muted-foreground">
                            {format(date, 'EEEEE')}
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              </section>
            )}

            {/* Top people */}
            <section aria-label="Top people">
              <h3 className="mb-2 text-xs font-medium text-muted-foreground">Top people</h3>
              {insights.topPeople.length === 0 ? (
                <p className="text-sm text-muted-foreground">No meetings with others yet.</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {insights.topPeople.map((p) => (
                    <li key={p.email} className="flex items-center gap-2.5">
                      <span
                        aria-hidden
                        className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-[10px] font-medium"
                      >
                        {initials(p.name, p.email)}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-sm" title={p.email}>
                        {p.name || p.email}
                      </span>
                      <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                        {p.meetings} {p.meetings === 1 ? 'meeting' : 'meetings'} ·{' '}
                        {formatMinutes(p.minutes)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </div>
    </div>
  );
}
