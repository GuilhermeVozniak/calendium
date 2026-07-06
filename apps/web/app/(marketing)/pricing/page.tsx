import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, Check } from 'lucide-react';

import { Faq, type FaqItem } from '@/components/marketing/faq';
import { START_TRIAL_HREF, SUPPORT_EMAIL } from '@/components/marketing/links';
import { SectionHeading } from '@/components/marketing/section-heading';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

export const metadata: Metadata = {
  title: 'Pricing',
  description:
    'Calendium is $50 a year — about $4.16 a month. Every feature, every platform, one subscription, starting with a 14-day free trial.',
};

const included = [
  'Unlimited Google & Microsoft accounts',
  'Split inbox with VIP routing',
  'Snooze, Send Later & follow-up reminders',
  'Snippets with keyboard shortcuts',
  'AI compose, summarize & ask',
  'Full calendar with availability sharing',
  'Unified search across mail & events',
  'Web, desktop, iOS & Android apps',
  'Push notifications on every platform',
];

const terms = [
  'Try everything free for 14 days — billing starts only when the trial ends',
  'Cancel anytime; you keep access through the period you paid for',
  'One subscription unlocks web, desktop, iOS & Android',
];

const billingFaq: FaqItem[] = [
  {
    question: 'How does billing work?',
    answer:
      'Checkout runs on Stripe. You start with a 14-day free trial; when it ends, your card is charged $50 and the plan renews yearly. Receipts come straight from Stripe.',
  },
  {
    question: 'Can I cancel?',
    answer:
      'Anytime, in about three clicks: Settings → Billing → Manage subscription opens the Stripe billing portal, where you can cancel, update your card, or download invoices. Your access continues to the end of the period you paid for.',
  },
  {
    question: 'Is there a monthly plan?',
    answer:
      'No — one annual plan keeps the pricing honest and the product simple. $50 a year, everything included, no feature gates to decode.',
  },
  {
    question: 'Can I subscribe inside the iPhone or Android app?',
    answer:
      'No, and on purpose. Calendium has no in-app purchases; subscriptions are managed on the web, like Spotify. Once you subscribe, the mobile and desktop apps unlock automatically.',
  },
  {
    question: 'What payment methods do you accept?',
    answer:
      'Everything Stripe Checkout supports: major credit and debit cards, plus Apple Pay, Google Pay, and Link where available.',
  },
  {
    question: 'What happens when my trial ends?',
    answer:
      'If you subscribed, nothing changes. If not, Calendium pauses until you do — your connected accounts and data stay intact, and nothing is deleted.',
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
          title="One plan. Everything included."
          lede="$50 a year — about $4.16 a month. Every feature, every platform, one subscription. No tiers, no add-ons, no per-seat math."
        />
      </section>

      <section className="mx-auto grid w-full max-w-6xl items-start gap-10 px-6 pb-24 pt-14 lg:grid-cols-[1.05fr_1fr] lg:gap-16">
        {/* Plan card */}
        <div className="relative overflow-hidden rounded-2xl border bg-card p-8 shadow-sm">
          <div
            aria-hidden
            className="pointer-events-none absolute inset-x-0 -top-24 h-48 bg-[radial-gradient(50%_100%_at_50%_0%,hsl(var(--foreground)/0.06),transparent)]"
          />
          <div className="relative">
            <div className="flex items-center justify-between gap-4">
              <h2 className="text-lg font-semibold tracking-tight">Calendium Annual</h2>
              <Badge variant="secondary">14-day free trial</Badge>
            </div>
            <div className="mt-6 flex items-baseline gap-2">
              <span className="text-6xl font-semibold tracking-tight">$50</span>
              <span className="text-muted-foreground">/ year</span>
            </div>
            <p className="mt-1 font-mono text-xs text-muted-foreground">
              ≈ $4.16 a month, billed annually
            </p>
            <ul className="mt-8 space-y-3">
              {terms.map((term) => (
                <li key={term} className="flex gap-3 text-sm leading-relaxed">
                  <Check className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  {term}
                </li>
              ))}
            </ul>
            <Button asChild size="lg" className="mt-8 w-full">
              <Link href={START_TRIAL_HREF}>
                Start free trial
                <ArrowRight />
              </Link>
            </Button>
            <p className="mt-3 text-center text-xs text-muted-foreground">
              Checkout and billing by Stripe. No in-app purchases, ever.
            </p>
          </div>
        </div>

        {/* Included checklist */}
        <div className="lg:pt-2">
          <p className="font-mono text-xs uppercase tracking-[0.2em] text-muted-foreground">
            Everything included
          </p>
          <ul className="mt-6 divide-y border-y">
            {included.map((feature) => (
              <li key={feature} className="flex items-center gap-3 py-3 text-sm">
                <Check className="size-4 shrink-0 text-muted-foreground" />
                {feature}
              </li>
            ))}
          </ul>
          <p className="mt-4 font-mono text-xs text-muted-foreground">
            Prices in USD. Taxes may apply depending on your location.
          </p>
        </div>
      </section>

      <section className="border-t">
        <div className="mx-auto grid w-full max-w-6xl gap-12 px-6 py-24 md:py-28 lg:grid-cols-[1fr_1.6fr] lg:gap-20">
          <SectionHeading
            eyebrow="Billing"
            title="Billing, plainly."
            lede="Stripe handles every charge; you stay in control from the billing portal."
          />
          <Faq items={billingFaq} className="self-start" />
        </div>
      </section>
    </>
  );
}
