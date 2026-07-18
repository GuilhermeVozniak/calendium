import { toast } from 'sonner';

const shown = new Set<string>();

export function teachShortcut(actionId: string, keys: string, actionLabel: string): void {
  if (shown.has(actionId)) return;
  shown.add(actionId);
  toast.message(`Tip: press ${keys} to ${actionLabel}`);
}

export function resetShortcutHints(): void {
  shown.clear();
}
