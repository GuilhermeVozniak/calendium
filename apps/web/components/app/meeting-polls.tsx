'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { addDays, endOfDay, format } from 'date-fns';
import { CalendarCheck2, Copy, Loader2, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import type { MeetingPoll, PollInput } from '@calendium/shared';

import { useAutoOpenCreate } from '@/components/app/booking-links';
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
import { Textarea } from '@/components/ui/textarea';
import { fetchAvailability, fetchCalendars } from '@/lib/calendar-data';
import { confirmPollApi, createPollApi, deletePollApi, fetchPolls } from '@/lib/scheduling-data';
import { cn } from '@/lib/utils';

/**
 * Meeting-poll manager (Settings > Scheduling): poll list + a create dialog
 * that picks candidate slots from the organizer's own availability
 * (lib/calendar-data.ts's fetchAvailability - the same feed the "Share
 * availability" dialog uses), plus a confirm-winner flow that surfaces the
 * created event once a poll is confirmed.
 */

const STATUS_META: Record<
  MeetingPoll['status'],
  { label: string; variant: 'outline' | 'secondary' | 'default' }
> = {
  open: { label: 'Open', variant: 'outline' },
  confirmed: { label: 'Confirmed', variant: 'default' },
  cancelled: { label: 'Cancelled', variant: 'secondary' },
};

function publicPollUrl(token: string): string {
  return `${window.location.origin}/poll/${token}`;
}

interface FormState {
  title: string;
  description: string;
  calendarId: string;
  durationMinutes: string;
}

function emptyForm(): FormState {
  return { title: '', description: '', calendarId: '', durationMinutes: '30' };
}

export function MeetingPolls() {
  const queryClient = useQueryClient();
  const pollsQuery = useQuery({ queryKey: ['meeting-polls'], queryFn: fetchPolls });
  const calendarsQuery = useQuery({ queryKey: ['calendars'], queryFn: fetchCalendars });

  const [formOpen, setFormOpen] = React.useState(false);
  const [form, setForm] = React.useState<FormState>(emptyForm());
  const [selectedSlots, setSelectedSlots] = React.useState<ReadonlySet<string>>(new Set());
  const [confirmTarget, setConfirmTarget] = React.useState<MeetingPoll | null>(null);
  const [confirmOptionId, setConfirmOptionId] = React.useState('');

  const polls = pollsQuery.data ?? [];
  const calendars = calendarsQuery.data ?? [];

  const durationMinutes = Math.max(5, Number(form.durationMinutes) || 30);
  const slotsQuery = useQuery({
    queryKey: ['availability', 'poll-candidates', durationMinutes],
    queryFn: () => {
      const from = new Date();
      return fetchAvailability(from, endOfDay(addDays(from, 6)), durationMinutes);
    },
    enabled: formOpen,
  });
  const slots = slotsQuery.data ?? [];

  const openCreateForm = React.useCallback(() => {
    setForm(emptyForm());
    setSelectedSlots(new Set());
    setFormOpen(true);
  }, []);

  useAutoOpenCreate('poll', openCreateForm);

  const toggleSlot = (start: string) => {
    setSelectedSlots((prev) => {
      const next = new Set(prev);
      if (next.has(start)) {
        next.delete(start);
      } else {
        next.add(start);
      }
      return next;
    });
  };

  const selectedCount = selectedSlots.size;

  const create = useMutation({
    mutationFn: () => {
      const options = slots
        .filter((s) => selectedSlots.has(s.start))
        .map((s) => ({ start: s.start, end: s.end }));
      const input: PollInput = {
        title: form.title.trim(),
        description: form.description.trim() || undefined,
        calendarId: form.calendarId,
        durationMinutes,
        options,
      };
      return createPollApi(input);
    },
    onSuccess: (poll) => {
      queryClient.setQueryData<MeetingPoll[]>(['meeting-polls'], (prev) =>
        prev ? [poll, ...prev] : [poll]
      );
      toast.success('Meeting poll created');
      setFormOpen(false);
    },
    onError: () => toast.error('Could not create the meeting poll'),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deletePollApi(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<MeetingPoll[]>(['meeting-polls'], (prev) =>
        prev?.filter((p) => p.id !== id)
      );
      toast.success('Poll deleted');
    },
    onError: () => toast.error('Could not delete the poll'),
  });

  const confirm = useMutation({
    mutationFn: ({ id, optionId }: { id: string; optionId: string }) =>
      confirmPollApi(id, optionId),
    onSuccess: (poll) => {
      queryClient.setQueryData<MeetingPoll[]>(['meeting-polls'], (prev) =>
        prev?.map((p) => (p.id === poll.id ? poll : p))
      );
      toast.success('Winner confirmed - event created');
      setConfirmTarget(null);
    },
    onError: () => toast.error('Could not confirm the winning time'),
  });

  const copyLink = async (token: string) => {
    try {
      await navigator.clipboard.writeText(publicPollUrl(token));
      toast.success('Poll link copied to clipboard');
    } catch {
      toast.error('Clipboard unavailable - copy the link manually.');
    }
  };

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Meeting polls</CardTitle>
          <CardDescription>
            Propose candidate times, let invitees vote, confirm the winner.
          </CardDescription>
          <CardAction>
            <Button size="sm" onClick={openCreateForm}>
              <Plus />
              New meeting poll
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {pollsQuery.isLoading && (
            <>
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </>
          )}
          {!pollsQuery.isLoading && polls.length === 0 && (
            <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
              No meeting polls yet. Create one to find a time that works for everyone.
            </p>
          )}
          {polls.map((poll) => {
            const meta = STATUS_META[poll.status];
            return (
              <div key={poll.id} className="flex items-center gap-3 rounded-lg border p-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium">{poll.title}</span>
                    <Badge variant={meta.variant}>{meta.label}</Badge>
                    {poll.eventId && (
                      <Badge variant="outline" className="gap-1 font-normal">
                        <CalendarCheck2 className="size-3" />
                        Event created
                      </Badge>
                    )}
                  </div>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {poll.options.length} candidate {poll.options.length === 1 ? 'time' : 'times'}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="gap-1.5 text-muted-foreground"
                  onClick={() => void copyLink(poll.token)}
                >
                  <Copy className="size-3.5" />
                  Copy link
                </Button>
                {poll.status === 'open' && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setConfirmTarget(poll);
                      setConfirmOptionId(poll.options[0]?.id ?? '');
                    }}
                  >
                    Confirm winner
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 text-muted-foreground hover:text-destructive"
                  aria-label={`Delete ${poll.title}`}
                  onClick={() => remove.mutate(poll.id)}
                  disabled={remove.isPending}
                >
                  <Trash2 />
                </Button>
              </div>
            );
          })}
        </CardContent>
      </Card>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>New meeting poll</DialogTitle>
            <DialogDescription>
              Pick at least two candidate times from your availability.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="poll-title">Title</Label>
              <Input
                id="poll-title"
                value={form.title}
                onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                placeholder="Team sync — pick a time"
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="grid gap-1.5">
                <Label htmlFor="poll-duration">Duration (minutes)</Label>
                <Input
                  id="poll-duration"
                  type="number"
                  min={5}
                  step={5}
                  value={form.durationMinutes}
                  onChange={(e) => setForm((f) => ({ ...f, durationMinutes: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="poll-calendar">Calendar</Label>
                <Select
                  value={form.calendarId}
                  onValueChange={(v) => setForm((f) => ({ ...f, calendarId: v }))}
                >
                  <SelectTrigger id="poll-calendar" size="sm">
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
              <Label htmlFor="poll-description">Description</Label>
              <Textarea
                id="poll-description"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                rows={2}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Candidate times ({selectedCount} selected)</Label>
              <div className="max-h-52 overflow-y-auto rounded-md border p-2">
                {slotsQuery.isLoading && <Skeleton className="h-8 w-full" />}
                {!slotsQuery.isLoading && slots.length === 0 && (
                  <p className="py-4 text-center text-xs text-muted-foreground">
                    No free slots found in the next week.
                  </p>
                )}
                <div className="flex flex-wrap gap-1.5">
                  {slots.map((slot) => {
                    const isSelected = selectedSlots.has(slot.start);
                    return (
                      <button
                        key={slot.start}
                        type="button"
                        aria-pressed={isSelected}
                        onClick={() => toggleSlot(slot.start)}
                        className={cn(
                          'rounded-md border px-2 py-1 text-xs tabular-nums transition-colors',
                          isSelected
                            ? 'border-primary/50 bg-primary/10 text-foreground'
                            : 'text-muted-foreground opacity-60 hover:opacity-100'
                        )}
                      >
                        {format(new Date(slot.start), 'EEE MMM d, h:mm a')}
                      </button>
                    );
                  })}
                </div>
              </div>
              {selectedCount > 0 && selectedCount < 2 && (
                <p className="text-xs text-destructive">Pick at least 2 candidate times.</p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setFormOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => create.mutate()}
              disabled={
                !form.title.trim() || !form.calendarId || selectedCount < 2 || create.isPending
              }
            >
              {create.isPending && <Loader2 className="animate-spin" />}
              Create poll
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={!!confirmTarget}
        onOpenChange={(open) => {
          if (!open) setConfirmTarget(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Confirm winning time</DialogTitle>
            <DialogDescription>{confirmTarget?.title}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            {confirmTarget?.options.map((opt) => (
              <label
                key={opt.id}
                className="flex items-center gap-2 rounded-md border p-2 text-sm"
              >
                <input
                  type="radio"
                  name="poll-winner"
                  checked={confirmOptionId === opt.id}
                  onChange={() => setConfirmOptionId(opt.id)}
                />
                {format(new Date(opt.start), 'EEE, MMM d')} ·{' '}
                {format(new Date(opt.start), 'h:mm a')} – {format(new Date(opt.end), 'h:mm a')}
              </label>
            ))}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmTarget(null)}>
              Cancel
            </Button>
            <Button
              onClick={() =>
                confirmTarget &&
                confirm.mutate({ id: confirmTarget.id, optionId: confirmOptionId })
              }
              disabled={!confirmOptionId || confirm.isPending}
            >
              {confirm.isPending && <Loader2 className="animate-spin" />}
              Confirm winner
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
