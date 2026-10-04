import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, ArrowUpRight, Check } from 'lucide-react';

import { Faq, type FaqItem } from '@/components/marketing/faq';
import { GITHUB_URL, SELF_HOSTING_DOCS_HREF, SUPPORT_EMAIL } from '@/components/marketing/links';
import { SectionHeading } from '@/components/marketing/section-heading';
import { StartTrialCta } from '@/components/marketing/start-trial-cta';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

export const metadata: Metadata = {
  title: 'Pricing',
  description:
    'Calendium is open source. Self-host the whole stack for free, or use Calendium Cloud — managed hosting for $50 a year, with a 14-day free trial. Every feature included either way.',
};

const selfHostPoints = [
  'Every feature unlocked — no license keys',
  'Your mail & calendar data on your own servers',
  'Bring your own Google, Microsoft & AI keys',
  'Deploy with Docker Compose in minutes',
  'Community support on GitHub',
];

const cloudPoints = [
  'Managed hosting, updates & backups',
  'Every feature, every platform',
  'Providers configured for you',
  'Email support from the team',
  '14-day free trial, cancel anytime',
];

const included = [
  'Unlimited Google & Microsoft accounts',
  'Split inbox with VIP routing',
  'Snooze, Send Later & follow-up reminders',
  'Snippets with keyboard shortcuts',
  'AI compose, summarize & ask',
  'Full calendar with availability sharing',
  'Unified search across mail & events',
  'Web, desktop, iOS & Android apps',
  'Push notifications on web, iOS & Android',
];

const comparison: { label: string; selfHost: string; cloud: string }[] = [
  { label: 'Hosting', selfHost: 'Your infrastructure', cloud: 'Fully managed by us' },
  { label: 'Updates', selfHost: 'You upgrade when ready', cloud: 'Automatic' },
  { label: 'Support', selfHost: 'Community (GitHub)', cloud: 'Email support' },
  { label: 'Providers setup', selfHost: 'Bring your own keys', cloud: 'Configured for you' },
  { label: 'Price', selfHost: 'Free', cloud: '$50 / year' },
];

const billingFaq: FaqItem[] = [
  {
    question: 'What’s the difference between self-hosted and Cloud?',
    answer:
      'Same app, two ways to run it. Self-hosted is free and open source (AGPLv3): you run the Go backend, Postgres, and the web app on your own infrastructure and bring your own provider keys. Cloud is that same app, hosted and maintained by us for $50 a year. Both unlock every feature.',
  },
  {
    question: 'How does billing work?',
    answer:
      'On Cloud, checkout runs on Paddle, our merchant of record. You start with a 14-day free trial; when it ends, subscribe for $50 a year and the plan renews yearly. Paddle handles invoices, receipts, and sales tax or VAT for your country. Self-hosting has no billing at all.',
  },
  {
    question: 'Can I cancel?',
    answer:
      'Anytime, in about three clicks: Settings → Billing → Cancel subscription opens the Paddle customer portal, where you can cancel, update your card, or download invoices. Your access continues until the end of the period you paid for.',
  },
  {
    question: 'Is there a monthly plan?',
    answer:
      'No — one annual plan keeps the pricing honest and the product simple. $50 a year, everything included, no feature gates to decode. Or self-host for free.',
  },
  {
    question: 'Can I subscribe inside the iPhone or Android app?',
    answer:
      'No, and on purpose. Calendium has no in-app purchases; subscriptions are managed on the web, like Spotify. Once you subscribe, the mobile and desktop apps unlock automatically.',
  },
  {
    question: 'What payment methods do you accept?',
    answer:
      'Everything Paddle Checkout supports: major credit and debit cards, plus Apple Pay, Google Pay, and PayPal where available.',
  },
  {
    question: 'Need invoices or team billing?',
    answer: (
      <>
        Write to{' '}
        <a
          href={`mailto:${SUPPORT_EMAIL}`}
          className="font-medium text-foreground underline underline-offset-4">
          {SUPPORT_EMAIL}
        </a>{' '}
        and we will sort it out.
      </>
    ),
  },
];

export default function PricingPage() {
  return (
    <>
      <section className="mx-auto w-full max-w-6xl px-6 pt-20 md:pt-28">
        <SectionHeading
          eyebrow="Pricing"
          title="Run it yourself, or let us run it."
          lede="Calendium is open source. Self-host the whole stack for free, or use Calendium Cloud — managed hosting for $50 a year. Every feature is included either way."
        />
      </section>

      {/* Two tiers, side by side */}
      <section className="mx-auto grid w-full max-w-6xl items-stretch gap-6 px-6 pb-6 pt-14 lg:grid-cols-2 lg:gap-8">
        {/* Self-hosted */}
        <div className="flex flex-col rounded-2xl border bg-card p-8 shadow-sm">
          <div className="flex items-center justify-between gap-4">
            <h2 className="text-lg font-semibold tracking-tight">Self-hosted</h2>
            <Badge variant="outline">Open source · AGPLv3</Badge>
          </div>
          <div className="mt-6 flex items-baseline gap-2">
            <span className="text-5xl font-semibold tracking-tight">Free</span>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">Run it yourself — forever.</p>
          <ul className="mt-8 space-y-3">
            {selfHostPoints.map((point) => (
              <li key={point} className="flex gap-3 text-sm leading-relaxed">
                <Check className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                {point}
              </li>
            ))}
          </ul>
          <div className="mt-8 flex flex-col gap-2">
            <Button asChild size="lg" variant="outline" className="w-full">
              <Link href={SELF_HOSTING_DOCS_HREF}>
                Read the self-hosting guide
                <ArrowRight />
              </Link>
            </Button>
            <Button asChild variant="ghost" size="sm" className="w-full text-muted-foreground">
              <a href={GITHUB_URL} target="_blank" rel="noreferrer noopener">
                View on GitHub
                <ArrowUpRight />
              </a>
            </Button>
          </div>
        </div>

        {/* Cloud */}
        <div className="relative flex flex-col overflow-hidden rounded-2xl border bg-card p-8 shadow-sm ring-1 ring-foreground/10">
          <div
            aria-hidden
            className="pointer-events-none absolute inset-x-0 -top-24 h-48 bg-[radial-gradient(50%_100%_at_50%_0%,hsl(var(--foreground)/0.06),transparent)]"
          />
          <div className="relative flex flex-1 flex-col">
            <div className="flex items-center justify-between gap-4">
              <h2 className="text-lg font-semibold tracking-tight">Cloud</h2>
              <Badge variant="secondary">14-day free trial</Badge>
            </div>
            <div className="mt-6 flex items-baseline gap-2">
              <span className="text-5xl font-semibold tracking-tight">$50</span>
              <span className="text-muted-foreground">/ year</span>
            </div>
            <p className="mt-1 font-mono text-xs text-muted-foreground">
              ≈ $4.16 a month, billed annually
            </p>
            <ul className="mt-8 space-y-3">
              {cloudPoints.map((point) => (
                <li key={point} className="flex gap-3 text-sm leading-relaxed">
                  <Check className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  {point}
                </li>
              ))}
            </ul>
            <div className="mt-8">
              <StartTrialCta className="w-full" />
              <p className="mt-3 text-center text-xs text-muted-foreground">
                Checkout and billing by Paddle, our merchant of record. No in-app purchases, ever.
              </p>
            </div>
          </div>
        </div>
      </section>

      {/* Compact comparison */}
      <section className="mx-auto w-full max-w-6xl px-6 pb-24 pt-10">
        <div className="overflow-x-auto rounded-xl border">
          <table className="w-full min-w-[34rem] text-left text-sm">
            <thead>
              <tr className="border-b bg-muted/40">
                <th className="px-5 py-3 font-medium text-muted-foreground">Compare</th>
                <th className="px-5 py-3 font-semibold">Self-hosted</th>
                <th className="px-5 py-3 font-semibold">Cloud</th>
              </tr>
            </thead>
            <tbody>
              {comparison.map((row) => (
                <tr key={row.label} className="border-b last:border-0">
                  <td className="px-5 py-3 font-medium">{row.label}</td>
                  <td className="px-5 py-3 text-muted-foreground">{row.selfHost}</td>
                  <td className="px-5 py-3 text-muted-foreground">{row.cloud}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* Every feature, either way */}
      <section className="border-t">
        <div className="mx-auto grid w-full max-w-6xl gap-10 px-6 py-24 md:py-28 lg:grid-cols-[1fr_1.2fr] lg:gap-16">
          <SectionHeading
            eyebrow="Every feature, either way"
            title="No tiers. No add-ons."
            lede="Self-hosted or Cloud, you get the whole product — the difference is only who runs the servers."
          />
          <div>
            <ul className="divide-y border-y">
              {included.map((feature) => (
                <li key={feature} className="flex items-center gap-3 py-3 text-sm">
                  <Check className="size-4 shrink-0 text-muted-foreground" />
                  {feature}
                </li>
              ))}
            </ul>
            <p className="mt-4 font-mono text-xs text-muted-foreground">
              Cloud prices in USD before tax. Paddle shows the exact total, including any sales tax or VAT, at checkout.
            </p>
          </div>
        </div>
      </section>

      <section className="border-t">
        <div className="mx-auto grid w-full max-w-6xl gap-12 px-6 py-24 md:py-28 lg:grid-cols-[1fr_1.6fr] lg:gap-20">
          <SectionHeading
            eyebrow="Billing"
            title="Billing, plainly."
            lede="Paddle handles every Cloud charge as merchant of record; you stay in control from the customer portal. Self-hosting has no billing at all."
          />
          <Faq items={billingFaq} className="self-start" />
        </div>
      </section>
    </>
  );
}
