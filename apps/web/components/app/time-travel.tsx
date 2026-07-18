'use client';

import * as React from 'react';
import { Globe, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { listTimeZones, zoneCaption } from '@/lib/timezones';

/**
 * Time Travel (Superhuman/Vimcal-style): overlay one city's clock on the
 * calendar grid without changing your own timezone. `active` is persisted by
 * the caller to `TIME_TRAVEL_STORAGE_KEY` (see the helpers below) so the
 * overlay survives a reload; this component only renders the toggle/search
 * control and an "exit" chip once a city is active.
 */
export const TIME_TRAVEL_STORAGE_KEY = 'calendium.timetravel';

function isValidTimeZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

/** Reads the persisted Time Travel zone, if any and still a valid IANA id. */
export function getStoredTimeTravelZone(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(TIME_TRAVEL_STORAGE_KEY);
    if (!raw || !isValidTimeZone(raw)) return null;
    return raw;
  } catch {
    return null;
  }
}

/** Persists (or clears, for `null`) the active Time Travel zone. */
export function setStoredTimeTravelZone(zone: string | null): void {
  if (typeof window === 'undefined') return;
  try {
    if (zone) window.localStorage.setItem(TIME_TRAVEL_STORAGE_KEY, zone);
    else window.localStorage.removeItem(TIME_TRAVEL_STORAGE_KEY);
  } catch {
    // localStorage unavailable (private mode, etc.) — the overlay still
    // works for the current session, it just won't survive a reload.
  }
}

export interface TimeTravelPickerProps {
  /** The active overlay zone (IANA id), or null when Time Travel is off. */
  active: string | null;
  onChange: (tz: string | null) => void;
  /** Controls the search popover from outside (e.g. a keyboard shortcut or palette entry). Uncontrolled (internal state) when omitted. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function TimeTravelPicker({
  active,
  onChange,
  open: openProp,
  onOpenChange: onOpenChangeProp,
}: TimeTravelPickerProps) {
  const [openState, setOpenState] = React.useState(false);
  const open = openProp ?? openState;
  const setOpen = onOpenChangeProp ?? setOpenState;
  const [query, setQuery] = React.useState('');

  const zones = React.useMemo(() => listTimeZones(), []);
  const results = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    const matched = q ? zones.filter((z) => z.toLowerCase().includes(q)) : zones;
    return matched.slice(0, 50);
  }, [zones, query]);

  // Esc exits Time Travel from anywhere on the page, matching the brief's
  // "Esc clears" behavior — not gated on the popover being open.
  React.useEffect(() => {
    if (!active) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onChange(null);
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [active, onChange]);

  if (active) {
    const { city, gmt } = zoneCaption(active, new Date());
    return (
      <span
        data-testid="time-travel-chip"
        className="inline-flex items-center gap-1.5 rounded-full border bg-muted/40 py-1 pr-1 pl-2.5 text-xs"
      >
        <Globe className="size-3.5" />
        <span className="font-medium">Time Travel: {city}</span>
        <span className="text-muted-foreground">{gmt}</span>
        <button
          type="button"
          onClick={() => onChange(null)}
          aria-label="Exit Time Travel"
          className="rounded-full p-0.5 hover:bg-accent"
        >
          <X className="size-3" />
        </button>
      </span>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1.5">
          <Globe className="size-3.5" />
          Time Travel
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-64 p-0" align="end">
        <Command shouldFilter={false}>
          <CommandInput placeholder="Jump to a city…" value={query} onValueChange={setQuery} />
          <CommandList>
            <CommandEmpty>No matching city.</CommandEmpty>
            <CommandGroup>
              {results.map((zone) => (
                <CommandItem
                  key={zone}
                  value={zone}
                  onSelect={() => {
                    onChange(zone);
                    setOpen(false);
                    setQuery('');
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
  );
}
