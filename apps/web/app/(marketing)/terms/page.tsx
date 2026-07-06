import type { Metadata } from 'next';
import Link from 'next/link';

import { LegalLayout, LegalList, LegalSection } from '@/components/marketing/legal';
import { GITHUB_URL, SUPPORT_EMAIL } from '@/components/marketing/links';

export const metadata: Metadata = {
  title: 'Terms of Service',
  description:
    'The terms for using Calendium Cloud and the open-source Calendium software.',
};

const UPDATED = 'July 2026';

export default function TermsPage() {
  return (
    <LegalLayout
      title="Terms of Service"
      updated={UPDATED}
      intro="These terms govern your use of Calendium Cloud, our managed email and calendar service. The Calendium software itself is open source and separately licensed — see the license section below."
    >
      <LegalSection title="Acceptance">
        <p>
          By creating an account or using Calendium Cloud, you agree to these terms. If you are using
          Calendium on behalf of an organization, you represent that you have the authority to accept
          these terms for it.
        </p>
      </LegalSection>

      <LegalSection title="The service">
        <p>
          Calendium is a keyboard-first client for the Google and Microsoft mailboxes and calendars
          you connect. Cloud is the version we host and maintain for you. You may also run the same
          software yourself under its open-source license, in which case these Cloud terms do not
          apply to your instance.
        </p>
      </LegalSection>

      <LegalSection title="Your account">
        <LegalList
          items={[
            'You are responsible for keeping your credentials secure and for activity under your account.',
            'You must provide accurate information and be old enough to form a binding contract in your jurisdiction.',
            'You are responsible for your compliance with the terms of the mail and calendar providers you connect.',
          ]}
        />
      </LegalSection>

      <LegalSection title="Subscriptions and billing">
        <p>
          Calendium Cloud costs <strong>$50 per year</strong> after a 14-day free trial. Billing runs
          through Stripe; when your trial ends, your payment method is charged and the plan renews
          annually until you cancel. You can cancel anytime from Settings → Billing, and your access
          continues through the end of the period you paid for. There are no in-app purchases — the
          mobile and desktop apps unlock automatically once you subscribe on the web. Fees are
          non-refundable except where required by law.
        </p>
      </LegalSection>

      <LegalSection title="Acceptable use">
        <p>You agree not to use Calendium to:</p>
        <LegalList
          items={[
            'send spam, phishing, or other unlawful or abusive messages;',
            'infringe others’ rights or violate any applicable law;',
            'attempt to disrupt, reverse-engineer, or gain unauthorized access to the service or other users’ data.',
          ]}
        />
      </LegalSection>

      <LegalSection title="Open-source license">
        <p>
          The Calendium software is released under the <strong>AGPLv3</strong>. You are free to run,
          study, modify, and self-host it under that license — see the{' '}
          <a href={GITHUB_URL} target="_blank" rel="noreferrer noopener">
            source repository
          </a>{' '}
          for details. These Terms of Service cover only the hosted Cloud offering, not your use of
          the source code.
        </p>
      </LegalSection>

      <LegalSection title="Disclaimers and liability">
        <p>
          Calendium Cloud is provided &ldquo;as is,&rdquo; without warranties of any kind. To the
          fullest extent permitted by law, we are not liable for indirect, incidental, or
          consequential damages, and our total liability for any claim is limited to the amount you
          paid us in the twelve months before the claim.
        </p>
      </LegalSection>

      <LegalSection title="Termination">
        <p>
          You may stop using Calendium and delete your account at any time. We may suspend or
          terminate an account that violates these terms. On termination, your right to use the Cloud
          service ends; the deletion of your data is described in our{' '}
          <Link href="/privacy">Privacy Policy</Link>.
        </p>
      </LegalSection>

      <LegalSection title="Changes and contact">
        <p>
          We may update these terms; material changes will be reflected in the date above. Continued
          use after a change means you accept the revised terms. Questions? Email{' '}
          <a href={`mailto:${SUPPORT_EMAIL}`}>{SUPPORT_EMAIL}</a>.
        </p>
      </LegalSection>
    </LegalLayout>
  );
}
