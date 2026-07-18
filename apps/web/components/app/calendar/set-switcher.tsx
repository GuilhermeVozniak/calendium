'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Loader2, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import type { CalendarSet, CalendarSetInput } from '@calendium/shared';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
import { Skeleton } from '@/components/ui/skeleton';
import { fetchCalendars } from '@/lib/calendar-data';
import {
  ALL_CALENDARS_SET_ID,
  activateSet,
  allCalendarsSet,
  calendarsMatchSet,
  createCalendarSetApi,
  deleteCalendarSetApi,
  fetchCalendarSets,
  getActiveSetId,
  setActiveSetId,
  updateCalendarSetApi,
} from '@/lib/set-data';

/**
 * Calendar sets: named groups of calendars ("Work", "Home") that can be
 * switched on together. Applying a set PATCHes each of its calendars'
 * server-persisted `isVisible` to true and every other calendar's to false
 * (lib/set-data.ts's activateSet) - visibility is what actually carries
 * across devices, so the set itself only remembers membership. Which set is
 * currently "active" is a client-only preference (localStorage): a manual
 * per-calendar toggle that diverges from the active set silently clears it.
 *
 * Self-contained dialog (own data fetching + mutations), mirroring
 * template-manager.tsx, so it can be opened from both the calendar sidebar
 * and the settings page.
 */

export interface SetSwitcherProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

interface FormState {
  name: string;
}

const EMPTY_FORM: FormState = { name: '' };

export function SetSwitcher({ open, onOpenChange }: SetSwitcherProps) {
  const queryClient = useQueryClient();
  const setsQuery = useQuery({
    queryKey: ['calendar-sets'],
    queryFn: fetchCalendarSets,
    enabled: open,
  });
  const calendarsQuery = useQuery({
    queryKey: ['calendars'],
    queryFn: fetchCalendars,
    enabled: open,
  });

  const sets = React.useMemo(() => setsQuery.data ?? [], [setsQuery.data]);
  const calendars = React.useMemo(() => calendarsQuery.data ?? [], [calendarsQuery.data]);
  const allSets = React.useMemo(() => [allCalendarsSet(calendars), ...sets], [calendars, sets]);

  const [activeId, setActiveIdState] = React.useState<string | null>(null);
  React.useEffect(() => {
    if (open) setActiveIdState(getActiveSetId());
  }, [open]);

  // Self-heal: once any calendar's visibility diverges from what the active
  // set prescribes (a manual per-calendar toggle, applied elsewhere in the
  // app), the "Active" badge - and the stored preference behind it - clears.
  React.useEffect(() => {
    if (!activeId || calendars.length === 0) return;
    const active = allSets.find((s) => s.id === activeId);
    if (active && !calendarsMatchSet(calendars, active)) {
      setActiveSetId(null);
      setActiveIdState(null);
    }
  }, [activeId, calendars, allSets]);

  const [renaming, setRenaming] = React.useState<CalendarSet | null>(null);
  const [formMode, setFormMode] = React.useState<'create' | 'rename' | null>(null);
  const [form, setForm] = React.useState<FormState>(EMPTY_FORM);

  const openCreateForm = () => {
    setRenaming(null);
    setForm(EMPTY_FORM);
    setFormMode('create');
  };

  const openRenameForm = (set: CalendarSet) => {
    setRenaming(set);
    setForm({ name: set.name });
    setFormMode('rename');
  };

  const apply = useMutation({
    mutationFn: (set: CalendarSet) => activateSet(set, calendars),
    onSuccess: (_data, set) => {
      setActiveIdState(set.id);
      void queryClient.invalidateQueries({ queryKey: ['calendars'] });
      toast.success(`Applied "${set.name}"`);
    },
    onError: () => toast.error('Could not apply the set'),
  });

  const save = useMutation({
    mutationFn: () => {
      const name = form.name.trim();
      if (formMode === 'rename' && renaming) {
        const input: CalendarSetInput = {
          name,
          calendarIds: renaming.calendarIds,
          position: renaming.position,
        };
        return updateCalendarSetApi(renaming.id, input);
      }
      const input: CalendarSetInput = {
        name,
        calendarIds: calendars.filter((c) => c.isVisible).map((c) => c.id),
        position: sets.length,
      };
      return createCalendarSetApi(input);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['calendar-sets'] });
      toast.success(formMode === 'rename' ? 'Set renamed' : 'Set saved');
      setFormMode(null);
    },
    onError: () =>
      toast.error(formMode === 'rename' ? 'Could not rename the set' : 'Could not save the set'),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteCalendarSetApi(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<CalendarSet[]>(['calendar-sets'], (prev) =>
        prev?.filter((s) => s.id !== id)
      );
      if (activeId === id) {
        setActiveSetId(null);
        setActiveIdState(null);
      }
      toast.success('Set deleted');
    },
    onError: () => toast.error('Could not delete the set'),
  });

  const loading = calendarsQuery.isLoading || setsQuery.isLoading;

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Calendar sets</DialogTitle>
            <DialogDescription>
              Groups of calendars you can switch on together.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2">
            {loading && (
              <>
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
              </>
            )}
            {!loading &&
              allSets.map((set) => {
                const isAll = set.id === ALL_CALENDARS_SET_ID;
                const isActive = activeId === set.id;
                return (
                  <div key={set.id} className="flex items-center gap-3 rounded-lg border p-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">{set.name}</span>
                        {isActive && <Badge variant="secondary">Active</Badge>}
                      </div>
                      <p className="mt-0.5 truncate text-xs text-muted-foreground">
                        {isAll
                          ? 'Every calendar'
                          : `${set.calendarIds.length} calendar${set.calendarIds.length === 1 ? '' : 's'}`}
                      </p>
                    </div>
                    <Button
                      variant={isActive ? 'secondary' : 'outline'}
                      size="sm"
                      aria-label={`Apply set ${set.name}`}
                      onClick={() => apply.mutate(set)}
                      disabled={apply.isPending}
                    >
                      {isActive && <Check className="size-3.5" />}
                      Apply
                    </Button>
                    {!isAll && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8"
                          aria-label={`Rename set ${set.name}`}
                          onClick={() => openRenameForm(set)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 text-muted-foreground hover:text-destructive"
                          aria-label={`Delete set ${set.name}`}
                          onClick={() => remove.mutate(set.id)}
                          disabled={remove.isPending}
                        >
                          <Trash2 />
                        </Button>
                      </>
                    )}
                  </div>
                );
              })}
          </div>

          <DialogFooter className="sm:justify-between">
            <Button variant="outline" onClick={openCreateForm} disabled={calendars.length === 0}>
              <Plus />
              Save current selection as set…
            </Button>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={formMode !== null} onOpenChange={(v) => !v && setFormMode(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{formMode === 'rename' ? 'Rename set' : 'Save current selection'}</DialogTitle>
            {formMode === 'create' && (
              <DialogDescription>
                Creates a set from the calendars currently visible.
              </DialogDescription>
            )}
          </DialogHeader>
          <div className="grid gap-1.5">
            <Label htmlFor="set-name">Name</Label>
            <Input
              id="set-name"
              value={form.name}
              onChange={(e) => setForm({ name: e.target.value })}
              placeholder="Work"
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setFormMode(null)}>
              Cancel
            </Button>
            <Button onClick={() => save.mutate()} disabled={!form.name.trim() || save.isPending}>
              {save.isPending && <Loader2 className="animate-spin" />}
              {formMode === 'rename' ? 'Save changes' : 'Create set'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
