'use client';

import * as React from 'react';

import { cn } from '@/lib/utils';

export interface ZeroScene {
  id: string;
  headline: string;
  sub: string;
  gradient: string;
}

/** Rotating end-of-triage artwork — pure CSS gradients, no image payloads. */
export const ZERO_SCENES: readonly ZeroScene[] = [
  { id: 'dawn', headline: 'Clear skies ahead', sub: 'Every conversation handled. Go make something.', gradient: 'from-sky-200 via-indigo-100 to-rose-100 dark:from-sky-950 dark:via-indigo-950 dark:to-rose-950' },
  { id: 'dunes', headline: 'Nothing but calm', sub: 'Your inbox is a quiet desert. Enjoy it.', gradient: 'from-amber-100 via-orange-100 to-rose-100 dark:from-amber-950 dark:via-orange-950 dark:to-rose-950' },
  { id: 'sea', headline: 'Smooth sailing', sub: 'Zero unhandled mail. The horizon is yours.', gradient: 'from-cyan-100 via-teal-100 to-emerald-100 dark:from-cyan-950 dark:via-teal-950 dark:to-emerald-950' },
  { id: 'alpine', headline: 'Peak performance', sub: 'You cleared the whole climb. Breathe it in.', gradient: 'from-slate-100 via-sky-100 to-violet-100 dark:from-slate-900 dark:via-sky-950 dark:to-violet-950' },
  { id: 'aurora', headline: 'Lights out, inbox down', sub: 'All quiet. See you when something matters.', gradient: 'from-emerald-100 via-cyan-100 to-fuchsia-100 dark:from-emerald-950 dark:via-cyan-950 dark:to-fuchsia-950' },
];

export function sceneForDate(date: Date): ZeroScene {
  // UTC-aware day-of-year calculation to ensure consistent scene rotation
  const year = date.getUTCFullYear();
  const start = new Date(Date.UTC(year, 0, 1));
  const day = Math.floor((date.getTime() - start.getTime()) / (24 * 60 * 60 * 1000));
  return ZERO_SCENES[day % ZERO_SCENES.length]!;
}

/** Full-pane celebration shown when a split hits zero (no query, no view). */
export function InboxZero({ date }: { date?: Date }) {
  const scene = sceneForDate(date ?? new Date());
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
      <div
        aria-hidden
        className={cn('h-40 w-64 rounded-2xl bg-gradient-to-br shadow-inner', scene.gradient)}
      />
      <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
        You&apos;re at Inbox Zero
      </p>
      <p className="text-lg font-semibold">{scene.headline}</p>
      <p className="text-muted-foreground max-w-xs text-sm text-balance">{scene.sub}</p>
    </div>
  );
}
