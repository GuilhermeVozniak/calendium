import type { Event } from '@calendium/shared';

export const FALLBACK_COLOR = '#6366f1';

export function withAlpha(hex: string, alpha: number): string {
  const clean = hex.replace('#', '');
  const full = clean.length === 3 ? clean.split('').map((c) => c + c).join('') : clean;
  const n = parseInt(full, 16);
  if (Number.isNaN(n) || full.length !== 6) return hex;
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}

export function sortByAllDayThenStart(a: Event, b: Event): number {
  return Number(b.allDay) - Number(a.allDay) || a.start.localeCompare(b.start);
}
