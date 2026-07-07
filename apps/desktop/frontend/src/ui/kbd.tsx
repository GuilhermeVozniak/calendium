import type * as React from 'react';

import { cn } from '@/lib/utils';

/** Keyboard-shortcut hint chip (Superhuman-style, used all over the chrome). */
function Kbd({ className, ...props }: React.ComponentProps<'kbd'>) {
  return (
    <kbd
      className={cn(
        'bg-muted text-muted-foreground pointer-events-none inline-flex h-5 min-w-5 items-center justify-center gap-0.5 rounded border px-1 font-sans text-[10px] font-medium select-none',
        className
      )}
      {...props}
    />
  );
}

export { Kbd };
