// Tiny dependency-free toast store (the desktop frontend keeps its dep set
// minimal). <Toaster/> (ui/toaster.tsx) subscribes and renders; call toast(...)
// from anywhere. Used for the undo-send affordance and honest mutation-failure
// surfacing (contract items 4 & 13).

export interface ToastAction {
  label: string;
  onClick: () => void | Promise<void>;
}

export type ToastVariant = 'default' | 'destructive';

export interface ToastOptions {
  title: string;
  description?: string;
  action?: ToastAction;
  /** Auto-dismiss after this many ms; <= 0 keeps it until dismissed. Default 5000. */
  durationMs?: number;
  variant?: ToastVariant;
}

export interface ToastRecord extends ToastOptions {
  id: string;
}

let toasts: ToastRecord[] = [];
const listeners = new Set<() => void>();
const timers = new Map<string, ReturnType<typeof setTimeout>>();

function notify(): void {
  for (const listener of listeners) listener();
}

export function subscribeToasts(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Stable snapshot for useSyncExternalStore (ref changes only on mutation). */
export function getToasts(): ToastRecord[] {
  return toasts;
}

export function dismissToast(id: string): void {
  const timer = timers.get(id);
  if (timer) {
    clearTimeout(timer);
    timers.delete(id);
  }
  const next = toasts.filter((t) => t.id !== id);
  if (next.length === toasts.length) return;
  toasts = next;
  notify();
}

export function toast(opts: ToastOptions): string {
  const id = Math.random().toString(36).slice(2);
  toasts = [...toasts, { id, ...opts }];
  notify();
  const duration = opts.durationMs ?? 5000;
  if (duration > 0) {
    timers.set(
      id,
      setTimeout(() => dismissToast(id), duration)
    );
  }
  return id;
}

/** Human-readable message from an API/network error for toast descriptions. */
export function errorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  return 'Something went wrong. Please try again.';
}
