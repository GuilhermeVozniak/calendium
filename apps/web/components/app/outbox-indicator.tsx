'use client';

import * as React from 'react';
import type { OutboxEntry } from '@calendium/shared';
import { AlertTriangle, CloudOff, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { getOutbox } from '@/lib/offline/queue';

/**
 * Honest outbox surface: the pill reflects the REAL queued-entry count from
 * the durable outbox (never an optimistic guess), and conflict/failed entries
 * stay listed until the user dismisses them. Replay is at-least-once and may
 * be interrupted — this never claims "all synced"; it simply disappears once
 * the outbox is genuinely empty.
 */
export function OutboxIndicator() {
  const [entries, setEntries] = React.useState<readonly OutboxEntry[]>([]);

  React.useEffect(() => {
    const outbox = getOutbox();
    // The outbox hands subscribers its LIVE entry array — copy before storing
    // so React state stays immutable and re-renders fire on every change.
    setEntries([...outbox.pending]);
    return outbox.subscribe((live) => setEntries([...live]));
  }, []);

  const queued = entries.filter((e) => e.status === 'queued').length;
  const problems = entries
    .filter((e) => e.status === 'conflict' || e.status === 'failed')
    .sort((a, b) => a.seq - b.seq);

  if (queued === 0 && problems.length === 0) return null;

  return (
    <div className="shrink-0 border-b" data-testid="outbox-indicator">
      {queued > 0 && (
        <div className="bg-muted text-muted-foreground flex items-center gap-2 px-4 py-1.5 text-xs">
          <CloudOff className="size-3.5 shrink-0" />
          <span data-testid="outbox-queued-count">
            {queued} queued — will sync when the connection is back
          </span>
        </div>
      )}
      {problems.map((entry) => (
        <div
          key={entry.id}
          className="text-destructive flex items-center gap-2 border-t px-4 py-1 text-xs"
        >
          <AlertTriangle className="size-3.5 shrink-0" />
          <span className="min-w-0 flex-1 truncate">{describeEntry(entry)}</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1 px-2 text-xs"
            onClick={() => void getOutbox().dismiss(entry.id)}
          >
            <X className="size-3" />
            Dismiss
          </Button>
        </div>
      ))}
    </div>
  );
}

function describeEntry(entry: OutboxEntry): string {
  const status =
    entry.status === 'failed'
      ? "couldn't sync after several attempts"
      : 'was rejected by the server';
  let what: string;
  switch (entry.action.kind) {
    case 'thread_action':
      what = `"${entry.action.action.replace(/_/g, ' ')}"`;
      break;
    case 'thread_snooze':
      what = 'A snooze';
      break;
    case 'thread_reminder':
      what = 'A reminder';
      break;
    case 'thread_open':
      what = 'A read receipt';
      break;
    case 'draft_save':
      what = 'A draft';
      break;
    case 'draft_send':
      what = 'A queued message';
      break;
  }
  const why = entry.lastError ? ` — ${entry.lastError}` : '';
  return `${what} ${status}${why}`;
}
