'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { endOfDay, format, startOfDay } from 'date-fns';
import { TriangleAlert } from 'lucide-react';

import type { Calendar } from '@calendium/shared';
import { findConflicts, toBusyIntervals } from '@calendium/shared';

import { fetchBusyEvents } from '@/lib/calendar-data';
import { fetchAccounts } from '@/lib/settings-data';
import { useSelfEmails } from '@/lib/use-identity';

const MAX_CONFLICTS_SHOWN = 3;

export interface ConflictWarningProps {
  /** All calendars across every connected account (the same list the caller already holds). */
  calendars: Calendar[];
  /** Candidate start/end for the event being created or edited; null while the form is incomplete. */
  start: Date | null;
  end: Date | null;
  allDay: boolean;
  /** The event being edited - excluded from its own conflict check. */
  ignoreEventId?: string;
}

/**
 * Inline amber double-booking warning (Task 12 - cross-account conflict
 * blocking). Fetches busy events via fetchBusyEvents - an unfiltered query
 * that includes hidden calendars and every connected account, not just the
 * ones currently shown - so a hidden or other-account event still surfaces a
 * warning here. Non-blocking: this only warns, callers keep Save enabled
 * regardless of what it shows.
 *
 * Self-contained by design: the caller only needs to supply the candidate
 * time window (+ its calendars, which it already has) - calendars/accounts
 * lookups happen internally via the same react-query cache keys the rest of
 * the app uses (['accounts'], etc.), so mounting this inside another
 * component costs one line and no extra prop plumbing.
 */
export function ConflictWarning({
  calendars,
  start,
  end,
  allDay,
  ignoreEventId,
}: ConflictWarningProps) {
  const selfEmails = useSelfEmails();
  const enabled = !allDay && start != null && end != null;

  const rangeFrom = enabled ? startOfDay(start as Date) : null;
  const rangeTo = enabled ? endOfDay(end as Date) : null;

  const busyQuery = useQuery({
    queryKey: ['busy-events', rangeFrom?.toISOString(), rangeTo?.toISOString()],
    queryFn: () => fetchBusyEvents(rangeFrom as Date, rangeTo as Date),
    enabled: enabled && rangeFrom != null && rangeTo != null,
  });
  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts });

  const conflicts = React.useMemo(() => {
    if (!enabled || !start || !end) return [];
    const busy = toBusyIntervals(busyQuery.data ?? [], calendars, [...selfEmails], {
      ignoreEventId,
    });
    return findConflicts(start, end, busy);
  }, [enabled, start, end, busyQuery.data, calendars, selfEmails, ignoreEventId]);

  if (conflicts.length === 0) return null;

  const accountEmailById = new Map(
    (accountsQuery.data ?? []).map((account) => [account.id, account.email])
  );
  const shown = conflicts.slice(0, MAX_CONFLICTS_SHOWN);
  const extra = conflicts.length - shown.length;

  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-700 dark:text-amber-400"
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" />
      <div className="space-y-0.5">
        {shown.map((conflict) => (
          <p key={conflict.eventId}>
            Conflicts with «{conflict.title}» ({format(conflict.start, 'h:mm')}–
            {format(conflict.end, 'h:mm')},{' '}
            {accountEmailById.get(conflict.accountId) ?? 'another account'})
          </p>
        ))}
        {extra > 0 && <p className="text-xs opacity-80">+{extra} more</p>}
      </div>
    </div>
  );
}
