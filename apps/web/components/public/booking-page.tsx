'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Check, Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import {
  ApiRequestError,
  createPublicBooking,
  fetchPublicBookingPage,
  fetchPublicSlots,
  type AvailabilitySlot,
  type Booking,
  type PublicBookingPage as PublicBookingPageDoc,
} from '@calendium/shared';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
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
import { Textarea } from '@/components/ui/textarea';
import { env } from '@/lib/env';
import { browserTimeZone, formatInTZ, listTimeZones, tzAbbrev } from '@/lib/timezone';

const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

type PageStage = 'loading' | 'not-found' | 'error' | 'ready';

interface FormState {
  name: string;
  email: string;
  note: string;
}

const EMPTY_FORM: FormState = { name: '', email: '', note: '' };

/** Stable per-day grouping key for `iso` as observed in `timeZone` (YYYY-MM-DD). */
function dayKey(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(iso));
}

function isEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
}

export function PublicBookingPage({ slug }: { slug: string }) {
  const [stage, setStage] = useState<PageStage>('loading');
  const [page, setPage] = useState<PublicBookingPageDoc | null>(null);

  const [timeZone, setTimeZone] = useState<string>('UTC');
  useEffect(() => {
    // Deferred to an effect so the server-rendered markup and the first
    // client render agree (Intl's resolved zone can differ from any static
    // default), avoiding a hydration mismatch.
    setTimeZone(browserTimeZone());
  }, []);
  const zones = useMemo(() => listTimeZones(), []);

  const [weekStart, setWeekStart] = useState<Date | null>(null);
  useEffect(() => {
    setWeekStart(new Date());
  }, []);

  const [slots, setSlots] = useState<AvailabilitySlot[] | null>(null);
  const [slotsError, setSlotsError] = useState<string | null>(null);

  const [selectedSlot, setSelectedSlot] = useState<AvailabilitySlot | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [rateLimited, setRateLimited] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [booking, setBooking] = useState<Booking | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchPublicBookingPage(env.apiUrl, slug)
      .then((doc) => {
        if (cancelled) return;
        setPage(doc);
        setStage('ready');
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiRequestError && err.status === 404) {
          setStage('not-found');
        } else {
          setStage('error');
        }
      });
    return () => {
      cancelled = true;
    };
  }, [slug]);

  const loadSlots = useCallback(
    (start: Date) => {
      setSlots(null);
      setSlotsError(null);
      const from = start.toISOString();
      const to = new Date(start.getTime() + WEEK_MS).toISOString();
      return fetchPublicSlots(env.apiUrl, slug, from, to)
        .then((res) => {
          setSlots(res);
        })
        .catch(() => {
          setSlots([]);
          setSlotsError('Could not load available times. Please try again.');
        });
    },
    [slug]
  );

  useEffect(() => {
    if (stage !== 'ready' || !weekStart) return;
    void loadSlots(weekStart);
  }, [stage, weekStart, loadSlots]);

  const groupedByDay = useMemo(() => {
    if (!slots) return [];
    const groups = new Map<string, AvailabilitySlot[]>();
    for (const slot of slots) {
      const key = dayKey(slot.start, timeZone);
      const list = groups.get(key);
      if (list) list.push(slot);
      else groups.set(key, [slot]);
    }
    return Array.from(groups.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, daySlots]) => ({
        key,
        label: formatInTZ(daySlots[0].start, timeZone, 'weekday-date'),
        slots: daySlots.sort((a, b) => a.start.localeCompare(b.start)),
      }));
  }, [slots, timeZone]);

  function selectSlot(slot: AvailabilitySlot) {
    setSelectedSlot(slot);
    setForm(EMPTY_FORM);
    setFieldError(null);
    setSubmitError(null);
    setRateLimited(false);
  }

  function cancelSelection() {
    setSelectedSlot(null);
    setFieldError(null);
    setSubmitError(null);
    setRateLimited(false);
  }

  async function submitBooking() {
    if (!selectedSlot) return;
    if (!form.name.trim() || !form.email.trim()) {
      setFieldError('Name and email are required.');
      return;
    }
    if (!isEmail(form.email.trim())) {
      setFieldError('Enter a valid email address.');
      return;
    }
    setFieldError(null);
    setSubmitError(null);
    setRateLimited(false);
    setSubmitting(true);
    try {
      const result = await createPublicBooking(env.apiUrl, slug, {
        start: selectedSlot.start,
        inviteeName: form.name.trim(),
        inviteeEmail: form.email.trim(),
        inviteeTimeZone: timeZone,
        note: form.note.trim() || undefined,
      });
      setBooking(result);
    } catch (err) {
      if (err instanceof ApiRequestError && err.status === 409) {
        toast.error('That slot was just taken');
        setSelectedSlot(null);
        if (weekStart) void loadSlots(weekStart);
      } else if (err instanceof ApiRequestError && err.status === 429) {
        setRateLimited(true);
      } else if (err instanceof ApiRequestError && err.status === 400) {
        setSubmitError('Please check your details and try again.');
      } else {
        toast.error('Something went wrong booking that time. Please try again.');
      }
    } finally {
      setSubmitting(false);
    }
  }

  if (stage === 'loading') {
    return (
      <div className="mx-auto w-full max-w-2xl px-6 py-16" data-testid="booking-page-loading">
        <Skeleton className="h-8 w-2/3" />
        <Skeleton className="mt-4 h-4 w-1/3" />
        <Skeleton className="mt-8 h-64 w-full" />
      </div>
    );
  }

  if (stage === 'not-found') {
    return (
      <div className="mx-auto w-full max-w-2xl px-6 py-24 text-center">
        <h1 className="text-xl font-semibold tracking-tight">This booking link doesn’t exist</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          It may have been renamed or is no longer active. Check the link and try again.
        </p>
      </div>
    );
  }

  if (stage === 'error' || !page) {
    return (
      <div className="mx-auto w-full max-w-2xl px-6 py-24 text-center">
        <h1 className="text-xl font-semibold tracking-tight">Something went wrong</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          We couldn’t load this booking page. Please refresh and try again.
        </p>
      </div>
    );
  }

  if (booking) {
    return (
      <div className="mx-auto w-full max-w-2xl px-6 py-16">
        <Card>
          <CardContent className="flex flex-col items-center gap-4 py-10 text-center">
            <div className="flex size-12 items-center justify-center rounded-full bg-primary/10 text-primary">
              <Check className="size-6" />
            </div>
            <div>
              <h1 className="text-xl font-semibold tracking-tight">You’re booked!</h1>
              <p className="mt-1 text-sm text-muted-foreground">
                A confirmation has been sent to {booking.inviteeEmail}.
              </p>
            </div>
            <div className="mt-2 rounded-lg border bg-muted/30 px-6 py-4">
              <p className="text-base font-medium">{formatInTZ(booking.start, timeZone, 'datetime')}</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {formatInTZ(booking.start, page.timeZone, 'datetime')} for {page.ownerName} (
                {tzAbbrev(page.timeZone, new Date(booking.start))})
              </p>
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-2xl px-6 py-16">
      <Card>
        <CardHeader>
          <CardTitle className="text-xl">{page.title}</CardTitle>
          <CardDescription>
            with {page.ownerName} · {page.durationMinutes} min
          </CardDescription>
          {page.description && (
            <p className="mt-2 text-sm text-muted-foreground">{page.description}</p>
          )}
        </CardHeader>
        <CardContent>
          {selectedSlot ? (
            <div className="flex flex-col gap-4">
              <div>
                <p className="text-sm text-muted-foreground">Selected time</p>
                <p className="text-base font-medium">
                  {formatInTZ(selectedSlot.start, timeZone, 'datetime')}
                </p>
              </div>

              {rateLimited && (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  Too many requests. Please wait a moment and try again.
                </p>
              )}
              {submitError && (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {submitError}
                </p>
              )}
              {fieldError && (
                <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {fieldError}
                </p>
              )}

              <div className="flex flex-col gap-1.5">
                <Label htmlFor="booking-name">Name</Label>
                <Input
                  id="booking-name"
                  value={form.name}
                  onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                  disabled={submitting}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="booking-email">Email</Label>
                <Input
                  id="booking-email"
                  type="email"
                  value={form.email}
                  onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                  disabled={submitting}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="booking-note">Note (optional)</Label>
                <Textarea
                  id="booking-note"
                  value={form.note}
                  onChange={(e) => setForm((f) => ({ ...f, note: e.target.value }))}
                  disabled={submitting}
                />
              </div>

              <div className="flex gap-2">
                <Button variant="outline" onClick={cancelSelection} disabled={submitting}>
                  Back
                </Button>
                <Button onClick={() => void submitBooking()} disabled={submitting}>
                  {submitting && <Loader2 className="animate-spin" />}
                  Confirm booking
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-6">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="booking-tz">Times shown in</Label>
                <Select value={timeZone} onValueChange={setTimeZone}>
                  <SelectTrigger id="booking-tz" aria-label="Times shown in" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {zones.map((zone) => (
                      <SelectItem key={zone} value={zone}>
                        {zone.replace(/_/g, ' ')}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              {slots === null ? (
                <div className="flex flex-col gap-3">
                  <Skeleton className="h-5 w-1/3" />
                  <Skeleton className="h-9 w-full" />
                  <Skeleton className="h-9 w-full" />
                </div>
              ) : slotsError ? (
                <p className="text-sm text-destructive">{slotsError}</p>
              ) : groupedByDay.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No times available this week. Check back soon.
                </p>
              ) : (
                <div className="flex flex-col gap-5">
                  {groupedByDay.map((day) => (
                    <div key={day.key}>
                      <p className="text-sm font-medium">{day.label}</p>
                      <div className="mt-2 grid grid-cols-3 gap-2">
                        {day.slots.map((slot) => (
                          <Button
                            key={slot.start}
                            variant="outline"
                            onClick={() => selectSlot(slot)}>
                            {formatInTZ(slot.start, timeZone, 'time')}
                          </Button>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
