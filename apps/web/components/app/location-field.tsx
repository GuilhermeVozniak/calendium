'use client';

import * as React from 'react';
import { Command as CommandPrimitive } from 'cmdk';
import { MapPin } from 'lucide-react';

import { ApiRequestError, type Place } from '@calendium/shared';

import { Command, CommandItem, CommandList } from '@/components/ui/command';
import { Input } from '@/components/ui/input';
import { autocompletePlacesApi } from '@/lib/places-data';
import { useInstance } from '@/lib/use-instance';
import { cn } from '@/lib/utils';

const MIN_QUERY_CHARS = 3;
const DEFAULT_DEBOUNCE_MS = 300;

export interface LocationFieldProps {
  value: string;
  /**
   * Fired on every change. `coords` is set only when the user picked an
   * autocomplete suggestion; free typing always reports `null` so stale
   * coordinates never outlive an edited location string.
   */
  onChange: (value: string, coords: { lat: number; lon: number } | null) => void;
  placeholder?: string;
  className?: string;
  /** Test seam; production keeps the 300ms default. */
  debounceMs?: number;
}

/**
 * Location input with debounced place autocomplete (M2.8 Task 11).
 * Renders a cmdk combobox (arrow-key + Enter navigation) when the server
 * advertises features.maps; otherwise — or after the server answers 501 or
 * the request fails — it degrades to the plain text input it replaced.
 */
export function LocationField({
  value,
  onChange,
  placeholder = 'Add location',
  className,
  debounceMs = DEFAULT_DEBOUNCE_MS,
}: LocationFieldProps) {
  const instance = useInstance();
  const mapsAdvertised = instance.data?.features.maps === true;
  const [unavailable, setUnavailable] = React.useState(false);
  const [suggestions, setSuggestions] = React.useState<Place[]>([]);
  const [open, setOpen] = React.useState(false);
  // Suppresses the lookup triggered by programmatic value changes (picking
  // a suggestion), so selecting never immediately re-opens the list.
  const skipNextLookup = React.useRef(false);

  const enabled = mapsAdvertised && !unavailable;

  React.useEffect(() => {
    if (!enabled) return;
    if (skipNextLookup.current) {
      skipNextLookup.current = false;
      return;
    }
    const q = value.trim();
    if (q.length < MIN_QUERY_CHARS) {
      setSuggestions([]);
      setOpen(false);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const places = await autocompletePlacesApi(q);
        if (cancelled) return;
        setSuggestions(places);
        setOpen(places.length > 0);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof ApiRequestError && err.status === 501) {
          // Maps not configured server-side: hide the affordance for good.
          setUnavailable(true);
        }
        setSuggestions([]);
        setOpen(false);
      }
    }, debounceMs);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [value, enabled, debounceMs]);

  if (!enabled) {
    return (
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value, null)}
        placeholder={placeholder}
        aria-label="Location"
        className={cn('h-8', className)}
      />
    );
  }

  const pick = (place: Place) => {
    skipNextLookup.current = true;
    setSuggestions([]);
    setOpen(false);
    onChange(place.address || place.name, { lat: place.lat, lon: place.lon });
  };

  return (
    <Command shouldFilter={false} className={cn('relative overflow-visible', className)}>
      {/* cmdk's Input primitive gives us list keyboard navigation for free. */}
      <div
        className={cn(
          'border-input dark:bg-input/30 flex h-8 w-full items-center rounded-md border bg-transparent px-3 shadow-xs transition-[color,box-shadow]',
          'focus-within:border-ring focus-within:ring-ring/50 focus-within:ring-[3px]'
        )}
      >
        {/* The styled CommandInput in ui/command.tsx hard-wires a search
            icon + bottom border meant for palettes; the combobox uses the
            bare cmdk input primitive with field styling instead. */}
        <CommandPrimitive.Input
          data-slot="command-input"
          value={value}
          onValueChange={(next) => onChange(next, null)}
          onBlur={() => setOpen(false)}
          placeholder={placeholder}
          aria-label="Location"
          className="placeholder:text-muted-foreground w-full bg-transparent text-sm outline-hidden disabled:cursor-not-allowed disabled:opacity-50"
        />
      </div>
      {open && suggestions.length > 0 && (
        <CommandList className="bg-popover text-popover-foreground absolute top-full right-0 left-0 z-50 mt-1 rounded-md border shadow-md">
          {suggestions.map((place) => (
            <CommandItem
              key={`${place.lat},${place.lon},${place.address}`}
              value={`${place.lat},${place.lon},${place.address}`}
              // onMouseDown fires before the input's blur closes the list.
              onMouseDown={(e) => e.preventDefault()}
              onSelect={() => pick(place)}
            >
              <MapPin className="size-4 shrink-0" />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{place.name}</span>
                <span className="text-muted-foreground block truncate text-xs">{place.address}</span>
              </span>
            </CommandItem>
          ))}
        </CommandList>
      )}
    </Command>
  );
}
