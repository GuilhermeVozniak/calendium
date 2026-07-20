'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { addDays, endOfDay, format } from 'date-fns';
import { CalendarSearch } from 'lucide-react';

import type { AvailabilitySlot, MemberAvailability } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { getApiClient } from '@/lib/api';
import { fetchAvailability } from '@/lib/calendar-data';
import { cn } from '@/lib/utils';

/**
 * Find Time inline in compose (M2.7 Task 13, the ⌘⇧A extension): your own
 * free slots overlaid with teammates' opaque busy blocks. Only slots free
 * for every selected member are insertable; a deliberate override lets you
 * insert a conflicting slot anyway, marked as such in the inserted text.
 *
 * Privacy: teammate busy data is exactly what the server returned — opaque
 * start/end blocks (free_busy contract). No details exist client-side and
 * none are looked up elsewhere.
 */

const DURATIONS = [15, 30, 45, 60] as const;
const RANGE_DAYS = 5;

/**
 * The selected, sharing members whose busy blocks overlap the slot.
 * Members with shared=false contribute nothing — the server sent no data
 * for them and none is fabricated.
 */
export function slotBlockers(
  slot: AvailabilitySlot,
  members: MemberAvailability[],
  selected: ReadonlySet<string>
): string[] {
  const slotStart = new Date(slot.start);
  const slotEnd = new Date(slot.end);
  return members
    .filter(
      (m) =>
        m.shared &&
        selected.has(m.userId) &&
        m.busy.some((b) => new Date(b.start) < slotEnd && new Date(b.end) > slotStart)
    )
    .map((m) => m.userId);
}

/** Text block inserted into the draft; conflicted slots are explicitly marked. */
export function buildFindTimeText(
  slots: Array<{ slot: AvailabilitySlot; conflicted: boolean }>
): string {
  const lines: string[] = ['How about one of these times?'];
  let lastDay = '';
  for (const { slot, conflicted } of slots) {
    const day = format(new Date(slot.start), 'EEEE, MMM d');
    if (day !== lastDay) {
      lines.push('', day);
      lastDay = day;
    }
    const window = `${format(new Date(slot.start), 'h:mm a')} – ${format(new Date(slot.end), 'h:mm a')}`;
    lines.push(`  • ${window}${conflicted ? ' (may conflict for a teammate)' : ''}`);
  }
  return lines.join('\n');
}

export function FindTimeDialog({
  open,
  onOpenChange,
  onInsert,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onInsert: (text: string) => void;
}) {
  const [duration, setDuration] = React.useState(30);
  const [teamId, setTeamId] = React.useState<string | null>(null);
  const [selectedMembers, setSelectedMembers] = React.useState<ReadonlySet<string>>(new Set());
  const [selectedSlots, setSelectedSlots] = React.useState<ReadonlySet<string>>(new Set());
  const [override, setOverride] = React.useState(false);

  // One fixed window: now → end of the RANGE_DAYS'th day, matching the
  // share-availability dialog's defaults.
  const [from] = React.useState(() => new Date());
  const to = React.useMemo(() => endOfDay(addDays(from, RANGE_DAYS - 1)), [from]);

  const slotsQuery = useQuery({
    queryKey: ['find-time-slots', duration, from.toISOString()],
    queryFn: () => fetchAvailability(from, to, duration),
    enabled: open,
    staleTime: 60_000,
  });
  const slots = slotsQuery.data ?? [];

  const teamsQuery = useQuery({
    queryKey: ['teams'],
    queryFn: () => getApiClient().listTeams(),
    enabled: open,
    staleTime: 60_000,
  });
  const teams = teamsQuery.data ?? [];

  const availabilityQuery = useQuery({
    queryKey: ['find-time-team', teamId, from.toISOString(), duration],
    queryFn: () =>
      getApiClient().teamAvailability(teamId!, from.toISOString(), to.toISOString()),
    enabled: open && !!teamId,
    staleTime: 60_000,
  });
  const members = React.useMemo(
    () => availabilityQuery.data ?? [],
    [availabilityQuery.data]
  );

  // Every sharing member starts selected when a team's data arrives.
  React.useEffect(() => {
    setSelectedMembers(new Set(members.filter((m) => m.shared).map((m) => m.userId)));
  }, [members]);

  // Changing controls invalidates the current slot selection.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the extra deps ARE the trigger — any control change deliberately clears the picked slots.
  React.useEffect(() => {
    setSelectedSlots(new Set());
  }, [open, duration, teamId, selectedMembers, override]);

  const toggleMember = (userId: string) => {
    setSelectedMembers((prev) => {
      const next = new Set(prev);
      if (next.has(userId)) next.delete(userId);
      else next.add(userId);
      return next;
    });
  };

  const toggleSlot = (start: string) => {
    setSelectedSlots((prev) => {
      const next = new Set(prev);
      if (next.has(start)) next.delete(start);
      else next.add(start);
      return next;
    });
  };

  const handleInsert = () => {
    const picked = slots
      .filter((s) => selectedSlots.has(s.start))
      .map((slot) => ({
        slot,
        conflicted: slotBlockers(slot, members, selectedMembers).length > 0,
      }));
    if (picked.length === 0) return;
    onInsert(`\n${buildFindTimeText(picked)}\n`);
    onOpenChange(false);
  };

  // Group by day for rendering.
  const groups = React.useMemo(() => {
    const map = new Map<string, AvailabilitySlot[]>();
    for (const slot of slots) {
      const key = format(new Date(slot.start), 'yyyy-MM-dd');
      const list = map.get(key) ?? [];
      list.push(slot);
      map.set(key, list);
    }
    return [...map.entries()].map(([day, daySlots]) => ({ day, slots: daySlots }));
  }, [slots]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CalendarSearch className="size-4" />
            Find time
          </DialogTitle>
          <DialogDescription>
            Pick a team to overlay teammates&apos; busy time; only slots free for everyone
            selected can be inserted.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2">
          <Select value={String(duration)} onValueChange={(v) => setDuration(Number(v))}>
            <SelectTrigger size="sm" className="w-32" aria-label="Duration">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {DURATIONS.map((d) => (
                <SelectItem key={d} value={String(d)}>
                  {d} minutes
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={teamId ?? 'none'}
            onValueChange={(v) => setTeamId(v === 'none' ? null : v)}
          >
            <SelectTrigger size="sm" className="w-44" aria-label="Team">
              <SelectValue placeholder="No team overlay" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">No team overlay</SelectItem>
              {teams.map((team) => (
                <SelectItem key={team.id} value={team.id}>
                  {team.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {teamId && members.length > 0 && (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
            {members.map((member) => {
              // Server-resolved roster identity (F2); honest id fallback.
              const label = member.name || member.email || member.userId;
              return member.shared ? (
                <label key={member.userId} className="flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={selectedMembers.has(member.userId)}
                    onChange={() => toggleMember(member.userId)}
                    aria-label={`Include ${label}`}
                  />
                  <span className="truncate" title={member.userId}>
                    {label}
                  </span>
                </label>
              ) : (
                <span
                  key={member.userId}
                  className="text-muted-foreground italic"
                  title={member.userId}
                >
                  {label} (not sharing)
                </span>
              );
            })}
          </div>
        )}

        <div className="max-h-60 overflow-y-auto rounded-md border p-3">
          {slotsQuery.isLoading && (
            <div className="space-y-3">
              <Skeleton className="h-5 w-24" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {!slotsQuery.isLoading && groups.length === 0 && (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No free slots found in the next {RANGE_DAYS} days.
            </p>
          )}
          {groups.map((group) => (
            <div key={group.day} className="mb-3 last:mb-0">
              <div className="mb-1.5 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                {format(new Date(`${group.day}T00:00`), 'EEE, MMM d')}
              </div>
              <div className="flex flex-wrap gap-1.5">
                {group.slots.map((slot) => {
                  const blockers = slotBlockers(slot, members, selectedMembers);
                  const blocked = blockers.length > 0;
                  const disabled = blocked && !override;
                  const isSelected = selectedSlots.has(slot.start);
                  return (
                    <button
                      key={slot.start}
                      type="button"
                      disabled={disabled}
                      aria-pressed={isSelected}
                      onClick={() => toggleSlot(slot.start)}
                      title={blocked ? 'Busy for a selected teammate' : undefined}
                      className={cn(
                        'rounded-md border px-2 py-1 text-xs tabular-nums transition-colors',
                        isSelected
                          ? 'border-primary/50 bg-primary/10 text-foreground'
                          : 'text-muted-foreground hover:text-foreground',
                        blocked && 'line-through opacity-60',
                        disabled && 'cursor-not-allowed'
                      )}
                    >
                      {format(new Date(slot.start), 'h:mm')} –{' '}
                      {format(new Date(slot.end), 'h:mm a')}
                      {blocked && <span className="ml-1 no-underline">· Busy</span>}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        {teamId && (
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={override}
              onChange={(event) => setOverride(event.target.checked)}
              aria-label="Allow conflicting slots"
            />
            Allow conflicting slots (inserted times are marked as possible conflicts)
          </label>
        )}

        <DialogFooter>
          <span className="mr-auto self-center text-xs text-muted-foreground">
            {selectedSlots.size} {selectedSlots.size === 1 ? 'slot' : 'slots'} selected
          </span>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleInsert} disabled={selectedSlots.size === 0}>
            Insert times
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
