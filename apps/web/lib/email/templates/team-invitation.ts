import { type OutgoingMail, renderEmail } from '@/lib/email/render';

/**
 * Team invitation — the REFERENCE wording that backend/internal/service/
 * team.go's text/html templates mirror. Not wired at runtime in piece 2 (the
 * Go service sends invitations); kept here and unit-tested so the two stay
 * in step.
 */
export function teamInvitationEmail({ to, inviter, team, url }: { to: string; inviter: string; team: string; url: string }): OutgoingMail {
  return {
    to,
    ...renderEmail({
      subject: `${inviter} invited you to ${team} on Calendium`,
      heading: `Join ${team}`,
      intro: `${inviter} has invited you to join the team "${team}" on Calendium.`,
      cta: { label: 'Accept the invitation', url },
      footer: "The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.",
    }),
  };
}
