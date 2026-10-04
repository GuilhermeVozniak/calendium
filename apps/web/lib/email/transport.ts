import nodemailer, { type Transporter } from 'nodemailer';

import { type MailConfig, readMailConfig } from '@/lib/email/config';
import type { OutgoingMail } from '@/lib/email/render';

/**
 * Lazy nodemailer transport over SMTP_*. Nothing here runs at import or at
 * `next build`: the config is read on first use and the transport is built
 * on the first send, so a self-host without SMTP never touches nodemailer.
 */
let transport: Transporter | undefined;
let config: MailConfig | undefined;

function mailConfig(): MailConfig {
  config ??= readMailConfig(process.env);
  return config;
}

export function isMailConfigured(): boolean {
  return mailConfig().configured;
}

function transporter(): Transporter {
  const cfg = mailConfig();
  if (!cfg.configured) throw new Error('email is not configured (SMTP_HOST unset)');
  transport ??= nodemailer.createTransport({
    host: cfg.host,
    port: cfg.port,
    secure: cfg.secure,
    requireTLS: cfg.requireTLS,
    ...(cfg.user ? { auth: { user: cfg.user, pass: cfg.pass } } : {}),
  });
  return transport;
}

/**
 * Sends one message from SMTP_FROM. On failure logs `email.send_failed` with
 * the recipient DOMAIN and the provider error (never the address, subject or
 * body) and rethrows so Better Auth answers 500 and the page can say
 * "We couldn't send the email. Try again in a minute."
 */
export async function sendMail(mail: OutgoingMail): Promise<void> {
  const cfg = mailConfig();
  if (!cfg.configured) throw new Error('email is not configured (SMTP_HOST unset)');
  try {
    await transporter().sendMail({
      from: cfg.from,
      to: mail.to,
      subject: mail.subject,
      html: mail.html,
      text: mail.text,
      ...(mail.replyTo ? { replyTo: mail.replyTo } : {}),
    });
  } catch (err) {
    console.error('email.send_failed', {
      domain: mail.to.split('@')[1] ?? '',
      error: err instanceof Error ? err.message : String(err),
    });
    throw err;
  }
}

/** Test seam: forget the cached config and transport. */
export function _resetTransportForTests(): void {
  transport = undefined;
  config = undefined;
}
