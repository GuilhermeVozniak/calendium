'use client';

import * as React from 'react';
import { useQuery } from '@tanstack/react-query';
import { addDays, endOfDay, format } from 'date-fns';
import { Copy, Globe, Link2 } from 'lucide-react';
import { toast } from 'sonner';

import type { AvailabilitySlot, BookingLink } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
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
import { formatInTZ, groupSlotsByDayInZone, listTimeZones, tzLongLabel } from '@/lib/timezones';
import { cn } from '@/lib/utils';

/**
 * "Share availability" (Superhuman / Vimcal style): pick a duration + range,
 * toggle the free slots you want to offer, copy a formatted text block —
 * pre-converted into whichever timezone the recipient is in (Vimcal-style
 * drag-and-copy availability as text), with an optional booking-link footer.
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

/**
 * Builds the copy-to-clipboard text block. When `recipientTZ` matches
 * `ownerTZ` this is byte-identical to the original (pre-Task-16) format —
 * machine-local `date-fns` formatting, labeled with the raw IANA zone name.
 * When they differ, every slot is re-rendered in `recipientTZ` via the
 * Intl-based `formatInTZ`/`groupSlotsByDayInZone` helpers (DST-correct,
 * independent of the machine's local timezone) and the header names the
 * recipient's zone (e.g. "Eastern Time — EDT") instead of an IANA id. An
 * optional `bookingUrl` appends a "pick a time" footer line either way.
 */
export function buildShareText(
  slots: AvailabilitySlot[],
  ownerTZ: string,
  recipientTZ: string,
  bookingUrl?: string
): string {
  const lines: string[] = [];

  if (recipientTZ === ownerTZ) {
    lines.push(`Here are a few times that work for me (all times ${ownerTZ}):`);
    for (const group of groupByDay(slots)) {
      lines.push('', format(new Date(`${group.day}T00:00`), 'EEEE, MMM d'));
      for (const slot of group.slots) {
        lines.push(
          `  • ${format(new Date(slot.start), 'h:mm a')} – ${format(new Date(slot.end), 'h:mm a')}`
        );
      }
    }
  } else {
    const { name, abbrev } = tzLongLabel(
      recipientTZ,
      slots[0] ? new Date(slots[0].start) : new Date()
    );
    lines.push(`Here are a few times that work for me (all times ${name} — ${abbrev}):`);
    for (const group of groupSlotsByDayInZone(slots, recipientTZ)) {
      lines.push('', formatInTZ(group.slots[0].start, recipientTZ, 'day'));
      for (const slot of group.slots) {
        lines.push(
          `  • ${formatInTZ(slot.start, recipientTZ, 'time')} – ${formatInTZ(slot.end, recipientTZ, 'time')}`
        );
      }
    }
  }

  if (bookingUrl) {
    lines.push('', `Or pick a time: ${bookingUrl}`);
  }

  return lines.join('\n');
}

function detectLocalTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
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
  const [recipientTZ, setRecipientTZ] = React.useState<string>(detectLocalTimeZone);
  const [tzPickerOpen, setTzPickerOpen] = React.useState(false);
  const [tzQuery, setTzQuery] = React.useState('');
  const [bookingLinkId, setBookingLinkId] = React.useState<string | null>(null);

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

  // Booking links for the optional "pick a time" footer. Best-effort: an
  // instance without any active links (or a demo/offline session where the
  // authed endpoint isn't reachable) just shows no picker, never fabricated
  // data — the availability text itself always derives from real slots.
  const bookingLinksQuery = useQuery({
    queryKey: ['booking-links-for-availability'],
    queryFn: async (): Promise<BookingLink[]> => {
      try {
        return await getApiClient().listBookingLinks();
      } catch {
        return [];
      }
    },
    enabled: open,
    staleTime: 60_000,
  });
  const activeBookingLinks = React.useMemo(
    () => (bookingLinksQuery.data ?? []).filter((l) => l.active),
    [bookingLinksQuery.data]
  );

  const slots = slotsQuery.data ?? [];
  const included = slots.filter((s) => !excluded.has(s.start));
  const groups = React.useMemo(() => groupByDay(slots), [slots]);
  const timeZone = React.useMemo(detectLocalTimeZone, []);

  const allTimeZones = React.useMemo(() => listTimeZones(), []);
  const tzResults = React.useMemo(() => {
    const query = tzQuery.trim().toLowerCase();
    const matched = query
      ? allTimeZones.filter((z) => z.toLowerCase().includes(query))
      : allTimeZones;
    return matched.slice(0, 50);
  }, [allTimeZones, tzQuery]);

  const bookingUrl = React.useMemo(() => {
    if (!bookingLinkId || typeof window === 'undefined') return undefined;
    const link = activeBookingLinks.find((l) => l.id === bookingLinkId);
    return link ? `${window.location.origin}/book/${link.slug}` : undefined;
  }, [bookingLinkId, activeBookingLinks]);

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

  const shareText = React.useMemo(
    () => buildShareText(included, timeZone, recipientTZ, bookingUrl),
    [included, timeZone, recipientTZ, bookingUrl]
  );

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(shareText);
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
          <Popover open={tzPickerOpen} onOpenChange={setTzPickerOpen}>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                className="ml-auto h-8 gap-1.5 px-2 text-xs"
                aria-label="Recipient timezone"
              >
                <Globe className="size-3.5" />
                {recipientTZ.replace(/_/g, ' ')}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-64 p-0" align="end">
              <Command shouldFilter={false}>
                <CommandInput
                  placeholder="Search timezone…"
                  value={tzQuery}
                  onValueChange={setTzQuery}
                />
                <CommandList>
                  <CommandEmpty>No matching timezone.</CommandEmpty>
                  <CommandGroup>
                    {tzResults.map((zone) => (
                      <CommandItem
                        key={zone}
                        value={zone}
                        onSelect={() => {
                          setRecipientTZ(zone);
                          setTzPickerOpen(false);
                          setTzQuery('');
                        }}
                      >
                        {zone.replace(/_/g, ' ')}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
        </div>

        {activeBookingLinks.length > 0 && (
          <div className="flex items-center gap-2">
            <Link2 className="size-3.5 shrink-0 text-muted-foreground" />
            <Select
              value={bookingLinkId ?? 'none'}
              onValueChange={(v) => setBookingLinkId(v === 'none' ? null : v)}
            >
              <SelectTrigger size="sm" className="w-full" aria-label="Booking link">
                <SelectValue placeholder="No booking link" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">No booking link</SelectItem>
                {activeBookingLinks.map((link) => (
                  <SelectItem key={link.id} value={link.id}>
                    {link.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

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
            {shareText}
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
