'use client';

import * as React from 'react';

import type { TeamThreadActivity } from '@calendium/shared';
import { Eye, Reply } from 'lucide-react';

import { getApiClient } from '@/lib/api';
import { openCollabStream, type CollabEvent } from '@/lib/collab-stream';

export interface TeamActivityChipsProps {
  threadId: string;
  /** Test seams — default to the real API client and collab stream. */
  fetchActivity?: (threadId: string) => Promise<TeamThreadActivity[]>;
  subscribe?: (onEvent: (ev: CollabEvent) => void) => () => void;
}

const defaultFetch = (threadId: string) => getApiClient().teamThreadActivity(threadId);
const defaultSubscribe = (onEvent: (ev: CollabEvent) => void) => openCollabStream(onEvent);

/**
 * Collapses activity rows (one per team × member) into one chip per
 * teammate: replied wins over merely seen, newest timestamps kept.
 * Exported for tests.
 */
export function summarizeActivity(rows: TeamThreadActivity[]): TeamThreadActivity[] {
  const byUser = new Map<string, TeamThreadActivity>();
  for (const row of rows) {
    const prev = byUser.get(row.userId);
    if (!prev) {
      byUser.set(row.userId, { ...row });
      continue;
    }
    if (row.openedAt && (!prev.openedAt || row.openedAt > prev.openedAt)) prev.openedAt = row.openedAt;
    if (row.repliedAt && (!prev.repliedAt || row.repliedAt > prev.repliedAt)) prev.repliedAt = row.repliedAt;
  }
  return [...byUser.values()];
}

/**
 * Teammate read/replied chips for the open thread (M2.7 team read statuses).
 * Shows one chip per teammate who opted in via shareReadStatuses — the
 * backend never serves anyone else — and refreshes live on
 * `activity.updated` collab events. Renders nothing when there is no
 * activity (solo users, no teams, nobody sharing).
 */
export function TeamActivityChips({
  threadId,
  fetchActivity = defaultFetch,
  subscribe = defaultSubscribe,
}: TeamActivityChipsProps) {
  const [rows, setRows] = React.useState<TeamThreadActivity[]>([]);

  const refresh = React.useCallback(async () => {
    try {
      setRows(await fetchActivity(threadId));
    } catch {
      // Indicators are decorative — a failed fetch just leaves them empty.
    }
  }, [fetchActivity, threadId]);

  React.useEffect(() => {
    setRows([]);
    void refresh();
  }, [refresh]);

  React.useEffect(
    () =>
      subscribe((ev) => {
        if (ev.type === 'activity.updated') void refresh();
      }),
    [subscribe, refresh]
  );

  const teammates = summarizeActivity(rows);
  if (teammates.length === 0) return null;

  return (
    <div
      data-testid="team-activity-chips"
      className="flex shrink-0 flex-wrap items-center gap-1.5 border-t px-4 py-2"
    >
      {teammates.map((mate) => {
        const replied = Boolean(mate.repliedAt);
        const Icon = replied ? Reply : Eye;
        return (
          <span
            key={mate.userId}
            className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs text-muted-foreground"
            title={replied ? `Replied ${mate.repliedAt}` : `Seen ${mate.openedAt}`}
          >
            <Icon className="size-3" aria-hidden />
            {mate.userId} {replied ? 'replied' : 'seen'}
          </span>
        );
      })}
    </div>
  );
}
