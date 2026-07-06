import type { Metadata } from 'next';
import Link from 'next/link';

import { AiSection } from '@/components/marketing/ai-section';
import { CalendarSection } from '@/components/marketing/calendar-section';
import { Faq, type FaqItem } from '@/components/marketing/faq';
import { FinalCta } from '@/components/marketing/final-cta';
import { Hero } from '@/components/marketing/hero';
import { PlatformsSection } from '@/components/marketing/platforms-section';
import { PricingTeaser } from '@/components/marketing/pricing-teaser';
import { SectionHeading } from '@/components/marketing/section-heading';
import { SelfHostingSection } from '@/components/marketing/self-hosting';
import { SocialProof } from '@/components/marketing/social-proof';
import { SpeedSection } from '@/components/marketing/speed-section';
import { TriageSection } from '@/components/marketing/triage-section';

export const metadata: Metadata = {
  title: { absolute: 'Calendium — email and calendar, at the speed of thought' },
  description:
    'One keyboard-first home for Gmail, Outlook, and your calendar. Split inbox triage, AI that writes like you, and every action under 100 ms. $50/year, 14-day free trial.',
};

const faqItems: FaqItem[] = [
  {
    question: 'Does Calendium work with my email?',
    answer:
      'Google (Gmail and Workspace) and Microsoft (Outlook and Microsoft 365) accounts — mail and calendar both. Connect as many accounts as you like; they share one inbox and one week view.',
  },
  {
    question: 'Is it really under 100 ms?',
    answer:
      'Your mailboxes and calendars are continuously mirrored to Calendium, so lists, search, and navigation are served locally instead of waiting on a provider round trip. The network only shows up where it has to — sending and background sync.',
  },
  {
    question: 'What does the AI see?',
    answer:
      'AI features run per request through OpenRouter: the thread you point them at, nothing more. Your mail is never used to train models, and nothing runs unless you invoke it.',
  },
  {
    question: 'Do I have to learn the shortcuts?',
    answer:
      'No — everything works with a mouse, and ⌘K finds any command by name. The shortcuts are simply there when you are ready to go fast.',
  },
  {
    question: 'How much does it cost?',
    answer: (
      <>
        $50 a year with everything included, after a 14-day free trial. See{' '}
        <Link href="/pricing" className="font-medium text-foreground underline underline-offset-4">
          pricing
        </Link>{' '}
        for the details.
      </>
    ),
  },
];

export default function LandingPage() {
  return (
    <>
      <Hero />
      <SpeedSection />
      <TriageSection />
      <AiSection />
      <CalendarSection />
      <PlatformsSection />
      <SelfHostingSection />
      <SocialProof />
      <PricingTeaser />
      <section className="border-t">
        <div className="mx-auto grid w-full max-w-6xl gap-12 px-6 py-24 md:py-32 lg:grid-cols-[1fr_1.6fr] lg:gap-20">
          <SectionHeading
            eyebrow="FAQ"
            title="Questions, answered."
            lede="Anything else — write to us and a human replies."
          />
          <Faq items={faqItems} className="self-start" />
        </div>
      </section>
      <FinalCta />
    </>
  );
}
