import type { EmailAddress, Message, Thread } from './types';

/**
 * Instant Intro reply, shared by web + desktop: reply-all to the latest
 * message in an intro thread, thank the introducer, and move them to BCC so
 * the two newly-connected parties can carry on the conversation without the
 * introducer being copied on every subsequent message.
 */
export interface InstantIntroDraft {
  /** Everyone on the thread except me and the introducer. */
  to: EmailAddress[];
  /** The introducer, moved to BCC. */
  bcc: EmailAddress[];
  /** "Re: …" (idempotent prefix). */
  subject: string;
  /** "Thanks <first name>! (moving you to BCC)\n\n" */
  body: string;
  threadId: string;
}

function normalizeEmail(email: string): string {
  return email.trim().toLowerCase();
}

/** Dedupes by email, case-insensitively, keeping the first occurrence. */
function dedupeAddresses(addresses: EmailAddress[]): EmailAddress[] {
  const seen = new Map<string, EmailAddress>();
  for (const addr of addresses) {
    const key = normalizeEmail(addr.email);
    if (!seen.has(key)) seen.set(key, addr);
  }
  return [...seen.values()];
}

/** Text before the first space of the display name, falling back to the email local part. */
function firstName(addr: EmailAddress): string {
  const name = addr.name?.trim();
  if (name) {
    const spaceIdx = name.indexOf(' ');
    return spaceIdx === -1 ? name : name.slice(0, spaceIdx);
  }
  return addr.email.split('@')[0] ?? addr.email;
}

/** Finds the chronologically latest message by sentAt. */
function latestMessage(messages: Message[]): Message {
  return messages.reduce((latest, candidate) =>
    new Date(candidate.sentAt).getTime() >= new Date(latest.sentAt).getTime() ? candidate : latest
  );
}

/** Prefixes "Re: " unless the subject already carries a (case-insensitive) "re:" prefix. */
function replySubject(subject: string): string {
  return /^re:/i.test(subject) ? subject : `Re: ${subject}`;
}

/**
 * Builds the Instant Intro reply from an intro thread: reply-all to the
 * latest message, thank the introducer (its sender), move them to BCC.
 * Returns null when the transform doesn't apply (no messages, or the
 * latest message is from me, or there is no third participant).
 */
export function buildInstantIntro(
  thread: Thread,
  messages: Message[],
  myEmail: string
): InstantIntroDraft | null {
  if (messages.length === 0) return null;

  const latest = latestMessage(messages);
  const me = normalizeEmail(myEmail);
  if (normalizeEmail(latest.from.email) === me) return null;

  const introducer = latest.from;
  const introducerKey = normalizeEmail(introducer.email);

  const participants = dedupeAddresses([latest.from, ...latest.to, ...latest.cc]);
  const to = participants.filter((addr) => {
    const key = normalizeEmail(addr.email);
    return key !== me && key !== introducerKey;
  });

  if (to.length === 0) return null;

  return {
    to,
    bcc: [introducer],
    subject: replySubject(latest.subject),
    body: `Thanks ${firstName(introducer)}! (moving you to BCC)\n\n`,
    threadId: thread.id,
  };
}
