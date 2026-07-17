'use client';

import type { Label } from '@calendium/shared';
import { Check, Tag } from 'lucide-react';

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';

export interface LabelPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  labels: Label[];
  /** Label ids already on the (single) target thread; enables toggle display. */
  activeLabelIds: ReadonlySet<string>;
  /** Called with the chosen label and whether to add or remove it. */
  onPick: (label: Label, add: boolean) => void;
}

/** `L` — keyboard-first label picker for the selected thread or bulk range. */
export function LabelPicker({ open, onOpenChange, labels, activeLabelIds, onPick }: LabelPickerProps) {
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder="Label as…" />
      <CommandList>
        <CommandEmpty>No labels found.</CommandEmpty>
        <CommandGroup heading="Labels">
          {labels.map((label) => {
            const active = activeLabelIds.has(label.id);
            return (
              <CommandItem
                key={label.id}
                value={label.name}
                onSelect={() => {
                  onPick(label, !active);
                  onOpenChange(false);
                }}
              >
                <Tag className="size-4" />
                <span>{label.name}</span>
                {active && <Check className="ml-auto size-4" aria-label="Applied" />}
              </CommandItem>
            );
          })}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
