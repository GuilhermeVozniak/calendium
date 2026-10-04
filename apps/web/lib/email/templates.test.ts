import { describe, expect, it } from 'vitest';

import { escapeHtml, renderEmail } from '@/lib/email/render';
import { resetPasswordEmail } from '@/lib/email/templates/reset-password';
import { teamInvitationEmail } from '@/lib/email/templates/team-invitation';
import { verifyEmail } from '@/lib/email/templates/verify-email';

describe('escapeHtml', () => {
  it('escapes the five HTML metacharacters and nothing else', () => {
    expect(escapeHtml(`<a href="x">Tom & Jerry's</a>`)).toBe('&lt;a href=&quot;x&quot;&gt;Tom &amp; Jerry&#39;s&lt;/a&gt;');
    expect(escapeHtml('café — ok')).toBe('café — ok');
  });
});

describe('renderEmail', () => {
  it('escapes every interpolation in html and keeps text raw', () => {
    const out = renderEmail({
      subject: 'S',
      heading: 'Hello <b>',
      intro: 'Intro & more',
      cta: { label: 'Go "now"', url: 'https://x.test/a?b=1&c=2' },
      footer: "Footer 'q'",
    });
    expect(out.subject).toBe('S');
    expect(out.html).toContain('Hello &lt;b&gt;');
    expect(out.html).toContain('Intro &amp; more');
    expect(out.html).toContain('href="https://x.test/a?b=1&amp;c=2"');
    expect(out.html).toContain('Go &quot;now&quot;');
    expect(out.html).toContain('Footer &#39;q&#39;');
    expect(out.html).not.toContain('<b>');
    expect(out.text).toBe("Hello <b>\n\nIntro & more\n\nGo \"now\": https://x.test/a?b=1&c=2\n\nFooter 'q'\n");
  });

  it('omits the CTA block when absent', () => {
    const out = renderEmail({ subject: 'S', heading: 'H', intro: 'I', footer: 'F' });
    expect(out.html).not.toContain('<a ');
    expect(out.text).toBe('H\n\nI\n\nF\n');
  });
});

const URL = 'https://mail.example.com/api/auth/verify-email?token=abc&callbackURL=%2Fverify-email';

describe('templates', () => {
  it('verifyEmail', () => {
    const m = verifyEmail({ to: 'ada@example.test', url: URL });
    expect(m.to).toBe('ada@example.test');
    expect(m.subject).toBe('Verify your email for Calendium');
    expect(m.html).toContain(`href="${URL.replace('&', '&amp;')}"`);
    expect(m.text).toContain(`Verify email: ${URL}`);
    expect(m.text).toContain('24 hours');
  });

  it('resetPasswordEmail', () => {
    const m = resetPasswordEmail({ to: 'ada@example.test', url: 'https://mail.example.com/api/auth/reset-password/tok?callbackURL=%2Freset-password' });
    expect(m.subject).toBe('Reset your Calendium password');
    expect(m.text).toContain('Reset password: https://mail.example.com/api/auth/reset-password/tok?callbackURL=%2Freset-password');
    expect(m.text).toContain('1 hour');
    expect(m.text).toContain('your password stays the same');
  });

  it('teamInvitationEmail mirrors the Go wording and escapes the inviter and team', () => {
    const m = teamInvitationEmail({ to: 'new@example.test', inviter: 'Olive <script>', team: 'Ops & "Co"', url: 'https://app.example.com/invite/raw' });
    expect(m.subject).toBe('Olive <script> invited you to Ops & "Co" on Calendium');
    expect(m.html).toContain('Olive &lt;script&gt; has invited you to join the team &quot;Ops &amp; &quot;Co&quot;&quot; on Calendium.');
    expect(m.html).not.toContain('<script>');
    expect(m.text).toContain('Olive <script> has invited you to join the team "Ops & "Co"" on Calendium.');
    expect(m.text).toContain('Accept the invitation: https://app.example.com/invite/raw');
    expect(m.text).toContain("The link expires in 14 days. If you weren't expecting this, you can safely ignore this email.");
  });
});
