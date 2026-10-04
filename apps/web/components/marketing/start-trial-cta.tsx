'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { SELF_HOSTING_DOCS_HREF, START_TRIAL_HREF } from '@/components/marketing/links';
import { Button } from '@/components/ui/button';
import { useInstance } from '@/lib/use-instance';

/** Pricing CTA: the trial funnel exists only when the server bills (cloud). */
export function StartTrialCta({ className }: { className?: string }) {
  const { data: instance, isPending } = useInstance();
  if (isPending) {
    return (
      <Button size="lg" className={className} disabled>
        Start free trial
      </Button>
    );
  }
  if (!instance?.features.billing) {
    return (
      <Button asChild size="lg" variant="outline" className={className}>
        <Link href={SELF_HOSTING_DOCS_HREF}>Self-host for free</Link>
      </Button>
    );
  }
  return (
    <Button asChild size="lg" className={className}>
      <Link href={START_TRIAL_HREF}>
        Start free trial
        <ArrowRight />
      </Link>
    </Button>
  );
}
