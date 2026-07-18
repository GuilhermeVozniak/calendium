'use client';

import type * as React from 'react';
import { Archive, MailCheck, MailX, Tag, X } from 'lucide-react';

import { Kbd } from '@/components/ui/kbd';
import { teachShortcut } from '@/lib/shortcut-hints';
import { cn } from '@/lib/utils';

export interface BulkBarProps {
  count: number;
  onArchive: () => void;
  onMarkRead: () => void;
  onLabel: () => void;
  onUnsubscribe: () => void;
  onClear: () => void;
}

/** Floating action bar shown while a bulk range selection is active. */
export function BulkBar({ count, onArchive, onMarkRead, onLabel, onUnsubscribe, onClear }: BulkBarProps) {
  if (count === 0) return null;
  return (
    <div
      role="toolbar"
      aria-label="Bulk actions"
      className={cn(
        'bg-background absolute bottom-10 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1',
        'rounded-lg border px-2 py-1.5 shadow-lg'
      )}
    >
      <span className="px-1.5 text-xs font-medium">{count} selected</span>
      <BulkButton icon={<Archive className="size-3.5" />} label="Archive" kbd="E" onClick={() => { teachShortcut('archive', 'E', 'Archive'); onArchive(); }} />
      <BulkButton icon={<MailCheck className="size-3.5" />} label="Mark read" kbd="⇧I" onClick={() => { teachShortcut('mark-read', '⇧I', 'Mark read'); onMarkRead(); }} />
      <BulkButton icon={<Tag className="size-3.5" />} label="Label" kbd="L" onClick={() => { teachShortcut('label', 'L', 'Label'); onLabel(); }} />
      <BulkButton icon={<MailX className="size-3.5" />} label="Unsubscribe" onClick={onUnsubscribe} />
      <BulkButton icon={<X className="size-3.5" />} label="Clear" kbd="Esc" onClick={onClear} />
    </div>
  );
}

interface BulkButtonProps {
  icon: React.ReactNode;
  label: string;
  kbd?: string;
  onClick: () => void;
}

function BulkButton({
  icon,
  label,
  kbd,
  onClick,
}: BulkButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="hover:bg-accent flex items-center gap-1.5 rounded-md px-2 py-1 text-xs"
    >
      {icon}
      {label}
      {kbd && <Kbd size="sm">{kbd}</Kbd>}
    </button>
  );
}
