import { render, screen } from '@testing-library/react';
import { format } from 'date-fns';
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
import { describe, expect, it } from 'vitest';

import { mockForecast } from '@/lib/calendar-mock';
import { forecastByDate } from '@/lib/use-weather';

import { DayTicker } from './day-ticker';
import { WeatherChip, weatherIcon } from './weather-chip';

describe('WeatherChip', () => {
  it('renders icon + rounded high/low from a mock forecast day', () => {
    const day = mockForecast(3, new Date(2026, 6, 19))[0]!;
    render(<WeatherChip forecast={day} />);
    const chip = screen.getByTestId(`weather-chip-${day.date}`);
    expect(chip.textContent).toContain(
      `${Math.round(day.highCelsius)}°/${Math.round(day.lowCelsius)}°`
    );
    expect(chip.title).toContain(`${day.precipChance}% chance of precipitation`);
  });

  it('compact mode shows only the high', () => {
    const day = mockForecast(1, new Date(2026, 6, 19))[0]!;
    render(<WeatherChip forecast={day} compact />);
    const chip = screen.getByTestId(`weather-chip-${day.date}`);
    expect(chip.textContent).toBe(`${Math.round(day.highCelsius)}°`);
  });

  it('renders nothing when there is no forecast (weather hidden/disabled)', () => {
    const { container } = render(<WeatherChip />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe('weatherIcon WMO buckets', () => {
  it.each([
    [0, Sun],
    [1, CloudSun],
    [2, CloudSun],
    [3, Cloud],
    [45, CloudFog],
    [48, CloudFog],
    [53, CloudDrizzle],
    [63, CloudRain],
    [81, CloudRain],
    [73, CloudSnow],
    [86, CloudSnow],
    [95, CloudLightning],
    [99, CloudLightning],
    [42, Cloud], // unknown code → generic cloud
  ])('maps code %d', (code, icon) => {
    expect(weatherIcon(code as number)).toBe(icon);
  });
});

describe('agenda strip weather chips', () => {
  const anchor = new Date(2026, 6, 19);
  const baseProps = {
    anchor,
    events: [],
    calendarById: new Map(),
    onAnchorChange: () => {},
    onEventClick: () => {},
  };

  it('renders a chip in the day strip for days with a forecast', () => {
    render(
      <DayTicker {...baseProps} weatherByDate={forecastByDate(mockForecast(14, anchor))} />
    );
    const key = format(anchor, 'yyyy-MM-dd');
    const cell = screen.getByTestId(`ticker-day-${key}`);
    expect(cell.querySelector(`[data-testid="weather-chip-${key}"]`)).not.toBeNull();
  });

  it('renders no chips when weather is hidden (no weatherByDate)', () => {
    render(<DayTicker {...baseProps} />);
    expect(screen.queryAllByTestId(/^weather-chip-/)).toHaveLength(0);
  });
});
