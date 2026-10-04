import { CalendarRange } from 'lucide-react';
import type * as React from 'react';

/** Centered card used by the public auth pages (forgot/reset/verify). */
export function AuthShell({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-6">
      <div
        aria-hidden
        className="absolute inset-0 -z-10 [background-image:radial-gradient(hsl(var(--border))_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_45%,black,transparent)] [background-size:24px_24px]"
      />
      <div className="flex w-full max-w-sm flex-col items-center">
        <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm">
          <CalendarRange className="size-6" />
        </div>
        <h1 className="mt-6 text-2xl font-semibold tracking-tight">{title}</h1>
        {description && (
          <p className="text-muted-foreground mt-2 text-center text-sm text-balance">{description}</p>
        )}
        <div className="mt-8 flex w-full flex-col gap-3">{children}</div>
      </div>
    </main>
  );
}
