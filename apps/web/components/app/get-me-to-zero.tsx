'use client';

import { toast } from 'sonner';

import { TimePickerDialog } from '@/components/app/snooze-menu';
import { zeroCutoffOptions } from '@/lib/mail-utils';
import { useMailActions } from '@/lib/use-mail';

export interface GetMeToZeroProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone?: (archivedCount: number) => void;
}

/** "Get Me To Zero": one command bulk-archives inbox mail older than a period. */
export function GetMeToZero({ open, onOpenChange, onDone }: GetMeToZeroProps) {
  const { getMeToZero } = useMailActions();
  return (
    <TimePickerDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Archive everything older than…"
      options={zeroCutoffOptions()}
      onPick={(when) => {
        void getMeToZero(when.toISOString())
          .then((count) => {
            toast.success(
              count === 0
                ? 'Nothing that old — you were already close to zero'
                : `Archived ${count} conversation${count === 1 ? '' : 's'} — welcome to zero`
            );
            onDone?.(count);
          })
          .catch(() => toast.error('Could not run Get Me To Zero.'));
      }}
    />
  );
}
