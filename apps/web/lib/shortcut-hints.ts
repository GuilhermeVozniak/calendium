import { toast } from 'sonner';

import { isCoachMuted } from '@/lib/tour-state';

const shown = new Set<string>();

export function teachShortcut(actionId: string, keys: string, actionLabel: string): void {
  // Mute check comes first and does NOT consume the hint — unmuting later
  // still teaches each action once.
  if (isCoachMuted()) return;
  if (shown.has(actionId)) return;
  shown.add(actionId);
  toast.message(`Tip: press ${keys} to ${actionLabel}`);
}

export function resetShortcutHints(): void {
  shown.clear();
}
