/**
 * Intl-only timezone helpers for the public booking/poll surfaces (and any
 * other page that needs to render an absolute instant in an arbitrary IANA
 * zone without pulling in a date library). No new dependencies: everything
 * here is built on `Intl.DateTimeFormat`, which is DST-aware by construction
 * — never fixed-offset arithmetic.
 */

function dtf(timeZone: string, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat('en-US', { timeZone, ...options });
}

/** Renders an ISO instant in `timeZone` using one of a few fixed layouts. */
export function formatInTZ(
  iso: string,
  timeZone: string,
  fmt: 'time' | 'weekday-date' | 'datetime'
): string {
  const date = new Date(iso);
  switch (fmt) {
    case 'time':
      return dtf(timeZone, { hour: 'numeric', minute: '2-digit', hour12: true }).format(date);
    case 'weekday-date':
      return dtf(timeZone, { weekday: 'long', month: 'long', day: 'numeric' }).format(date);
    case 'datetime':
      return dtf(timeZone, {
        weekday: 'short',
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        hour12: true,
      })
        .format(date)
        .replace(' at ', ', '); // en-US 'short' layouts sometimes join with " at "
  }
}

/** GMT offset label at instant `at` (defaults to now), e.g. "GMT-4", "GMT+5:30". */
export function tzOffsetLabel(timeZone: string, at: Date = new Date()): string {
  try {
    const parts = dtf(timeZone, { timeZoneName: 'shortOffset' }).formatToParts(at);
    const value = parts.find((p) => p.type === 'timeZoneName')?.value;
    // UTC itself renders as bare "GMT" (no offset digits) — normalize to the
    // same "GMT+0" shape every other zone uses.
    return value === 'GMT' ? 'GMT+0' : (value ?? 'GMT+0');
  } catch {
    return 'GMT+0';
  }
}

/**
 * Short zone abbreviation at instant `at` (defaults to now), e.g. "EDT"/"EST".
 * Zones without a widely-recognized abbreviation (most of Asia/Africa/most of
 * the southern hemisphere) fall back to the same "GMT±H:MM" shape Intl itself
 * returns for `timeZoneName: 'short'` in that case.
 */
export function tzAbbrev(timeZone: string, at: Date = new Date()): string {
  try {
    const parts = dtf(timeZone, { timeZoneName: 'short' }).formatToParts(at);
    return parts.find((p) => p.type === 'timeZoneName')?.value ?? timeZone;
  } catch {
    return timeZone;
  }
}

/** The visitor's resolved IANA zone, falling back to 'UTC' if Intl is unavailable. */
export function browserTimeZone(): string {
  try {
    return new Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

// A representative fallback list (roughly one zone per UTC offset, biased
// toward populous cities) for runtimes without `Intl.supportedValuesOf`,
// which shipped later than the rest of the Intl surface this module relies
// on (older Safari/WebKit, some embedded runtimes).
const FALLBACK_TIME_ZONES = [
  'UTC',
  'America/Los_Angeles',
  'America/Denver',
  'America/Chicago',
  'America/New_York',
  'America/Sao_Paulo',
  'Atlantic/Azores',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Athens',
  'Europe/Moscow',
  'Africa/Cairo',
  'Africa/Johannesburg',
  'Asia/Dubai',
  'Asia/Karachi',
  'Asia/Kolkata',
  'Asia/Dhaka',
  'Asia/Bangkok',
  'Asia/Shanghai',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Australia/Perth',
  'Australia/Adelaide',
  'Australia/Sydney',
  'Pacific/Auckland',
  'Pacific/Honolulu',
];

/** Every IANA timezone id Intl knows about, or a curated fallback list. */
export function listTimeZones(): string[] {
  try {
    if (typeof Intl.supportedValuesOf === 'function') {
      return Intl.supportedValuesOf('timeZone');
    }
  } catch {
    // fall through to the static list below
  }
  return FALLBACK_TIME_ZONES;
}
