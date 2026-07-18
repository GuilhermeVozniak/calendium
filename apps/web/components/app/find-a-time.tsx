'use client';

import * as React from 'react';
import { addDays, addMinutes, format, isBefore, startOfDay } from 'date-fns';

import type { TimeProposal, TimeProposalInput } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { getApiClient } from '@/lib/api';
import { cn } from '@/lib/utils';

const GRID_START_HOUR = 8;
const GRID_END_HOUR = 20; // exclusive
const SLOT_MINUTES = 30;
const DATETIME_FMT = "yyyy-MM-dd'T'HH:mm";

// ---------------------------------------------------------------------------
// FindATimeGrid — horizontal day grid, one row per attendee (+ "You"),
// columns are half-hour slots. Busy intervals shade a row's cells; an
// attendee the provider couldn't resolve is labeled "availability unknown"
// rather than rendered as free (honesty policy — never fabricate free time).
// Clicking a column header always fires onPick, even over a busy/unknown
// column: the grid informs the choice, it never blocks manual time entry.
// ---------------------------------------------------------------------------

export interface FindATimeGridProps {
  attendeeEmails: string[];
  durationMinutes: number;
  initialDate: Date;
  onPick: (start: Date, end: Date) => void;
}

interface GridRow {
  key: string;
  label: string;
  /** null = availability unknown (unresolved by the provider, or not checked). */
  busy: { start: string; end: string }[] | null;
}

export function FindATimeGrid({
  attendeeEmails,
  durationMinutes,
  initialDate,
  onPick,
}: FindATimeGridProps) {
  const [date, setDate] = React.useState(() => startOfDay(initialDate));
  const [busyByEmail, setBusyByEmail] = React.useState<Record<
    string,
    { start: string; end: string }[]
  > | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    const from = startOfDay(date);
    const to = addDays(from, 1);
    getApiClient()
      .getFreeBusy(attendeeEmails, from.toISOString(), to.toISOString())
      .then((data) => {
        if (cancelled) return;
        setBusyByEmail(data);
      })
      .catch(() => {
        if (cancelled) return;
        setError('Could not load availability — pick a time manually');
        setBusyByEmail(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [attendeeEmails, date]);

  const slots = React.useMemo(() => {
    const list: Date[] = [];
    let cur = addMinutes(startOfDay(date), GRID_START_HOUR * 60);
    const end = addMinutes(startOfDay(date), GRID_END_HOUR * 60);
    while (isBefore(cur, end)) {
      list.push(cur);
      cur = addMinutes(cur, SLOT_MINUTES);
    }
    return list;
  }, [date]);

  const rows: GridRow[] = React.useMemo(() => {
    const guestRows = attendeeEmails.map((email) => {
      const key = email.toLowerCase();
      const resolved = busyByEmail && Object.hasOwn(busyByEmail, key);
      return { key: email, label: email, busy: resolved ? busyByEmail![key] : null };
    });
    // "You" is not covered by the guest free/busy response — its own
    // schedule isn't queried here, so it's rendered as unknown, never free.
    return [{ key: '__self__', label: 'You', busy: null }, ...guestRows];
  }, [attendeeEmails, busyByEmail]);

  const isRowBusy = (row: GridRow, slotStart: Date, slotEnd: Date) => {
    if (!row.busy) return false;
    return row.busy.some((interval) => {
      const bStart = new Date(interval.start);
      const bEnd = new Date(interval.end);
      return bStart < slotEnd && bEnd > slotStart;
    });
  };

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium">Find a time</p>
        <Input
          type="date"
          value={format(date, 'yyyy-MM-dd')}
          onChange={(e) => {
            if (!e.target.value) return;
            const next = new Date(`${e.target.value}T00:00`);
            if (!Number.isNaN(next.getTime())) setDate(startOfDay(next));
          }}
          aria-label="Find-a-time date"
          className="h-8 w-40"
        />
      </div>

      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading availability…</p>
      ) : (
        <div className="overflow-x-auto rounded-md border">
          <table className="w-full min-w-max border-collapse text-xs">
            <thead>
              <tr>
                <th className="w-32 border-b p-1.5 text-left font-medium">Attendee</th>
                {slots.map((slot) => (
                  <th key={slot.toISOString()} className="border-b p-0 text-center font-normal">
                    <button
                      type="button"
                      className="w-full px-1.5 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                      aria-label={`Pick ${format(slot, 'p')}`}
                      onClick={() => onPick(slot, addMinutes(slot, durationMinutes))}
                    >
                      {format(slot, 'HH:mm')}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.key}>
                  <td className="border-b p-1.5 font-medium">
                    {row.label}
                    {row.busy === null && (
                      <span className="ml-1 text-[10px] font-normal text-muted-foreground">
                        (availability unknown)
                      </span>
                    )}
                  </td>
                  {slots.map((slot) => {
                    const slotEnd = addMinutes(slot, durationMinutes);
                    const busy = isRowBusy(row, slot, slotEnd);
                    return (
                      <td
                        key={slot.toISOString()}
                        data-busy={busy ? 'true' : 'false'}
                        data-testid={`cell-${row.key}-${slot.toISOString()}`}
                        className={cn(
                          'h-6 w-8 border-b',
                          busy ? 'bg-destructive/25' : row.busy === null ? 'bg-muted/40' : ''
                        )}
                      />
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ProposeTimeForm — shown to a non-organizer viewing an event: pick a start
// and end, add an optional note, and counter-propose the time.
// ---------------------------------------------------------------------------

export interface ProposeTimeFormProps {
  eventId: string;
  initialStart: Date;
  initialEnd: Date;
  onProposed: () => void;
}

export function ProposeTimeForm({
  eventId,
  initialStart,
  initialEnd,
  onProposed,
}: ProposeTimeFormProps) {
  const [start, setStart] = React.useState(() => format(initialStart, DATETIME_FMT));
  const [end, setEnd] = React.useState(() => format(initialEnd, DATETIME_FMT));
  const [note, setNote] = React.useState('');
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const handleSubmit = async () => {
    const startDate = new Date(start);
    const endDate = new Date(end);
    if (
      Number.isNaN(startDate.getTime()) ||
      Number.isNaN(endDate.getTime()) ||
      !(endDate > startDate)
    ) {
      setError('Pick a valid start and end time');
      return;
    }
    setSubmitting(true);
    setError(null);
    const input: TimeProposalInput = {
      start: startDate.toISOString(),
      end: endDate.toISOString(),
      note: note.trim() || undefined,
    };
    try {
      await getApiClient().proposeTime(eventId, input);
      onProposed();
    } catch {
      setError('Could not propose this time — try again');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <p className="text-sm font-medium">Propose a new time</p>
      <div className="grid grid-cols-2 gap-2">
        <Input
          type="datetime-local"
          value={start}
          onChange={(e) => setStart(e.target.value)}
          aria-label="Proposed start"
        />
        <Input
          type="datetime-local"
          value={end}
          onChange={(e) => setEnd(e.target.value)}
          aria-label="Proposed end"
        />
      </div>
      <Textarea
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Add a note (optional)"
        aria-label="Proposal note"
        className="min-h-12"
      />
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <Button size="sm" className="self-start" onClick={handleSubmit} disabled={submitting}>
        {submitting ? 'Proposing…' : 'Propose time'}
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ProposalsList — shown to the organizer viewing an event: pending
// counter-proposals with one-click accept/decline. Accept is replay-safe on
// the backend (409 if already resolved); on that conflict this refreshes the
// list instead of claiming success.
// ---------------------------------------------------------------------------

export interface ProposalsListProps {
  eventId: string;
  onAccepted: () => void;
}

export function ProposalsList({ eventId, onAccepted }: ProposalsListProps) {
  const [proposals, setProposals] = React.useState<TimeProposal[] | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const [actingId, setActingId] = React.useState<string | null>(null);

  const load = React.useCallback(() => {
    setLoading(true);
    setError(null);
    getApiClient()
      .listProposals(eventId)
      .then((data) => setProposals(data.filter((p) => p.status === 'pending')))
      .catch(() => setError('Could not load proposals'))
      .finally(() => setLoading(false));
  }, [eventId]);

  React.useEffect(() => {
    load();
  }, [load]);

  const accept = async (proposalId: string) => {
    setActingId(proposalId);
    setActionError(null);
    try {
      await getApiClient().acceptProposal(eventId, proposalId);
      onAccepted();
    } catch {
      // 409 (already accepted/resolved elsewhere) or any other failure: never
      // claim success — refetch so the list reflects the real state.
      setActionError('That proposal was already resolved — refreshed the list');
      load();
    } finally {
      setActingId(null);
    }
  };

  const decline = async (proposalId: string) => {
    setActingId(proposalId);
    setActionError(null);
    try {
      await getApiClient().declineProposal(eventId, proposalId);
      load();
    } catch {
      setActionError('Could not decline this proposal');
    } finally {
      setActingId(null);
    }
  };

  // Only the very first fetch blanks the panel with a loading message — a
  // refresh triggered by accept/decline (including the 409 replay-safe path)
  // must keep any actionError visible instead of hiding it behind "Loading…".
  if (loading && proposals === null) {
    return <p className="text-sm text-muted-foreground">Loading proposals…</p>;
  }
  if (error && proposals === null) {
    return (
      <p role="alert" className="text-xs text-destructive">
        {error}
      </p>
    );
  }
  if (!proposals || (proposals.length === 0 && !actionError)) return null;

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <p className="text-sm font-medium">Proposed times</p>
      {actionError && (
        <p role="alert" className="text-xs text-destructive">
          {actionError}
        </p>
      )}
      {proposals.map((proposal) => (
        <div key={proposal.id} className="flex items-center justify-between gap-2 text-sm">
          <div className="min-w-0">
            <p className="truncate font-medium">{proposal.proposerName || proposal.proposerEmail}</p>
            <p className="text-xs text-muted-foreground">
              {format(new Date(proposal.start), 'PP p')} – {format(new Date(proposal.end), 'p')}
            </p>
            {proposal.note && (
              <p className="truncate text-xs italic text-muted-foreground">"{proposal.note}"</p>
            )}
          </div>
          <div className="flex shrink-0 gap-1.5">
            <Button
              size="sm"
              onClick={() => accept(proposal.id)}
              disabled={actingId === proposal.id}
            >
              Accept
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => decline(proposal.id)}
              disabled={actingId === proposal.id}
            >
              Decline
            </Button>
          </div>
        </div>
      ))}
    </div>
  );
}
