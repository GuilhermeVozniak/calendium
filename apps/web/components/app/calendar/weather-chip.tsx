'use client';

import type { LucideIcon } from 'lucide-react';
import {
  Cloud,
  CloudDrizzle,
  CloudFog,
  CloudLightning,
  CloudRain,
  CloudSnow,
  CloudSun,
  Sun,
} from 'lucide-react';

import type { DayForecast } from '@calendium/shared';

import { cn } from '@/lib/utils';

/**
 * Maps WMO weather interpretation codes (the `code` of a DayForecast) to a
 * lucide icon by bucket: 0 clear, 1–2 partly cloudy, 3 overcast, 45/48 fog,
 * 51–57 drizzle, 61–67 + 80–82 rain, 71–77 + 85/86 snow, 95+ thunderstorm.
 */
export function weatherIcon(code: number): LucideIcon {
  if (code === 0) return Sun;
  if (code === 1 || code === 2) return CloudSun;
  if (code === 3) return Cloud;
  if (code === 45 || code === 48) return CloudFog;
  if (code >= 51 && code <= 57) return CloudDrizzle;
  if ((code >= 61 && code <= 67) || (code >= 80 && code <= 82)) return CloudRain;
  if ((code >= 71 && code <= 77) || code === 85 || code === 86) return CloudSnow;
  if (code >= 95) return CloudLightning;
  return Cloud;
}

export interface WeatherChipProps {
  /** The day's forecast; the chip renders nothing when absent (fail-soft). */
  forecast?: DayForecast;
  /** Icon + high only — for narrow surfaces like the agenda day strip. */
  compact?: boolean;
  className?: string;
}

/**
 * Small icon + high/low chip for a calendar day header (M2.8 Task 13).
 * Purely presentational: data arrives via lib/use-weather.ts so views stay
 * renderable (and testable) without a query provider.
 */
export function WeatherChip({ forecast, compact = false, className }: WeatherChipProps) {
  if (!forecast) return null;
  const Icon = weatherIcon(forecast.code);
  const high = Math.round(forecast.highCelsius);
  const low = Math.round(forecast.lowCelsius);
  return (
    <span
      data-testid={`weather-chip-${forecast.date}`}
      title={`High ${high}°, low ${low}° — ${forecast.precipChance}% chance of precipitation`}
      className={cn(
        'inline-flex items-center gap-0.5 text-[10px] text-muted-foreground tabular-nums',
        className
      )}
    >
      <Icon aria-hidden className="size-3 shrink-0" />
      {compact ? <>{high}°</> : <>{high}°/{low}°</>}
    </span>
  );
}
