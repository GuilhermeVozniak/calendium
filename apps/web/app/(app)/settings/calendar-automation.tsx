'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import type { CalendarPrefs, TravelMode } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';

import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { fetchCalendarPrefs, updateCalendarPrefsApi } from '@/lib/calendar-prefs-data';
import { cn } from '@/lib/utils';

const DAY_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const BUFFER_OPTIONS = [0, 5, 10, 15, 20, 25, 30];
const TRAVEL_MODES: { value: TravelMode; label: string }[] = [
  { value: 'driving', label: 'Driving' },
  { value: 'walking', label: 'Walking' },
  { value: 'transit', label: 'Transit' },
];

/** "540" → "09:00" for the workday hour selects (half-hour steps). */
function minutesLabel(m: number): string {
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}
const HOUR_OPTIONS = Array.from({ length: 49 }, (_, i) => i * 30); // 00:00..24:00

function tzOptions(): string[] {
  try {
    return Intl.supportedValuesOf('timeZone');
  } catch {
    return [];
  }
}

/**
 * Settings → "Calendar automation" (M2.8 Task 5): working hours bounding the
 * automation engine, FocusGuard goal + auto-decline, meeting buffers, OOO
 * auto-decline, travel buffers / leave alerts, and weather. Saves the whole
 * form as one PATCH — the server validates the merged document (400) and
 * gates it behind the subscription (402).
 */
export function CalendarAutomationSection() {
  const queryClient = useQueryClient();
  const prefsQuery = useQuery({ queryKey: ['calendar-prefs'], queryFn: fetchCalendarPrefs });
  const zones = React.useMemo(tzOptions, []);

  const [form, setForm] = React.useState<CalendarPrefs | null>(null);
  const [homeLat, setHomeLat] = React.useState('');
  const [homeLon, setHomeLon] = React.useState('');
  const hydrated = React.useRef(false);

  React.useEffect(() => {
    if (prefsQuery.data && !hydrated.current) {
      setForm(prefsQuery.data);
      setHomeLat(prefsQuery.data.homeLat === null ? '' : String(prefsQuery.data.homeLat));
      setHomeLon(prefsQuery.data.homeLon === null ? '' : String(prefsQuery.data.homeLon));
      hydrated.current = true;
    }
  }, [prefsQuery.data]);

  const set = React.useCallback(<K extends keyof CalendarPrefs>(key: K, value: CalendarPrefs[K]) => {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));
  }, []);

  const toggleDay = (day: number) => {
    setForm((prev) => {
      if (!prev) return prev;
      const on = prev.workDays.includes(day);
      const workDays = on
        ? prev.workDays.filter((d) => d !== day)
        : [...prev.workDays, day].sort((a, b) => a - b);
      return { ...prev, workDays };
    });
  };

  const save = useMutation({
    mutationFn: () => {
      if (!form) return Promise.reject(new Error('not hydrated'));
      const lat = homeLat.trim() === '' ? null : Number(homeLat);
      const lon = homeLon.trim() === '' ? null : Number(homeLon);
      if ((lat !== null && Number.isNaN(lat)) || (lon !== null && Number.isNaN(lon))) {
        return Promise.reject(new ApiRequestError(400, 'validation_failed', 'invalid coordinates'));
      }
      return updateCalendarPrefsApi({ ...form, homeLat: lat, homeLon: lon });
    },
    onSuccess: (p) => {
      queryClient.setQueryData<CalendarPrefs>(['calendar-prefs'], p);
      toast.success('Calendar automation settings saved');
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.status === 400) {
        toast.error('Could not save — some values are invalid. Check the highlighted fields.');
        return;
      }
      if (err instanceof ApiRequestError && err.status === 402) {
        toast.error('Calendar automation requires an active subscription.');
        return;
      }
      toast.error('Could not save calendar automation settings');
    },
  });

  const focusHours = form ? Math.round(form.focusGoalMinutesPerWeek / 60) : 0;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Calendar automation</CardTitle>
        <CardDescription>
          FocusGuard, meeting buffers, out-of-office auto-decline, travel buffers, and weather
          - all bounded by the working hours below.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {!form ? (
          <>
            <Skeleton className="h-9 w-48" />
            <Skeleton className="h-24 w-full" />
          </>
        ) : (
          <>
            {/* Working hours */}
            <div className="flex flex-col gap-3">
              <div className="grid gap-1.5 sm:max-w-xs">
                <Label htmlFor="cal-auto-tz">Time zone</Label>
                <Input
                  id="cal-auto-tz"
                  value={form.timeZone}
                  onChange={(e) => set('timeZone', e.target.value)}
                  list="cal-auto-tz-options"
                  placeholder="America/New_York"
                />
                <datalist id="cal-auto-tz-options">
                  {zones.map((z) => (
                    <option key={z} value={z} />
                  ))}
                </datalist>
              </div>
              <div className="grid gap-1.5">
                <Label>Working days</Label>
                <div className="flex flex-wrap gap-1.5">
                  {DAY_LABELS.map((label, day) => {
                    const on = form.workDays.includes(day);
                    return (
                      <Button
                        key={label}
                        type="button"
                        size="sm"
                        variant={on ? 'default' : 'outline'}
                        aria-pressed={on}
                        className={cn('w-12', !on && 'text-muted-foreground')}
                        onClick={() => toggleDay(day)}
                      >
                        {label}
                      </Button>
                    );
                  })}
                </div>
              </div>
              <div className="flex flex-wrap gap-4">
                <div className="grid gap-1.5">
                  <Label htmlFor="cal-auto-start">Workday starts</Label>
                  <Select
                    value={String(form.workdayStartMinutes)}
                    onValueChange={(v) => set('workdayStartMinutes', Number(v))}
                  >
                    <SelectTrigger id="cal-auto-start" className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {HOUR_OPTIONS.slice(0, -1).map((m) => (
                        <SelectItem key={m} value={String(m)}>
                          {minutesLabel(m)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="cal-auto-end">Workday ends</Label>
                  <Select
                    value={String(form.workdayEndMinutes)}
                    onValueChange={(v) => set('workdayEndMinutes', Number(v))}
                  >
                    <SelectTrigger id="cal-auto-end" className="w-28">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {HOUR_OPTIONS.slice(1).map((m) => (
                        <SelectItem key={m} value={String(m)}>
                          {minutesLabel(m)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
            </div>

            {/* FocusGuard */}
            <div className="flex flex-col gap-3 border-t pt-4">
              <div className="grid gap-1.5">
                <Label htmlFor="cal-auto-focus-goal">
                  Weekly focus goal{' '}
                  <span className="font-normal text-muted-foreground">
                    {focusHours === 0 ? '(off)' : `(${focusHours} h/week)`}
                  </span>
                </Label>
                <input
                  id="cal-auto-focus-goal"
                  type="range"
                  min={0}
                  max={40}
                  step={1}
                  value={focusHours}
                  onChange={(e) => set('focusGoalMinutesPerWeek', Number(e.target.value) * 60)}
                  className="h-2 w-full max-w-sm cursor-pointer accent-primary"
                />
                <p className="text-xs text-muted-foreground">
                  FocusGuard schedules protected focus blocks until the weekly goal is met. 0 turns
                  it off.
                </p>
              </div>
              <div className="flex items-center gap-2">
                <Switch
                  id="cal-auto-focus-decline"
                  checked={form.focusAutoDecline}
                  onCheckedChange={(v) => set('focusAutoDecline', v)}
                  disabled={form.focusGoalMinutesPerWeek === 0}
                />
                <Label htmlFor="cal-auto-focus-decline">Auto-decline invites during focus blocks</Label>
              </div>
              {form.focusAutoDecline && (
                <div className="grid gap-1.5 sm:max-w-md">
                  <Label htmlFor="cal-auto-focus-msg">Focus decline message</Label>
                  <Input
                    id="cal-auto-focus-msg"
                    value={form.focusDeclineMessage}
                    onChange={(e) => set('focusDeclineMessage', e.target.value)}
                    placeholder="I'm in focus time - happy to find another slot."
                  />
                </div>
              )}
            </div>

            {/* Buffers */}
            <div className="flex flex-col gap-3 border-t pt-4">
              <div className="grid gap-1.5">
                <Label htmlFor="cal-auto-buffer">Buffer between meetings</Label>
                <Select
                  value={String(form.autoBufferMinutes)}
                  onValueChange={(v) => set('autoBufferMinutes', Number(v))}
                >
                  <SelectTrigger id="cal-auto-buffer" className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {BUFFER_OPTIONS.map((m) => (
                      <SelectItem key={m} value={String(m)}>
                        {m === 0 ? 'Off' : `${m} min`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  Automatically keeps breathing room around back-to-back meetings.
                </p>
              </div>
            </div>

            {/* Out of office */}
            <div className="flex flex-col gap-3 border-t pt-4">
              <div className="flex items-center gap-2">
                <Switch
                  id="cal-auto-ooo"
                  checked={form.oooAutoDecline}
                  onCheckedChange={(v) => set('oooAutoDecline', v)}
                />
                <Label htmlFor="cal-auto-ooo">Auto-decline invites while out of office</Label>
              </div>
              {form.oooAutoDecline && (
                <div className="grid gap-1.5 sm:max-w-md">
                  <Label htmlFor="cal-auto-ooo-msg">Out-of-office decline message</Label>
                  <Textarea
                    id="cal-auto-ooo-msg"
                    value={form.oooDeclineMessage}
                    onChange={(e) => set('oooDeclineMessage', e.target.value)}
                    placeholder="I'm out of office and will respond when I'm back."
                    rows={2}
                  />
                </div>
              )}
            </div>

            {/* Travel */}
            <div className="flex flex-col gap-3 border-t pt-4">
              <div className="flex items-center gap-2">
                <Switch
                  id="cal-auto-travel"
                  checked={form.travelBuffers}
                  onCheckedChange={(v) => set('travelBuffers', v)}
                />
                <Label htmlFor="cal-auto-travel">Travel buffers before and after located events</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch
                  id="cal-auto-leave"
                  checked={form.leaveAlerts}
                  onCheckedChange={(v) => set('leaveAlerts', v)}
                />
                <Label htmlFor="cal-auto-leave">Time-to-leave alerts</Label>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="cal-auto-travel-mode">Travel mode</Label>
                <Select
                  value={form.travelMode}
                  onValueChange={(v) => set('travelMode', v as TravelMode)}
                >
                  <SelectTrigger id="cal-auto-travel-mode" className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TRAVEL_MODES.map((m) => (
                      <SelectItem key={m.value} value={m.value}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-wrap gap-4">
                <div className="grid gap-1.5">
                  <Label htmlFor="cal-auto-home-lat">Home latitude</Label>
                  <Input
                    id="cal-auto-home-lat"
                    type="number"
                    inputMode="decimal"
                    className="w-36"
                    value={homeLat}
                    onChange={(e) => setHomeLat(e.target.value)}
                    placeholder="52.37"
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="cal-auto-home-lon">Home longitude</Label>
                  <Input
                    id="cal-auto-home-lon"
                    type="number"
                    inputMode="decimal"
                    className="w-36"
                    value={homeLon}
                    onChange={(e) => setHomeLon(e.target.value)}
                    placeholder="4.89"
                  />
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                Home coordinates anchor travel time for the first and last event of the day.
              </p>
            </div>

            {/* Weather */}
            <div className="flex items-center gap-2 border-t pt-4">
              <Switch
                id="cal-auto-weather"
                checked={form.weatherEnabled}
                onCheckedChange={(v) => set('weatherEnabled', v)}
              />
              <Label htmlFor="cal-auto-weather">Show weather on outdoor and located events</Label>
            </div>
          </>
        )}
      </CardContent>
      <CardFooter className="border-t pt-6">
        <Button onClick={() => save.mutate()} disabled={!form || save.isPending}>
          {save.isPending && <Loader2 className="animate-spin" />}
          Save
        </Button>
      </CardFooter>
    </Card>
  );
}
