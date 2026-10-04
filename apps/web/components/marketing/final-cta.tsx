import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { START_TRIAL_HREF } from './links';

export function FinalCta() {
  return (
    <section className="relative overflow-hidden border-t">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,hsl(var(--border)/0.6)_1px,transparent_1px),linear-gradient(to_bottom,hsl(var(--border)/0.6)_1px,transparent_1px)] bg-[size:64px_64px] [mask-image:radial-gradient(ellipse_70%_60%_at_50%_100%,black_25%,transparent_75%)]"
      />
      <div className="relative mx-auto flex w-full max-w-3xl flex-col items-center px-6 py-24 text-center md:py-32">
        <h2 className="text-balance text-4xl font-semibold tracking-tight sm:text-5xl">
          Fast is a feeling.
          <br />
          <span className="text-muted-foreground">Get it back.</span>
        </h2>
        <p className="mt-5 max-w-md text-pretty text-base leading-relaxed text-muted-foreground">
          Fourteen days of the fastest email and calendar you have ever used. Set up in two
          minutes.
        </p>
        <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
          <Button asChild size="lg">
            <Link href={START_TRIAL_HREF}>
              Start free trial
              <ArrowRight />
            </Link>
          </Button>
          <Button asChild size="lg" variant="outline">
            <Link href="/pricing">See pricing</Link>
          </Button>
        </div>
        <p className="mt-6 font-mono text-xs text-muted-foreground">
          $50/year after the trial · cancel anytime · checkout by Paddle
        </p>
      </div>
    </section>
  );
}
