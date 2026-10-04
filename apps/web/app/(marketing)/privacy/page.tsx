import type { Metadata } from 'next';
import Link from 'next/link';

import { LegalLayout, LegalList, LegalSection } from '@/components/marketing/legal';
import { SUPPORT_EMAIL } from '@/components/marketing/links';

export const metadata: Metadata = {
  title: 'Privacy Policy',
  description:
    'How Calendium handles your mail, calendar, and account data — on Calendium Cloud and when you self-host.',
};

const UPDATED = 'October 2026';

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
              <strong>Integrations.</strong> If you connect Todoist or HubSpot, we store their
              OAuth tokens the same way and mirror the tasks or contact context you use in the app.
            </>,
            <>
              <strong>Billing information.</strong> Subscriptions are sold by Paddle, our merchant
              of record. We store your subscription status and Paddle customer and subscription
              ids; your payment details are held by Paddle, not us.
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
          When new mail arrives we may summarise it, draft a reply, suggest quick replies, run your
          classifiers, detect reminders and build your writing-style profile in the background.
          Turn this off in Settings → AI → <strong>Background AI processing</strong>; on-demand
          actions (compose, reply, summarise, ask) still send only the thread you point them at.
          Requests go to OpenRouter, which routes them to model providers that vary by model. Your
          mail is never used to train models.
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
              <strong>Paddle</strong> — merchant of record for Cloud subscriptions: payment,
              invoices, receipts, taxes and refunds.
            </>,
            <>
              <strong>OpenRouter</strong> — the AI gateway for the AI features above; it routes
              requests to model providers that vary by model.
            </>,
            <>
              <strong>Todoist and HubSpot</strong> — only if you connect them, for the tasks and
              CRM context you choose to sync.
            </>,
            <>
              <strong>Open-Meteo</strong> — weather on calendar days; receives coordinates only.
            </>,
            <>
              <strong>Nominatim and OSRM</strong> — place search and routing for event locations;
              receive your query text and coordinates.
            </>,
            <>
              <strong>Apple APNs and Google FCM</strong> — deliver push notifications to the mobile
              apps; receive your device token and the notification preview. Browser notifications
              go through your browser vendor&apos;s push service in the same way.
            </>,
            <>
              <strong>Our email delivery provider</strong> — sends account emails such as address
              verification and password resets; receives your email address and the message.
            </>,
          ]}
        />
      </LegalSection>

      <LegalSection title="Retention and deletion">
        <p>
          We keep your data for as long as your account is active. You can disconnect a mailbox at
          any time, which removes its stored tokens and mirrored content. You can download a copy of
          everything from Settings → Account → <strong>Download my data</strong>, and you can delete
          your account from Settings → Account → <strong>Delete account</strong> — on the web, in
          the mobile app or on desktop. Deletion removes your personal data from Cloud immediately,
          subject to any records we must retain for legal or accounting reasons (Paddle keeps its
          own transaction records as merchant of record).
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
