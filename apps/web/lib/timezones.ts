// Multi-timezone grid support: pinned-zone persistence (localStorage — the
// prefs API only carries splitOrder today, see lib/prefs-data.ts) plus
// DST-correct offset/label math built on Intl, never fixed UTC arithmetic.

export const PINNED_KEY = 'calendium.pinnedTimeZones';
const MAX_PINNED_ZONES = 3;

function isValidTimeZone(zone: string): boolean {
  if (!zone) return false;
  try {
    // Constructing the formatter throws a RangeError for unknown IANA ids —
    // the cheapest validity check available, and works without relying on
    // Intl.supportedValuesOf (not available in every runtime/browser).
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

function dedupe(zones: string[]): string[] {
  return Array.from(new Set(zones));
}

/** Pinned timezone ids, validated and capped at 3. Empty outside the browser. */
export function getPinnedTimeZones(): string[] {
  if (typeof window === 'undefined') return [];
  try {
    const raw = window.localStorage.getItem(PINNED_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const valid = parsed.filter((z): z is string => typeof z === 'string' && isValidTimeZone(z));
    return dedupe(valid).slice(0, MAX_PINNED_ZONES);
  } catch {
    return [];
  }
}

/** Persist pinned zones: invalid ids dropped, deduped, capped at 3. */
export function setPinnedTimeZones(zones: string[]): void {
  if (typeof window === 'undefined') return;
  const valid = dedupe(zones.filter(isValidTimeZone)).slice(0, MAX_PINNED_ZONES);
  try {
    window.localStorage.setItem(PINNED_KEY, JSON.stringify(valid));
  } catch {
    // localStorage unavailable (private mode, etc.) — pin state won't
    // survive a reload, but the current session is unaffected.
  }
}

/** Offset of `timeZone` from UTC in minutes at instant `at` (DST-aware). */
export function tzOffsetMinutes(timeZone: string, at: Date): number {
  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
  const p = Object.fromEntries(dtf.formatToParts(at).map((x) => [x.type, x.value]));
  const asUTC = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour % 24, +p.minute, +p.second);
  return Math.round((asUTC - at.getTime()) / 60_000);
}

/**
 * Gutter label for the primary-zone hour `hour` of `day` rendered in `zone`:
 * label ("3 PM") + dayShift (-1|0|1) so the gutter can mark "prev/next day".
 *
 * `day`/`hour` describe an instant using the *system* (primary-grid) local
 * timezone — i.e. the same wall-clock semantics as `new Date(y, m, d, hour)`.
 * The label/day-shift are then computed for that same instant as observed in
 * `zone`. Each call is independently DST-correct for whatever `day` it's
 * given — but that only makes a *caller* correct across a mid-week DST
 * transition in `zone` if the caller actually invokes this once per displayed
 * day. A caller that instead shares one result across several displayed days
 * (as the week view's single pinned-zone gutter does, anchored to the first
 * day) is not automatically protected: it must separately detect when the
 * per-day results would disagree and flag that, since a shared gutter can't
 * show two different labels for the same hour row. See
 * `zoneLabelsUniformAcrossDays` in time-grid.tsx for that check.
 */
export function hourLabelInZone(
  day: Date,
  hour: number,
  zone: string
): { label: string; dayShift: -1 | 0 | 1 } {
  const instant = new Date(day.getFullYear(), day.getMonth(), day.getDate(), hour, 0, 0, 0);

  const dtf = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hour12: true,
    hour: 'numeric',
    minute: '2-digit',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
  const p = Object.fromEntries(dtf.formatToParts(instant).map((x) => [x.type, x.value]));

  const label = p.minute === '00' ? `${p.hour} ${p.dayPeriod}` : `${p.hour}:${p.minute} ${p.dayPeriod}`;

  // Compare calendar dates (not elapsed time) so the shift is always a clean
  // -1/0/1 day count, independent of either zone's UTC offset.
  const localDayStart = Date.UTC(day.getFullYear(), day.getMonth(), day.getDate());
  const zonedDayStart = Date.UTC(+p.year, +p.month - 1, +p.day);
  const diffDays = Math.round((zonedDayStart - localDayStart) / 86_400_000);
  const dayShift = (Math.max(-1, Math.min(1, diffDays)) as -1 | 0 | 1);

  return { label, dayShift };
}

function formatGmtOffset(offsetMinutes: number): string {
  const sign = offsetMinutes < 0 ? '-' : '+';
  const abs = Math.abs(offsetMinutes);
  const hours = Math.floor(abs / 60);
  const minutes = abs % 60;
  return `GMT${sign}${hours}${minutes ? `:${String(minutes).padStart(2, '0')}` : ''}`;
}

// A handful of common zones get a short, familiar caption; everything else
// falls back to the IANA id's last path segment with underscores as spaces.
const CITY_LABELS: Record<string, string> = {
  'America/New_York': 'NYC',
  'America/Los_Angeles': 'LA',
  'Europe/London': 'London',
  'Asia/Tokyo': 'Tokyo',
  'Asia/Kolkata': 'Kolkata',
  'Australia/Sydney': 'Sydney',
};

function cityLabel(zone: string): string {
  if (CITY_LABELS[zone]) return CITY_LABELS[zone];
  const last = zone.split('/').at(-1) ?? zone;
  return last.replace(/_/g, ' ');
}

/** Short zone caption for the gutter header, e.g. "NYC GMT-4" → cityLabel + gmtLabel. */
export function zoneCaption(zone: string, at: Date): { city: string; gmt: string } {
  return { city: cityLabel(zone), gmt: formatGmtOffset(tzOffsetMinutes(zone, at)) };
}

// ---------------------------------------------------------------------------
// Recipient-TZ availability text + Time Travel overlay (Task 16)
// ---------------------------------------------------------------------------

/** Every IANA zone id the runtime knows about, for a searchable TZ picker. Empty if unsupported. */
export function listTimeZones(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [];
  }
}

function timeZoneNamePart(zone: string, at: Date, style: 'longGeneric' | 'short'): string {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: style }).formatToParts(
    at
  );
  return parts.find((p) => p.type === 'timeZoneName')?.value ?? zone;
}

/** Short zone abbreviation at instant `at`, e.g. "EDT" (summer) / "EST" (winter) — DST-aware. */
export function tzAbbrev(zone: string, at: Date): string {
  return timeZoneNamePart(zone, at, 'short');
}

/** Human-readable zone name + abbreviation at instant `at`, e.g. { name: "Eastern Time", abbrev: "EDT" }. */
export function tzLongLabel(zone: string, at: Date): { name: string; abbrev: string } {
  return { name: timeZoneNamePart(zone, at, 'longGeneric'), abbrev: timeZoneNamePart(zone, at, 'short') };
}

/**
 * Formats a real UTC instant (an ISO string, e.g. an AvailabilitySlot's
 * start/end) as it reads on a wall clock in `zone` — independent of the
 * machine's local timezone, unlike date-fns' `format`. `'time'` yields
 * "3:05 PM"; `'day'` yields a day heading like "Monday, Jul 20".
 */
export function formatInTZ(iso: string, zone: string, pattern: 'time' | 'day'): string {
  const at = new Date(iso);
  if (pattern === 'time') {
    return new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      hour: 'numeric',
      minute: '2-digit',
      hour12: true,
    }).format(at);
  }
  return new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    weekday: 'long',
    month: 'short',
    day: 'numeric',
  }).format(at);
}

/** Groups slots by their calendar date *as observed in `zone`* (not the machine's local date). */
export function groupSlotsByDayInZone<T extends { start: string }>(
  slots: T[],
  zone: string
): Array<{ day: string; slots: T[] }> {
  const dtf = new Intl.DateTimeFormat('en-CA', {
    timeZone: zone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
  const map = new Map<string, T[]>();
  for (const slot of slots) {
    const key = dtf.format(new Date(slot.start));
    const list = map.get(key) ?? [];
    list.push(slot);
    map.set(key, list);
  }
  return [...map.entries()].map(([day, daySlots]) => ({ day, slots: daySlots }));
}
