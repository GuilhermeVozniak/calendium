import Link from 'next/link';
import { ArrowRight, ArrowUpRight, Server } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { GITHUB_URL, SELF_HOSTING_DOCS_HREF } from './links';

const points = [
  'Run the whole stack — Go backend, Postgres, web app — on your own hardware',
  'Every feature unlocked; no license keys, no subscription',
  'Bring your own Google, Microsoft & AI provider keys',
  'Point the desktop & mobile apps at your server',
];

/** Landing-page callout for the open-core self-hosting option. */
export function SelfHostingSection() {
  return (
    <section id="self-hosting" className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-24 md:py-28">
        <div className="overflow-hidden rounded-2xl border bg-card p-8 shadow-sm md:p-12">
          <div className="grid gap-10 lg:grid-cols-[1.35fr_1fr] lg:items-center">
            <div>
              <div className="inline-flex items-center gap-2 rounded-full border px-3 py-1 font-mono text-xs uppercase tracking-wider text-muted-foreground">
                <Server className="size-3.5" /> Open source · AGPLv3
              </div>
              <h2 className="mt-5 text-2xl font-semibold tracking-tight md:text-3xl">
                Your data, your server.
              </h2>
              <p className="mt-3 max-w-xl text-muted-foreground">
                Calendium is open source. Host the entire app yourself — on a VPS, a home
                server, or your own cloud — and keep every byte of mail and calendar data on
                infrastructure you control. Free, forever.
              </p>
              <ul className="mt-6 grid gap-2.5 text-sm">
                {points.map((point) => (
                  <li key={point} className="flex gap-2.5">
                    <ArrowRight className="mt-1 size-3.5 shrink-0 text-muted-foreground" />
                    <span>{point}</span>
                  </li>
                ))}
              </ul>
            </div>
            <div className="flex flex-col gap-3 lg:items-stretch">
              <Button asChild size="lg">
                <Link href={SELF_HOSTING_DOCS_HREF}>
                  Read the self-hosting guide
                  <ArrowRight />
                </Link>
              </Button>
              <Button asChild variant="outline" size="lg">
                <a href={GITHUB_URL} target="_blank" rel="noreferrer noopener">
                  View on GitHub
                  <ArrowUpRight />
                </a>
              </Button>
              <p className="text-center text-xs text-muted-foreground">
                Prefer we run it? <Link href="/pricing" className="underline underline-offset-4">Calendium Cloud</Link> is $50/year.
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
