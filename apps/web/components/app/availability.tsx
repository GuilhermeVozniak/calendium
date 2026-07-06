'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { addDays, endOfDay, format } from 'date-fns';
import { Copy, Globe } from 'lucide-react';
import { toast } from 'sonner';

import type { AvailabilitySlot } from '@calendium/shared';

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
import { fetchAvailability } from '@/lib/calendar-data';
import { cn } from '@/lib/utils';

/**
 * "Share availability" (Superhuman / Vimcal style): pick a duration + range,
 * toggle the free slots you want to offer, copy a formatted text block.
 */

const DURATIONS = [15, 30, 45, 60] as const;
const RANGES = [3, 5, 7, 14] as const;

function groupByDay(slots: AvailabilitySlot[]): Array<{ day: string; slots: AvailabilitySlot[] }> {
  const map = new Map<string, AvailabilitySlot[]>();
  for (const slot of slots) {
    const key = format(new Date(slot.start), 'yyyy-MM-dd');
    const list = map.get(key) ?? [];
    list.push(slot);
    map.set(key, list);
  }
  return [...map.entries()].map(([day, daySlots]) => ({ day, slots: daySlots }));
}

function buildShareText(slots: AvailabilitySlot[], timeZone: string): string {
  const lines = [`Here are a few times that work for me (all times ${timeZone}):`];
  for (const group of groupByDay(slots)) {
    lines.push('', format(new Date(`${group.day}T00:00`), 'EEEE, MMM d'));
    for (const slot of group.slots) {
      lines.push(
        `  • ${format(new Date(slot.start), 'h:mm a')} – ${format(new Date(slot.end), 'h:mm a')}`
      );
    }
  }
  return lines.join('\n');
}

export function AvailabilityDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [duration, setDuration] = React.useState(30);
  const [rangeDays, setRangeDays] = React.useState(5);
  const [excluded, setExcluded] = React.useState<ReadonlySet<string>>(new Set());

  const slotsQuery = useQuery({
    queryKey: ['availability', duration, rangeDays],
    queryFn: () => {
      const from = new Date();
      return fetchAvailability(from, endOfDay(addDays(from, rangeDays - 1)), duration);
    },
    enabled: open,
    staleTime: 60_000,
  });

  // Every slot starts selected; changing the controls resets the selection.
  React.useEffect(() => {
    setExcluded(new Set());
  }, [open, duration, rangeDays]);

  const slots = slotsQuery.data ?? [];
  const included = slots.filter((s) => !excluded.has(s.start));
  const groups = React.useMemo(() => groupByDay(slots), [slots]);
  const timeZone = React.useMemo(() => {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch {
      return 'UTC';
    }
  }, []);

  const toggleSlot = (start: string) => {
    setExcluded((prev) => {
      const next = new Set(prev);
      if (next.has(start)) {
        next.delete(start);
      } else {
        next.add(start);
      }
      return next;
    });
  };

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(buildShareText(included, timeZone));
      toast.success(`Copied ${included.length} ${included.length === 1 ? 'slot' : 'slots'} to clipboard`);
      onOpenChange(false);
    } catch {
      toast.error('Clipboard unavailable - copy manually from the preview below.');
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Share availability</DialogTitle>
          <DialogDescription>
            Pick the free slots you want to offer, then copy them as text into any email.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2">
          <Select value={String(duration)} onValueChange={(v) => setDuration(Number(v))}>
            <SelectTrigger size="sm" className="w-32">
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
          <Select value={String(rangeDays)} onValueChange={(v) => setRangeDays(Number(v))}>
            <SelectTrigger size="sm" className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RANGES.map((d) => (
                <SelectItem key={d} value={String(d)}>
                  Next {d} days
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="ml-auto inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Globe className="size-3.5" />
            {timeZone}
          </span>
        </div>

        <div className="max-h-60 overflow-y-auto rounded-md border p-3">
          {slotsQuery.isLoading && (
            <div className="space-y-3">
              <Skeleton className="h-5 w-24" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-5 w-24" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {!slotsQuery.isLoading && groups.length === 0 && (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No free slots found in this range.
            </p>
          )}
          {groups.map((group) => (
            <div key={group.day} className="mb-3 last:mb-0">
              <div className="mb-1.5 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                {format(new Date(`${group.day}T00:00`), 'EEE, MMM d')}
              </div>
              <div className="flex flex-wrap gap-1.5">
                {group.slots.map((slot) => {
                  const isIncluded = !excluded.has(slot.start);
                  return (
                    <button
                      key={slot.start}
                      type="button"
                      aria-pressed={isIncluded}
                      onClick={() => toggleSlot(slot.start)}
                      className={cn(
                        'rounded-md border px-2 py-1 text-xs tabular-nums transition-colors',
                        isIncluded
                          ? 'border-primary/50 bg-primary/10 text-foreground'
                          : 'text-muted-foreground opacity-60 hover:opacity-100'
                      )}
                    >
                      {format(new Date(slot.start), 'h:mm')} – {format(new Date(slot.end), 'h:mm a')}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        {included.length > 0 && (
          <pre className="max-h-28 overflow-y-auto rounded-md bg-muted/50 p-3 font-mono text-xs whitespace-pre-wrap text-muted-foreground">
            {buildShareText(included, timeZone)}
          </pre>
        )}

        <DialogFooter>
          <span className="mr-auto self-center text-xs text-muted-foreground">
            {included.length} {included.length === 1 ? 'slot' : 'slots'} selected
          </span>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleCopy} disabled={included.length === 0}>
            <Copy />
            Copy to clipboard
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
