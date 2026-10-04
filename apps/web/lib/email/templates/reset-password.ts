import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/** Password reset (Better Auth `sendResetPassword`; link valid 1 h, single use). */
export function resetPasswordEmail({ to, url }: { to: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: 'Reset your Calendium password',
      heading: 'Reset your password',
      intro: 'We received a request to reset the password for this address.',
      cta: { label: 'Reset password', url },
      footer: "This link expires in 1 hour and can be used once. If you didn't request a reset, you can safely ignore this email — your password stays the same.",
    }),
  };
}
