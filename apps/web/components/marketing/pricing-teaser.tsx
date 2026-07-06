import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { START_TRIAL_HREF } from './links';

export function PricingTeaser() {
  return (
    <section className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-24 md:py-32">
        <div className="relative overflow-hidden rounded-2xl border bg-card p-8 shadow-sm sm:p-12">
          <div
            aria-hidden
            className="pointer-events-none absolute inset-x-0 -top-32 h-64 bg-[radial-gradient(50%_100%_at_50%_0%,hsl(var(--foreground)/0.06),transparent)]"
          />
          <div className="relative flex flex-col justify-between gap-10 md:flex-row md:items-end">
            <div className="max-w-md">
              <p className="font-mono text-xs uppercase tracking-[0.2em] text-muted-foreground">
                Pricing
              </p>
              <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
                Everything included. One price.
              </h2>
              <p className="mt-4 text-sm leading-relaxed text-muted-foreground">
                No tiers, no add-ons, no per-seat math. Fourteen days free, then one annual plan
                with every feature on every platform.
              </p>
            </div>
            <div className="shrink-0">
              <div className="flex items-baseline gap-2">
                <span className="text-6xl font-semibold tracking-tight">$50</span>
                <span className="text-muted-foreground">/ year</span>
              </div>
              <p className="mt-1 font-mono text-xs text-muted-foreground">≈ $4.16 a month</p>
              <div className="mt-6 flex flex-wrap items-center gap-3">
                <Button asChild>
                  <Link href={START_TRIAL_HREF}>
                    Start free trial
                    <ArrowRight />
                  </Link>
                </Button>
                <Button asChild variant="ghost" className="text-muted-foreground">
                  <Link href="/pricing">Pricing details</Link>
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
