import type { ReactNode } from 'react';
import { Plus } from 'lucide-react';

import { cn } from '@/lib/utils';

export interface FaqItem {
  question: string;
  answer: ReactNode;
}

/** Dependency-free accordion built on <details>/<summary>. */
export function Faq({ items, className }: { items: FaqItem[]; className?: string }) {
  return (
    <div className={cn('divide-y border-y', className)}>
      {items.map((item) => (
        <details key={item.question} className="group">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-4 py-5 text-left text-sm font-medium [&::-webkit-details-marker]:hidden">
            {item.question}
            <Plus
              aria-hidden
              className="size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-open:rotate-45"
            />
          </summary>
          <div className="max-w-prose pb-5 text-sm leading-relaxed text-muted-foreground">
            {item.answer}
          </div>
        </details>
      ))}
    </div>
  );
}
