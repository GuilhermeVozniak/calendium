import type { Metadata } from 'next';
import Link from 'next/link';

import { LegalLayout, LegalList, LegalSection } from '@/components/marketing/legal';
import { SUPPORT_EMAIL } from '@/components/marketing/links';

export const metadata: Metadata = {
  title: 'Privacy Policy',
  description:
    'How Calendium handles your mail, calendar, and account data — on Calendium Cloud and when you self-host.',
};

const UPDATED = 'July 2026';

export default function PrivacyPage() {
  return (
    <LegalLayout
      title="Privacy Policy"
      updated={UPDATED}
      intro="Calendium is an email and calendar client. This policy explains what we collect, why, and the choices you have. We wrote it to be read — plain language, no dark patterns."
    >
      <LegalSection title="Cloud vs. self-hosted">
        <p>
          Calendium comes in two forms, and this policy applies differently to each. On{' '}
          <strong>Calendium Cloud</strong> (the managed service at our domain) we are the data
          controller for the data described below. When you <strong>self-host</strong> Calendium —
          the open-source stack running on your own servers — <strong>you</strong> are the
          controller of all data, and we receive nothing. This document describes the Cloud
          service; a self-host operator should publish their own policy for their users.
        </p>
      </LegalSection>

      <LegalSection title="Information we collect">
        <LegalList
          items={[
            <>
              <strong>Account information.</strong> Your name, email address, and authentication
              credentials, managed by Better Auth. Passwords are stored only as salted hashes.
            </>,
            <>
              <strong>Connected mailboxes.</strong> When you connect a Google or Microsoft account,
              we store OAuth refresh tokens (encrypted at rest with AES-256-GCM) and mirror your
              mail and calendar so the app can serve them quickly.
            </>,
            <>
              <strong>Billing information.</strong> Subscriptions run through Stripe. We store your
              subscription status and customer ID; your card details are held by Stripe, not us.
            </>,
            <>
              <strong>Device tokens.</strong> If you enable notifications, we store the push token
              for your device so we can deliver alerts.
            </>,
          ]}
        />
      </LegalSection>

      <LegalSection title="How we use it">
        <p>
          We use your information solely to operate the product: to sync and display your mail and
          calendar, send messages you compose, deliver notifications you asked for, and process your
          subscription. We do <strong>not</strong> sell your data, serve ads, or use the contents of
          your mailbox to train machine-learning models.
        </p>
      </LegalSection>

      <LegalSection title="AI features">
        <p>
          AI actions (compose, reply, summarize, ask) run only when you invoke them. At that moment
          the specific thread you point them at is sent to our model provider, OpenRouter, to
          generate a response — nothing more, and nothing runs in the background. Your mail is never
          used to train models.
        </p>
      </LegalSection>

      <LegalSection title="Third parties we rely on">
        <LegalList
          items={[
            <>
              <strong>Google &amp; Microsoft</strong> — to access the mailboxes and calendars you
              connect, under the scopes you approve.
            </>,
            <>
              <strong>Stripe</strong> — payment processing and billing for Cloud subscriptions.
            </>,
            <>
              <strong>OpenRouter</strong> — the AI model provider for the AI features above.
            </>,
          ]}
        />
      </LegalSection>

      <LegalSection title="Retention and deletion">
        <p>
          We keep your data for as long as your account is active. You can disconnect a mailbox at
          any time, which removes its stored tokens and mirrored content, and you can delete your
          account entirely — doing so erases your personal data from Cloud, subject to any records we
          must retain for legal or accounting reasons.
        </p>
      </LegalSection>

      <LegalSection title="Security">
        <p>
          Provider refresh tokens are encrypted at rest, all traffic is served over TLS, and the Go
          API authenticates every request with short-lived signed tokens. No system is perfectly
          secure, but we treat your mailbox with the seriousness it deserves.
        </p>
      </LegalSection>

      <LegalSection title="Your rights">
        <p>
          Depending on where you live, you may have the right to access, correct, export, or delete
          your personal data. Reach us and we will help you exercise those rights.
        </p>
      </LegalSection>

      <LegalSection title="Changes and contact">
        <p>
          If we make material changes to this policy we will update the date above and, for
          significant changes, notify you. Questions? Email{' '}
          <a href={`mailto:${SUPPORT_EMAIL}`}>{SUPPORT_EMAIL}</a>. See also our{' '}
          <Link href="/terms">Terms of Service</Link>.
        </p>
      </LegalSection>
    </LegalLayout>
  );
}
