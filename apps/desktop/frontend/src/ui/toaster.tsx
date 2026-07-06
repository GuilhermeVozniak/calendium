import { X } from 'lucide-react';
import { useSyncExternalStore } from 'react';

import { dismissToast, getToasts, subscribeToasts, type ToastRecord } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Button } from '@/ui/button';

/** Bottom-right toast stack. Mount once at the app root. */
export function Toaster() {
  const toasts = useSyncExternalStore(subscribeToasts, getToasts, getToasts);
  if (toasts.length === 0) return null;
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-80 flex-col gap-2">
      {toasts.map((item) => (
        <ToastCard key={item.id} item={item} />
      ))}
    </div>
  );
}

function ToastCard({ item }: { item: ToastRecord }) {
  return (
    <div
      className={cn(
        'pointer-events-auto flex items-start gap-3 rounded-lg border bg-popover p-3 text-popover-foreground shadow-lg',
        item.variant === 'destructive' && 'border-destructive/50'
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{item.title}</div>
        {item.description && (
          <div className="mt-0.5 text-xs text-muted-foreground">{item.description}</div>
        )}
      </div>
      {item.action && (
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            void item.action!.onClick();
            dismissToast(item.id);
          }}
        >
          {item.action.label}
        </Button>
      )}
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => dismissToast(item.id)}
        className="shrink-0 rounded-sm text-muted-foreground opacity-70 transition-opacity hover:opacity-100"
      >
        <X className="size-4" />
      </button>
    </div>
  );
}
