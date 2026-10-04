import { type EnvLike, envBool } from '@/lib/auth-env';

/**
 * SMTP settings for the web tier, read from the same SMTP_* variables as the
 * Go api/worker (config.FromEnv). `{ configured: false }` means no sender:
 * email verification is off, forgot-password shows the admin instructions.
 */
export type MailConfig =
  | { configured: false }
  | {
      configured: true;
      host: string;
      port: number;
      secure: boolean;
      user?: string;
      pass?: string;
      from: string;
      /** STARTTLS is mandatory: not implicit TLS and not a loopback host. */
      requireTLS: boolean;
    };

const FROM_RE = /^(?:[^<>]*<)?[^\s@<>]+@[^\s@<>]+>?$/;

/** localhost / 127.x / ::1 — the one case plaintext SMTP is acceptable (Mailpit, a local relay). */
export function isLoopback(host: string): boolean {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, '');
  return h === 'localhost' || h === '::1' || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(h);
}

/**
 * Parses SMTP_*. Throws on a half-set block or an invalid value so a typo
 * fails at boot with the variable named; returns `{configured:false}` when
 * none of SMTP_HOST/SMTP_FROM/SMTP_USER/SMTP_PASS is set (SMTP_PORT and
 * SMTP_SECURE alone do not count — the env templates pre-fill them).
 */
export function readMailConfig(env: EnvLike): MailConfig {
  const host = env.SMTP_HOST?.trim() ?? '';
  const from = env.SMTP_FROM?.trim() ?? '';
  const user = env.SMTP_USER ?? '';
  const pass = env.SMTP_PASS ?? '';
  if (!host && !from && !user && !pass) return { configured: false };

  const missing: string[] = [];
  if (!host) missing.push('SMTP_HOST');
  if (!from) missing.push('SMTP_FROM');
  if (!user && pass) missing.push('SMTP_USER');
  if (user && !pass) missing.push('SMTP_PASS');
  if (missing.length > 0) throw new Error(`SMTP_* is partially configured: ${missing.join(', ')}`);

  // Blank counts as unset (compose passes `SMTP_PORT=` through as ''), like Go's os.Getenv.
  const portRaw = env.SMTP_PORT?.trim() || '587';
  const port = /^\d+$/.test(portRaw) ? Number(portRaw) : Number.NaN;
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error(`SMTP_PORT must be an integer between 1 and 65535, got "${portRaw}"`);
  }
  const secure = envBool(env, 'SMTP_SECURE');
  if (!FROM_RE.test(from)) {
    throw new Error(`SMTP_FROM must be an email address or "Name <addr>", got "${from}"`);
  }
  return {
    configured: true,
    host,
    port,
    secure,
    from,
    requireTLS: !secure && !isLoopback(host),
    ...(user ? { user, pass } : {}),
  };
}

/**
 * readMailConfig plus the cloud rule: unless SELF_HOSTED is true (envBool,
 * the Go config's grammar) an SMTP sender
 * is mandatory (verification, reset and invitations depend on it). Called
 * from instrumentation.ts at server boot — never from `next build`.
 */
export function assertMailConfigForMode(env: EnvLike): MailConfig {
  const mail = readMailConfig(env);
  if (!mail.configured && !envBool(env, 'SELF_HOSTED')) {
    throw new Error(
      'SMTP_HOST and SMTP_FROM are required when SELF_HOSTED=false (cloud mode); set them or run with SELF_HOSTED=true'
    );
  }
  return mail;
}
