'use client';

import * as React from 'react';
import { CalendarClock } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Separator } from '@/components/ui/separator';
import { formatOptionTime, type TimeOption } from '@/lib/mail-utils';

interface TimePickerDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  options: TimeOption[];
  onPick: (when: Date) => void;
}

/**
 * Superhuman-style time picker used for snooze (z), follow-up reminders (h)
 * and Send Later. Presets are selectable with the 1–4 number keys; a custom
 * date/time row covers everything else.
 */
export function TimePickerDialog({
  open,
  onOpenChange,
  title,
  description,
  options,
  onPick,
}: TimePickerDialogProps) {
  const [custom, setCustom] = React.useState('');

  React.useEffect(() => {
    if (!open) setCustom('');
  }, [open]);

  function pick(when: Date) {
    onOpenChange(false);
    onPick(when);
  }

  function onKeyDown(event: React.KeyboardEvent) {
    const index = Number.parseInt(event.key, 10) - 1;
    if (Number.isNaN(index) || !options[index]) return;
    if (event.target instanceof HTMLInputElement) return;
    event.preventDefault();
    pick(options[index].when);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm gap-0 p-0" onKeyDown={onKeyDown} showCloseButton={false}>
        <DialogHeader className="px-4 pt-4 pb-2">
          <DialogTitle className="flex items-center gap-2 text-sm font-medium">
            <CalendarClock className="text-muted-foreground size-4" />
            {title}
          </DialogTitle>
          {description ? (
            <DialogDescription className="text-xs">{description}</DialogDescription>
          ) : null}
        </DialogHeader>
        <div className="flex flex-col px-2 pb-2">
          {options.map((option, index) => (
            <Button
              key={option.id}
              variant="ghost"
              className="h-9 justify-between px-2 font-normal"
              onClick={() => pick(option.when)}
            >
              <span className="flex items-center gap-2">
                <Kbd size="sm">{index + 1}</Kbd>
                {option.label}
              </span>
              <span className="text-muted-foreground text-xs">{formatOptionTime(option.when)}</span>
            </Button>
          ))}
        </div>
        <Separator />
        <form
          className="flex items-center gap-2 p-3"
          onSubmit={(event) => {
            event.preventDefault();
            const when = new Date(custom);
            if (!custom || Number.isNaN(when.getTime())) return;
            pick(when);
          }}
        >
          <Input
            type="datetime-local"
            value={custom}
            onChange={(event) => setCustom(event.target.value)}
            className="h-8 flex-1 text-xs"
            aria-label="Custom date and time"
          />
          <Button type="submit" size="sm" variant="secondary" disabled={!custom}>
            Set
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}
