import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';

import { cn } from '@/lib/utils';

/**
 * Keyboard-shortcut chip, styled after Superhuman's shortcut hints: small
 * uppercase keycaps on a muted chip with a faint keycap shadow. Use inside
 * menu items, tooltips, and the command palette.
 *
 *   <Kbd>⌘</Kbd> <Kbd>K</Kbd>  ·  <KbdGroup keys={['G', 'I']} />
 */
const kbdVariants = cva(
  'pointer-events-none inline-flex shrink-0 select-none items-center justify-center rounded border border-border bg-muted font-sans font-medium uppercase tracking-wide text-muted-foreground shadow-[inset_0_-1px_0_0_hsl(var(--border))] [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-3',
  {
    variants: {
      size: {
        sm: 'h-4 min-w-4 px-1 text-[0.625rem]',
        default: 'h-5 min-w-5 px-1.5 text-[0.6875rem]',
        lg: 'h-6 min-w-6 px-2 text-xs',
      },
    },
    defaultVariants: {
      size: 'default',
    },
  }
);

function Kbd({
  className,
  size,
  ...props
}: React.ComponentProps<'kbd'> & VariantProps<typeof kbdVariants>) {
  return <kbd data-slot="kbd" className={cn(kbdVariants({ size }), className)} {...props} />;
}

/** A sequence of keys (e.g. Superhuman's "G then I"), rendered as sibling chips. */
function KbdGroup({
  className,
  keys,
  size,
  children,
  ...props
}: React.ComponentProps<'span'> & VariantProps<typeof kbdVariants> & { keys?: string[] }) {
  return (
    <span
      data-slot="kbd-group"
      className={cn('inline-flex items-center gap-1', className)}
      {...props}>
      {keys ? keys.map((key, index) => <Kbd key={`${key}-${index}`} size={size}>{key}</Kbd>) : children}
    </span>
  );
}

export { Kbd, KbdGroup, kbdVariants };
