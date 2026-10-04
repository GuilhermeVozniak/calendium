/**
 * Minimal branded email rendering: one heading, one paragraph, an optional
 * call-to-action button and a footer, as inline-styled HTML plus a plain-text
 * twin. EVERY interpolation goes through escapeHtml — user-controlled strings
 * (names, team names, addresses) must never reach the HTML raw.
 */

export interface EmailContent {
  subject: string;
  heading: string;
  intro: string;
  cta?: { label: string; url: string };
  footer: string;
}

export interface RenderedEmail {
  subject: string;
  html: string;
  text: string;
}

/** A rendered message addressed to one recipient (the transport's input). */
export interface OutgoingMail extends RenderedEmail {
  to: string;
  replyTo?: string;
}

const ESCAPES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

export function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (c) => ESCAPES[c] ?? c);
}

export function renderEmail(c: EmailContent): RenderedEmail {
  const cta = c.cta
    ? `<p style="margin:24px 0"><a href="${escapeHtml(c.cta.url)}" style="display:inline-block;padding:12px 20px;border-radius:8px;background:#111827;color:#ffffff;text-decoration:none;font-weight:600">${escapeHtml(c.cta.label)}</a></p>` +
      `<p style="font-size:13px;color:#6b7280;word-break:break-all">If the button doesn't work, open this link: ${escapeHtml(c.cta.url)}</p>`
    : '';
  const html =
    `<!doctype html><html><body style="margin:0;padding:32px 16px;background:#f9fafb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#111827">` +
    `<table role="presentation" width="100%" cellspacing="0" cellpadding="0"><tr><td align="center">` +
    `<table role="presentation" width="100%" style="max-width:520px;background:#ffffff;border-radius:12px;padding:32px" cellspacing="0" cellpadding="0"><tr><td>` +
    `<p style="margin:0 0 16px;font-size:13px;font-weight:600;letter-spacing:.04em;color:#6b7280">CALENDIUM</p>` +
    `<h1 style="margin:0 0 16px;font-size:20px;font-weight:600">${escapeHtml(c.heading)}</h1>` +
    `<p style="margin:0;font-size:15px;line-height:1.6">${escapeHtml(c.intro)}</p>` +
    cta +
    `<p style="margin:24px 0 0;font-size:13px;line-height:1.6;color:#6b7280">${escapeHtml(c.footer)}</p>` +
    `</td></tr></table></td></tr></table></body></html>`;
  const text = [c.heading, '', c.intro, ...(c.cta ? ['', `${c.cta.label}: ${c.cta.url}`] : []), '', c.footer, ''].join('\n');
  return { subject: c.subject, html, text };
}
