import type { ReactNode } from 'react';

import { KbdGroup } from '@/components/ui/kbd';
import { cn } from '@/lib/utils';

interface SectionHeadingProps {
  eyebrow: string;
  /** Optional keyboard sequence shown beside the eyebrow — the real in-app binding. */
  keys?: string[];
  title: ReactNode;
  lede?: ReactNode;
  align?: 'left' | 'center';
  className?: string;
}

export function SectionHeading({
  eyebrow,
  keys,
  title,
  lede,
  align = 'left',
  className,
}: SectionHeadingProps) {
  return (
    <div className={cn('max-w-2xl', align === 'center' && 'mx-auto text-center', className)}>
      <div className={cn('flex items-center gap-3', align === 'center' && 'justify-center')}>
        <span className="font-mono text-xs uppercase tracking-[0.2em] text-muted-foreground">
          {eyebrow}
        </span>
        {keys ? <KbdGroup keys={keys} size="sm" /> : null}
      </div>
      <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
        {title}
      </h2>
      {lede ? (
        <p className="mt-4 text-pretty text-base leading-relaxed text-muted-foreground">{lede}</p>
      ) : null}
    </div>
  );
}
