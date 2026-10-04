import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/** Sign-up / sign-in verification (Better Auth `sendVerificationEmail`; link valid 24 h). */
export function verifyEmail({ to, url }: { to: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: 'Verify your email for Calendium',
      heading: 'Verify your email',
      intro: 'Confirm this address to finish setting up your Calendium account.',
      cta: { label: 'Verify email', url },
      footer: "This link expires in 24 hours. If you didn't create a Calendium account, you can safely ignore this email.",
    }),
  };
}
