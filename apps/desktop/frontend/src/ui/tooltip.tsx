import * as React from 'react';

import { cn } from '@/lib/utils';

type Side = 'top' | 'bottom' | 'left' | 'right';

const sideClasses: Record<Side, string> = {
  top: 'bottom-full left-1/2 mb-1.5 -translate-x-1/2',
  bottom: 'top-full left-1/2 mt-1.5 -translate-x-1/2',
  left: 'right-full top-1/2 mr-1.5 -translate-y-1/2',
  right: 'left-full top-1/2 ml-1.5 -translate-y-1/2',
};

interface TooltipProps {
  label: React.ReactNode;
  side?: Side;
  className?: string;
  children: React.ReactNode;
}

/**
 * Compact CSS-only tooltip (no positioning library — fine for the fixed
 * chrome this app uses it in).
 */
function Tooltip({ label, side = 'top', className, children }: TooltipProps) {
  return (
    <span className="group/tooltip relative inline-flex">
      {children}
      <span
        role="tooltip"
        className={cn(
          'pointer-events-none absolute z-50 hidden whitespace-nowrap rounded-md bg-primary px-2 py-1 text-xs text-primary-foreground shadow-md group-hover/tooltip:inline-flex group-hover/tooltip:items-center group-hover/tooltip:gap-1.5',
          sideClasses[side],
          className
        )}
      >
        {label}
      </span>
    </span>
  );
}

export { Tooltip };
