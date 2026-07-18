'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Loader2, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import type { AvailabilityWindow, BookingLink, BookingLinkInput } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
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
import { fetchCalendars } from '@/lib/calendar-data';
import {
  createBookingLinkApi,
  deleteBookingLinkApi,
  fetchBookingLinks,
  updateBookingLinkApi,
} from '@/lib/scheduling-data';

/**
 * Booking-link manager (Settings > Scheduling): list, create/edit dialog with
 * a weekly-windows editor, buffers, daily limit, and a copy-public-URL button.
 * The public URL is derived from window.location.origin (the web app serves
 * /book/{slug} itself) rather than the API base — Task 14 brief, honesty
 * policy: copy-link buttons must copy a real, working public URL.
 */

export const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/;
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

export function localTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return 'UTC';
  }
}

export function publicBookingUrl(slug: string): string {
  return `${window.location.origin}/book/${slug}`;
}

/**
 * Shared weekly-windows row editor (weekday select + start/end time inputs),
 * reused by the booking-link form here and the working-hours editor in
 * Settings > Scheduling — both edit the same AvailabilityWindow[] shape.
 */
export function WindowsEditor({
  windows,
  onChange,
  addLabel = 'Add window',
  minRows = 1,
}: {
  windows: AvailabilityWindow[];
  onChange: (next: AvailabilityWindow[]) => void;
  addLabel?: string;
  /** Fewest rows the "Remove" button will allow (0 = can clear to "no constraint"). */
  minRows?: number;
}) {
  const addWindow = () => {
    onChange([...windows, { weekday: 1, start: '09:00', end: '17:00' }]);
  };
  const removeWindow = (index: number) => {
    onChange(windows.filter((_, i) => i !== index));
  };
  const updateWindow = (index: number, patch: Partial<AvailabilityWindow>) => {
    onChange(windows.map((w, i) => (i === index ? { ...w, ...patch } : w)));
  };

  return (
    <div className="flex flex-col gap-2">
      {windows.map((w, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: rows have no stable identity beyond position (matches mail/page.tsx precedent)
        <div key={i} className="flex items-center gap-2">
          <Select
            value={String(w.weekday)}
            onValueChange={(v) => updateWindow(i, { weekday: Number(v) })}
          >
            <SelectTrigger size="sm" className="w-24" aria-label={`Window ${i + 1} weekday`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {WEEKDAYS.map((label, day) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: `day` is the weekday number (0-6) itself, not an arbitrary position
                <SelectItem key={day} value={String(day)}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Input
            type="time"
            aria-label={`Window ${i + 1} start`}
            value={w.start}
            onChange={(e) => updateWindow(i, { start: e.target.value })}
            className="w-28"
          />
          <span className="text-xs text-muted-foreground">to</span>
          <Input
            type="time"
            aria-label={`Window ${i + 1} end`}
            value={w.end}
            onChange={(e) => updateWindow(i, { end: e.target.value })}
            className="w-28"
          />
          <Button
            variant="ghost"
            size="icon"
            className="size-8 text-muted-foreground hover:text-destructive"
            aria-label={`Remove window ${i + 1}`}
            onClick={() => removeWindow(i)}
            disabled={windows.length <= minRows}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" className="w-fit" onClick={addWindow}>
        <Plus />
        {addLabel}
      </Button>
    </div>
  );
}

interface FormState {
  slug: string;
  title: string;
  description: string;
  calendarId: string;
  durationMinutes: string;
  timeZone: string;
  windows: AvailabilityWindow[];
  bufferBeforeMin: string;
  bufferAfterMin: string;
  dailyLimit: string;
  minNoticeMin: string;
  maxAdvanceDays: string;
  respectWorkingHours: boolean;
  addConferencing: boolean;
  active: boolean;
}

function emptyForm(): FormState {
  return {
    slug: '',
    title: '',
    description: '',
    calendarId: '',
    durationMinutes: '30',
    timeZone: localTimeZone(),
    windows: [{ weekday: 1, start: '09:00', end: '17:00' }],
    bufferBeforeMin: '0',
    bufferAfterMin: '0',
    dailyLimit: '0',
    minNoticeMin: '60',
    maxAdvanceDays: '30',
    respectWorkingHours: true,
    addConferencing: false,
    active: true,
  };
}

function toInput(form: FormState): BookingLinkInput {
  return {
    slug: form.slug.trim(),
    title: form.title.trim(),
    description: form.description.trim() || undefined,
    calendarId: form.calendarId,
    durationMinutes: Math.max(5, Number(form.durationMinutes) || 30),
    timeZone: form.timeZone,
    windows: form.windows,
    bufferBeforeMin: Math.max(0, Number(form.bufferBeforeMin) || 0),
    bufferAfterMin: Math.max(0, Number(form.bufferAfterMin) || 0),
    dailyLimit: Math.max(0, Number(form.dailyLimit) || 0),
    minNoticeMin: Math.max(0, Number(form.minNoticeMin) || 0),
    maxAdvanceDays: Math.max(0, Number(form.maxAdvanceDays) || 0),
    respectWorkingHours: form.respectWorkingHours,
    addConferencing: form.addConferencing,
    active: form.active,
  };
}

/**
 * Reads a one-shot `?new=link` query param (set by the command palette's
 * "Create booking link" action) to auto-open the create dialog, then strips
 * the param so a reload/back-nav doesn't reopen it.
 */
export function useAutoOpenCreate(marker: string, open: () => void) {
  // biome-ignore lint/correctness/useExhaustiveDependencies: intentional mount-only effect — re-running on `open`/`marker` identity changes would fight the param removal below
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get('new') !== marker) return;
    open();
    params.delete('new');
    const next = params.toString();
    window.history.replaceState(
      null,
      '',
      `${window.location.pathname}${next ? `?${next}` : ''}${window.location.hash}`
    );
  }, []);
}

export function BookingLinks() {
  const queryClient = useQueryClient();
  const linksQuery = useQuery({ queryKey: ['booking-links'], queryFn: fetchBookingLinks });
  const calendarsQuery = useQuery({ queryKey: ['calendars'], queryFn: fetchCalendars });

  const [editing, setEditing] = React.useState<BookingLink | null>(null);
  const [formOpen, setFormOpen] = React.useState(false);
  const [form, setForm] = React.useState<FormState>(emptyForm());
  const [slugError, setSlugError] = React.useState<string | null>(null);

  const links = linksQuery.data ?? [];
  const calendars = calendarsQuery.data ?? [];

  const openCreateForm = React.useCallback(() => {
    setEditing(null);
    setForm(emptyForm());
    setSlugError(null);
    setFormOpen(true);
  }, []);

  useAutoOpenCreate('link', openCreateForm);

  const openEditForm = (link: BookingLink) => {
    setEditing(link);
    setForm({
      slug: link.slug,
      title: link.title,
      description: link.description ?? '',
      calendarId: link.calendarId,
      durationMinutes: String(link.durationMinutes),
      timeZone: link.timeZone,
      windows:
        link.windows.length > 0 ? link.windows : [{ weekday: 1, start: '09:00', end: '17:00' }],
      bufferBeforeMin: String(link.bufferBeforeMin),
      bufferAfterMin: String(link.bufferAfterMin),
      dailyLimit: String(link.dailyLimit),
      minNoticeMin: String(link.minNoticeMin),
      maxAdvanceDays: String(link.maxAdvanceDays),
      respectWorkingHours: link.respectWorkingHours,
      addConferencing: link.addConferencing,
      active: link.active,
    });
    setSlugError(null);
    setFormOpen(true);
  };

  const slugValid = SLUG_RE.test(form.slug.trim());

  const save = useMutation({
    mutationFn: () => {
      const input = toInput(form);
      return editing ? updateBookingLinkApi(editing.id, input) : createBookingLinkApi(input);
    },
    onSuccess: (link) => {
      queryClient.setQueryData<BookingLink[]>(['booking-links'], (prev) => {
        if (!prev) return [link];
        return editing ? prev.map((l) => (l.id === link.id ? link : l)) : [link, ...prev];
      });
      toast.success(editing ? 'Booking link updated' : 'Booking link created');
      setFormOpen(false);
    },
    onError: (err) => {
      if (err instanceof ApiRequestError && err.status === 409) {
        setSlugError(`"${form.slug.trim()}" is already taken - try another slug.`);
        return;
      }
      toast.error(
        editing ? 'Could not update the booking link' : 'Could not create the booking link'
      );
    },
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteBookingLinkApi(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<BookingLink[]>(['booking-links'], (prev) =>
        prev?.filter((l) => l.id !== id)
      );
      toast.success('Booking link deleted');
    },
    onError: () => toast.error('Could not delete the booking link'),
  });

  const copyLink = async (slug: string) => {
    try {
      await navigator.clipboard.writeText(publicBookingUrl(slug));
      toast.success('Booking link copied to clipboard');
    } catch {
      toast.error('Clipboard unavailable - copy the link manually.');
    }
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Booking links</CardTitle>
          <CardDescription>
            Personal scheduling pages - share a link, let people book straight onto your calendar.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={openCreateForm}>
              <Plus />
              New booking link
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {linksQuery.isLoading && (
            <>
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </>
          )}
          {!linksQuery.isLoading && links.length === 0 && (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              No booking links yet. Create one to let people schedule time with you.
            </p>
          )}
          {links.map((link) => (
            <div key={link.id} className="flex items-center gap-3 rounded-lg border p-3">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium">{link.title}</span>
                  <Badge variant="outline" className="font-normal">
                    {link.durationMinutes}m
                  </Badge>
                  {!link.active && <Badge variant="secondary">Inactive</Badge>}
                </div>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">/book/{link.slug}</p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="gap-1.5 text-muted-foreground"
                onClick={() => void copyLink(link.slug)}
              >
                <Copy className="size-3.5" />
                Copy link
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                aria-label={`Edit ${link.title}`}
                onClick={() => openEditForm(link)}
              >
                <Pencil />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-8 text-muted-foreground hover:text-destructive"
                aria-label={`Delete ${link.title}`}
                onClick={() => remove.mutate(link.id)}
                disabled={remove.isPending}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </CardContent>
      </Card>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? 'Edit booking link' : 'New booking link'}</DialogTitle>
            <DialogDescription>
              Choose a slug, duration, and weekly availability windows.
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="link-title">Title</Label>
              <Input
                id="link-title"
                value={form.title}
                onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                placeholder="30-minute intro call"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="link-slug">Slug</Label>
              <Input
                id="link-slug"
                value={form.slug}
                onChange={(e) => {
                  setForm((f) => ({ ...f, slug: e.target.value.toLowerCase() }));
                  setSlugError(null);
                }}
                placeholder="intro-call"
              />
              <p className="text-xs text-muted-foreground">
                {publicBookingUrl(form.slug.trim() || '…')}
              </p>
              {form.slug.trim().length > 0 && !slugValid && (
                <p className="text-xs text-destructive">
                  Lowercase letters, digits, and hyphens only (no leading/trailing hyphen).
                </p>
              )}
              {slugError && <p className="text-xs text-destructive">{slugError}</p>}
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="grid gap-1.5">
                <Label htmlFor="link-duration">Duration (minutes)</Label>
                <Input
                  id="link-duration"
                  type="number"
                  min={5}
                  step={5}
                  value={form.durationMinutes}
                  onChange={(e) => setForm((f) => ({ ...f, durationMinutes: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="link-calendar">Calendar</Label>
                <Select
                  value={form.calendarId}
                  onValueChange={(v) => setForm((f) => ({ ...f, calendarId: v }))}
                >
                  <SelectTrigger id="link-calendar" size="sm">
                    <SelectValue placeholder="Choose a calendar" />
                  </SelectTrigger>
                  <SelectContent>
                    {calendars
                      .filter((c) => c.canWrite)
                      .map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          {c.name}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="link-description">Description</Label>
              <Textarea
                id="link-description"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                rows={2}
              />
            </div>

            <div className="grid gap-1.5">
              <Label>Availability windows</Label>
              <WindowsEditor
                windows={form.windows}
                onChange={(windows) => setForm((f) => ({ ...f, windows }))}
              />
            </div>

            <div className="grid grid-cols-2 gap-2">
              <div className="grid gap-1.5">
                <Label htmlFor="link-buffer-before">Buffer before (min)</Label>
                <Input
                  id="link-buffer-before"
                  type="number"
                  min={0}
                  value={form.bufferBeforeMin}
                  onChange={(e) => setForm((f) => ({ ...f, bufferBeforeMin: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="link-buffer-after">Buffer after (min)</Label>
                <Input
                  id="link-buffer-after"
                  type="number"
                  min={0}
                  value={form.bufferAfterMin}
                  onChange={(e) => setForm((f) => ({ ...f, bufferAfterMin: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="link-daily-limit">Daily limit (0 = unlimited)</Label>
                <Input
                  id="link-daily-limit"
                  type="number"
                  min={0}
                  value={form.dailyLimit}
                  onChange={(e) => setForm((f) => ({ ...f, dailyLimit: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="link-min-notice">Minimum notice (min)</Label>
                <Input
                  id="link-min-notice"
                  type="number"
                  min={0}
                  value={form.minNoticeMin}
                  onChange={(e) => setForm((f) => ({ ...f, minNoticeMin: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="link-max-advance">Max advance (days)</Label>
                <Input
                  id="link-max-advance"
                  type="number"
                  min={0}
                  value={form.maxAdvanceDays}
                  onChange={(e) => setForm((f) => ({ ...f, maxAdvanceDays: e.target.value }))}
                />
              </div>
            </div>

            <div className="flex items-center gap-2">
              <Switch
                id="link-respect-hours"
                checked={form.respectWorkingHours}
                onCheckedChange={(v) => setForm((f) => ({ ...f, respectWorkingHours: v }))}
              />
              <Label htmlFor="link-respect-hours" className="font-normal">
                Respect working hours
              </Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="link-conferencing"
                checked={form.addConferencing}
                onCheckedChange={(v) => setForm((f) => ({ ...f, addConferencing: v }))}
              />
              <Label htmlFor="link-conferencing" className="font-normal">
                Add video conferencing
              </Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="link-active"
                checked={form.active}
                onCheckedChange={(v) => setForm((f) => ({ ...f, active: v }))}
              />
              <Label htmlFor="link-active" className="font-normal">
                Active
              </Label>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setFormOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => save.mutate()}
              disabled={
                !form.title.trim() ||
                !slugValid ||
                !form.calendarId ||
                form.windows.length === 0 ||
                save.isPending
              }
            >
              {save.isPending && <Loader2 className="animate-spin" />}
              {editing ? 'Save changes' : 'Create booking link'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
