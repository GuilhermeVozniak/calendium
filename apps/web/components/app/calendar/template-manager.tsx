'use client';

import * as React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2, Pencil, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import type { EventTemplate, EventTemplateInput } from '@calendium/shared';

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
  createEventTemplateApi,
  deleteEventTemplateApi,
  fetchEventTemplates,
  updateEventTemplateApi,
} from '@/lib/template-data';

/**
 * Saved event defaults ("1:1", "Focus block") a user can apply from the
 * event dialog or command palette. This manager is a self-contained dialog
 * (own data fetching + mutations) so it can be opened from both the calendar
 * sidebar and the settings page without prop drilling calendars/templates.
 */

export interface TemplateManagerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

interface FormState {
  name: string;
  title: string;
  description: string;
  location: string;
  durationMinutes: string;
  calendarId: string;
  allDay: boolean;
}

const EMPTY_FORM: FormState = {
  name: '',
  title: '',
  description: '',
  location: '',
  durationMinutes: '30',
  calendarId: '',
  allDay: false,
};

const NO_CALENDAR = 'default';

export function TemplateManager({ open, onOpenChange }: TemplateManagerProps) {
  const queryClient = useQueryClient();
  const templatesQuery = useQuery({
    queryKey: ['event-templates'],
    queryFn: fetchEventTemplates,
    enabled: open,
  });
  const calendarsQuery = useQuery({
    queryKey: ['calendars'],
    queryFn: fetchCalendars,
    enabled: open,
  });

  const [editing, setEditing] = React.useState<EventTemplate | null>(null);
  const [formOpen, setFormOpen] = React.useState(false);
  const [form, setForm] = React.useState<FormState>(EMPTY_FORM);

  const templates = templatesQuery.data ?? [];
  const calendars = calendarsQuery.data ?? [];

  const openCreateForm = () => {
    setEditing(null);
    setForm(EMPTY_FORM);
    setFormOpen(true);
  };

  const openEditForm = (t: EventTemplate) => {
    setEditing(t);
    setForm({
      name: t.name,
      title: t.title,
      description: t.description,
      location: t.location,
      durationMinutes: String(t.durationMinutes),
      calendarId: t.calendarId ?? '',
      allDay: t.allDay,
    });
    setFormOpen(true);
  };

  const save = useMutation({
    mutationFn: () => {
      const input: EventTemplateInput = {
        name: form.name.trim(),
        title: form.title.trim() || form.name.trim(),
        description: form.description.trim() || undefined,
        location: form.location.trim() || undefined,
        durationMinutes: Math.max(5, Number(form.durationMinutes) || 30),
        allDay: form.allDay,
        calendarId: form.calendarId || null,
      };
      return editing ? updateEventTemplateApi(editing.id, input) : createEventTemplateApi(input);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['event-templates'] });
      toast.success(editing ? 'Template updated' : 'Template created');
      setFormOpen(false);
    },
    onError: () =>
      toast.error(editing ? 'Could not update the template' : 'Could not create the template'),
  });

  const remove = useMutation({
    mutationFn: (id: string) => deleteEventTemplateApi(id),
    onSuccess: (_data, id) => {
      queryClient.setQueryData<EventTemplate[]>(['event-templates'], (prev) =>
        prev?.filter((t) => t.id !== id)
      );
      toast.success('Template deleted');
    },
    onError: () => toast.error('Could not delete the template'),
  });

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Event templates</DialogTitle>
            <DialogDescription>
              Saved defaults you can reuse when creating an event.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-2">
            {templatesQuery.isLoading && (
              <>
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
              </>
            )}
            {!templatesQuery.isLoading && templates.length === 0 && (
              <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
                No templates yet. Create one to speed up event creation.
              </p>
            )}
            {templates.map((t) => (
              <div key={t.id} className="flex items-center gap-3 rounded-lg border p-3">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{t.name}</span>
                    <Badge variant="outline" className="font-normal">
                      {t.durationMinutes}m
                    </Badge>
                  </div>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">{t.title}</p>
                </div>
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  used {t.usageCount}x
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label={`Edit template ${t.name}`}
                  onClick={() => openEditForm(t)}
                >
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 text-muted-foreground hover:text-destructive"
                  aria-label={`Delete template ${t.name}`}
                  onClick={() => remove.mutate(t.id)}
                  disabled={remove.isPending}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>

          <DialogFooter className="sm:justify-between">
            <Button variant="outline" onClick={openCreateForm}>
              <Plus />
              New template
            </Button>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{editing ? 'Edit template' : 'New template'}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="template-name">Name</Label>
              <Input
                id="template-name"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="1:1"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="template-title">Event title</Label>
              <Input
                id="template-title"
                value={form.title}
                onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                placeholder="1:1 with ..."
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="grid gap-1.5">
                <Label htmlFor="template-duration">Duration (minutes)</Label>
                <Input
                  id="template-duration"
                  type="number"
                  min={5}
                  step={5}
                  value={form.durationMinutes}
                  onChange={(e) => setForm((f) => ({ ...f, durationMinutes: e.target.value }))}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="template-calendar">Calendar</Label>
                <Select
                  value={form.calendarId || NO_CALENDAR}
                  onValueChange={(v) =>
                    setForm((f) => ({ ...f, calendarId: v === NO_CALENDAR ? '' : v }))
                  }
                >
                  <SelectTrigger id="template-calendar" size="sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={NO_CALENDAR}>Default calendar</SelectItem>
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
              <Label htmlFor="template-location">Location</Label>
              <Input
                id="template-location"
                value={form.location}
                onChange={(e) => setForm((f) => ({ ...f, location: e.target.value }))}
                placeholder="Add location"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="template-description">Description</Label>
              <Textarea
                id="template-description"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                rows={3}
              />
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="template-all-day"
                checked={form.allDay}
                onCheckedChange={(v) => setForm((f) => ({ ...f, allDay: v }))}
              />
              <Label htmlFor="template-all-day" className="font-normal">
                All day
              </Label>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setFormOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => save.mutate()} disabled={!form.name.trim() || save.isPending}>
              {save.isPending && <Loader2 className="animate-spin" />}
              {editing ? 'Save changes' : 'Create template'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
