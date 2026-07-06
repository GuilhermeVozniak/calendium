import Link from 'next/link';
import { Command } from 'lucide-react';

import { cn } from '@/lib/utils';

/** Calendium wordmark — a ⌘ glyph for a keyboard-first product. */
export function Wordmark({ className }: { className?: string }) {
  return (
    <Link
      href="/"
      className={cn(
        'flex items-center gap-2.5 rounded-md outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
        className
      )}>
      <span
        aria-hidden
        className="grid size-6 shrink-0 place-items-center rounded-md bg-foreground text-background">
        <Command className="size-3.5" strokeWidth={2.25} />
      </span>
      <span className="text-[15px] font-semibold tracking-tight">Calendium</span>
    </Link>
  );
}
