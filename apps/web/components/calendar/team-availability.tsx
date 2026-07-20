'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { addDays, format, startOfDay } from 'date-fns';
import { ChevronLeft, ChevronRight, Users } from 'lucide-react';

import type { AvailabilitySlot } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import { cn } from '@/lib/utils';

/**
 * Team availability overview (M2.7 Task 13): member rows × hour columns for
 * one day. The server is authoritative — each member's row is built ONLY
 * from the opaque busy blocks it returns (free_busy privacy: start/end,
 * nothing else), and members who have not shared a calendar with the team
 * are shown as "Not sharing". This component never backfills details from
 * other caches.
 */

/** Hour columns of the overview grid, in the viewer's local timezone. */
export const GRID_START_HOUR = 7;
export const GRID_END_HOUR = 19;

const GRID_HOURS = Array.from(
  { length: GRID_END_HOUR - GRID_START_HOUR },
  (_, i) => GRID_START_HOUR + i
);

/**
 * Whether any opaque busy block overlaps the local hour cell [hour, hour+1)
 * of `day`. Timezone-aware by construction: the server's ISO instants are
 * compared against Dates built in the viewer's local zone.
 */
export function hourIsBusy(busy: AvailabilitySlot[], day: Date, hour: number): boolean {
  const cellStart = new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour);
  const cellEnd = new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour + 1);
  return busy.some((b) => new Date(b.start) < cellEnd && new Date(b.end) > cellStart);
}

function hourLabel(hour: number): string {
  return format(new Date(2000, 0, 1, hour), 'h a');
}

function detectLocalTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

export function TeamAvailabilityDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [teamId, setTeamId] = React.useState<string | null>(null);
  const [day, setDay] = React.useState<Date>(() => startOfDay(new Date()));

  const teamsQuery = useQuery({
    queryKey: ['teams'],
    queryFn: () => getApiClient().listTeams(),
    enabled: open,
    staleTime: 60_000,
  });
  const teams = teamsQuery.data ?? [];

  // Auto-select the first team so the overview is one click away.
  React.useEffect(() => {
    if (open && !teamId && teams.length > 0) setTeamId(teams[0]!.id);
  }, [open, teamId, teams]);

  const availabilityQuery = useQuery({
    queryKey: ['team-availability', teamId, day.toISOString()],
    queryFn: () =>
      getApiClient().teamAvailability(
        teamId!,
        day.toISOString(),
        addDays(day, 1).toISOString()
      ),
    enabled: open && !!teamId,
    staleTime: 60_000,
  });
  const rows = availabilityQuery.data ?? [];
  const timeZone = React.useMemo(detectLocalTimeZone, []);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Users className="size-4" />
            Team availability
          </DialogTitle>
          <DialogDescription>
            Who is free when — busy time shows as opaque blocks; members choose what to
            share by sharing a calendar with the team.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2">
          {teams.length > 0 ? (
            <Select value={teamId ?? undefined} onValueChange={setTeamId}>
              <SelectTrigger size="sm" className="w-44" aria-label="Team">
                <SelectValue placeholder="Pick a team" />
              </SelectTrigger>
              <SelectContent>
                {teams.map((team) => (
                  <SelectItem key={team.id} value={team.id}>
                    {team.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            !teamsQuery.isLoading && (
              <span className="text-sm text-muted-foreground">
                You are not a member of any team yet.
              </span>
            )
          )}
          <div className="ml-auto flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              onClick={() => setDay((d) => addDays(d, -1))}
              aria-label="Previous day"
            >
              <ChevronLeft />
            </Button>
            <span className="min-w-28 text-center text-sm font-medium tabular-nums">
              {format(day, 'EEE, MMM d')}
            </span>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              onClick={() => setDay((d) => addDays(d, 1))}
              aria-label="Next day"
            >
              <ChevronRight />
            </Button>
          </div>
        </div>

        {availabilityQuery.isLoading && teamId && (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        )}

        {!availabilityQuery.isLoading && teamId && (
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full border-collapse text-xs">
              <thead>
                <tr className="border-b bg-muted/40">
                  <th scope="col" className="w-32 px-2 py-1.5 text-left font-medium">
                    Member
                  </th>
                  {GRID_HOURS.map((hour) => (
                    <th
                      scope="col"
                      key={hour}
                      className="border-l px-1 py-1.5 text-center font-normal text-muted-foreground"
                    >
                      {hourLabel(hour)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((member) => (
                  <tr key={member.userId} className="border-b last:border-b-0">
                    <th
                      scope="row"
                      className="max-w-32 truncate px-2 py-2 text-left font-medium"
                      title={member.userId}
                    >
                      {/* Server-resolved roster identity (F2); honest id fallback. */}
                      {member.name || member.email || member.userId}
                    </th>
                    {member.shared ? (
                      GRID_HOURS.map((hour) => {
                        const busy = hourIsBusy(member.busy, day, hour);
                        return (
                          <td key={hour} className="border-l p-0.5">
                            {/* Opaque by contract: a busy cell is only ever
                                "Busy" — the server sent no details and none
                                are looked up client-side. */}
                            {busy && (
                              <div
                                role="img"
                                aria-label="Busy"
                                title="Busy"
                                className={cn('h-6 rounded-sm bg-primary/70', 'min-w-6')}
                              />
                            )}
                          </td>
                        );
                      })
                    ) : (
                      <td
                        colSpan={GRID_HOURS.length}
                        className="border-l px-2 py-2 text-muted-foreground italic"
                      >
                        Not sharing
                      </td>
                    )}
                  </tr>
                ))}
                {rows.length === 0 && (
                  <tr>
                    <td
                      colSpan={GRID_HOURS.length + 1}
                      className="px-2 py-6 text-center text-muted-foreground"
                    >
                      No members to show.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}

        <p className="text-xs text-muted-foreground">
          Times shown in {timeZone.replace(/_/g, ' ')}.
        </p>
      </DialogContent>
    </Dialog>
  );
}
