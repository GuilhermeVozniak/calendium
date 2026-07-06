import * as React from 'react';

import { cn } from '@/lib/utils';

/** Keyboard-shortcut hint chip (Superhuman-style, used all over the chrome). */
function Kbd({ className, ...props }: React.ComponentProps<'kbd'>) {
  return (
    <kbd
      className={cn(
        'pointer-events-none inline-flex h-5 min-w-5 select-none items-center justify-center gap-0.5 rounded border bg-muted px-1 font-sans text-[10px] font-medium text-muted-foreground',
        className
      )}
      {...props}
    />
  );
}

export { Kbd };
