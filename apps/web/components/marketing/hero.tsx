import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { APP_HREF, START_TRIAL_HREF } from './links';
import { ProductMock } from './product-mock';

export function Hero() {
  return (
    <section className="relative overflow-hidden">
      {/* Subtle grid, fading toward the fold */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,hsl(var(--border)/0.6)_1px,transparent_1px),linear-gradient(to_bottom,hsl(var(--border)/0.6)_1px,transparent_1px)] bg-[size:64px_64px] [mask-image:radial-gradient(ellipse_75%_55%_at_50%_0%,black_25%,transparent_75%)]"
      />

      <div className="relative mx-auto w-full max-w-6xl px-6 pt-20 md:pt-28">
        <div className="max-w-3xl motion-reduce:animate-none animate-in fade-in-0 slide-in-from-bottom-2 duration-700">
          <p className="font-mono text-xs uppercase tracking-[0.25em] text-muted-foreground">
            Email + calendar · keyboard-first
          </p>
          <h1 className="mt-6 text-balance text-5xl font-semibold leading-[1.05] tracking-tight sm:text-6xl md:text-7xl">
            Email and calendar,
            <br />
            <span className="text-muted-foreground">at the speed of thought.</span>
          </h1>
          <p className="mt-6 max-w-xl text-pretty text-base leading-relaxed text-muted-foreground sm:text-lg">
            One keyboard-first home for Gmail, Outlook, and your calendar. Local-first sync, a
            split inbox that triages itself, and AI that writes like you — every action under
            100 ms.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button asChild size="lg">
              <Link href={START_TRIAL_HREF}>
                Start free trial
                <ArrowRight />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <Link href={APP_HREF}>Open the app</Link>
            </Button>
          </div>
          <p className="mt-5 font-mono text-xs text-muted-foreground">
            14 days free · $50/year after · cancel anytime
          </p>
        </div>

        <div className="pb-24 pt-16 md:pb-32">
          <ProductMock />
        </div>
      </div>
    </section>
  );
}
