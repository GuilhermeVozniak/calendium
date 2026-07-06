import * as React from 'react';

import { cn } from '@/lib/utils';

interface DropdownContextValue {
  open: boolean;
  setOpen: (open: boolean) => void;
}

const DropdownContext = React.createContext<DropdownContextValue | null>(null);

function useDropdown() {
  const ctx = React.useContext(DropdownContext);
  if (!ctx) throw new Error('Dropdown components must be used inside <DropdownMenu>');
  return ctx;
}

/** Compact hand-rolled dropdown menu (outside-click + Escape close). */
function DropdownMenu({ children, className }: { children: React.ReactNode; className?: string }) {
  const [open, setOpen] = React.useState(false);
  const ref = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('pointerdown', onPointerDown);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('pointerdown', onPointerDown);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  return (
    <DropdownContext.Provider value={{ open, setOpen }}>
      <div ref={ref} className={cn('relative inline-flex', className)}>
        {children}
      </div>
    </DropdownContext.Provider>
  );
}

/** Clones its single child element, wiring click-to-toggle + aria state. */
function DropdownMenuTrigger({ children }: { children: React.ReactElement }) {
  const { open, setOpen } = useDropdown();
  const child = children as React.ReactElement<{
    onClick?: React.MouseEventHandler;
    'aria-expanded'?: boolean;
    'aria-haspopup'?: React.AriaAttributes['aria-haspopup'];
  }>;
  return React.cloneElement(child, {
    'aria-haspopup': 'menu',
    'aria-expanded': open,
    onClick: (e: React.MouseEvent) => {
      child.props.onClick?.(e);
      setOpen(!open);
    },
  });
}

function DropdownMenuContent({
  className,
  align = 'start',
  ...props
}: React.ComponentProps<'div'> & { align?: 'start' | 'end' }) {
  const { open } = useDropdown();
  if (!open) return null;
  return (
    <div
      role="menu"
      className={cn(
        'absolute top-full z-50 mt-1 min-w-44 overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md',
        align === 'end' ? 'right-0' : 'left-0',
        className
      )}
      {...props}
    />
  );
}

function DropdownMenuItem({
  className,
  onSelect,
  ...props
}: Omit<React.ComponentProps<'button'>, 'onSelect'> & { onSelect?: () => void }) {
  const { setOpen } = useDropdown();
  return (
    <button
      type="button"
      role="menuitem"
      className={cn(
        'flex w-full select-none items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm outline-none hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted-foreground',
        className
      )}
      onClick={() => {
        onSelect?.();
        setOpen(false);
      }}
      {...props}
    />
  );
}

function DropdownMenuLabel({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div className={cn('px-2 py-1.5 text-xs font-medium text-muted-foreground', className)} {...props} />
  );
}

function DropdownMenuSeparator({ className, ...props }: React.ComponentProps<'div'>) {
  return <div className={cn('-mx-1 my-1 h-px bg-border', className)} {...props} />;
}

export {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
};
