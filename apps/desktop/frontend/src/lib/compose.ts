import type { Message, Thread } from '@calendium/shared';

/**
 * How the composer opens: a blank new message, or a reply seeded from a thread
 * and the message being replied to.
 */
export type ComposeIntent =
  | { kind: 'new' }
  | {
      kind: 'reply';
      thread: Thread;
      message: Message;
      /** Prefills the body (e.g. an AI instant-reply suggestion) instead of leaving it blank. */
      body?: string;
      /** Every message in the thread; enables the Instant Intro action (M2.5). */
      messages?: Message[];
    };

const COMPOSE_EVENT = 'calendium:compose';

/** Open the composer from anywhere (command palette, ThreadPane reply, etc.). */
export function openCompose(intent: ComposeIntent = { kind: 'new' }): void {
  window.dispatchEvent(new CustomEvent<ComposeIntent>(COMPOSE_EVENT, { detail: intent }));
}

/** Subscribe to compose-open requests; returns an unsubscribe function. */
export function onOpenCompose(handler: (intent: ComposeIntent) => void): () => void {
  const fn = (e: Event) => handler((e as CustomEvent<ComposeIntent>).detail);
  window.addEventListener(COMPOSE_EVENT, fn);
  return () => window.removeEventListener(COMPOSE_EVENT, fn);
}

// ---------------------------------------------------------------------------
// Plain-text <-> minimal-HTML conversion, shared by ComposeView's body field
// and SettingsView's signature editor (both are plain textareas backed by an
// HTML-bearing API field).
// ---------------------------------------------------------------------------

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/** Plain-text body -> minimal HTML the backend/providers accept. */
export function toHtml(text: string): string {
  return `<p>${escapeHtml(text).replace(/\n/g, '<br>')}</p>`;
}

/** Reverses toHtml's minimal `<p>…<br>…</p>` shape, to edit a stored HTML value (e.g. a signature) as plain text again. */
export function htmlToText(html: string): string {
  if (!html.trim()) return '';
  return html
    .replace(/^<p>/, '')
    .replace(/<\/p>$/, '')
    .replace(/<br\s*\/?>/g, '\n')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&');
}

// ---------------------------------------------------------------------------
// Signature application (M2.5) — mirrors the web app's signature helper: an
// account's signatureHtml is appended below an RFC-3676 "-- " delimiter so
// switching the From account (or reapplying) *swaps* the signature block
// rather than duplicating it, while leaving anything the user already typed
// above the delimiter untouched.
// ---------------------------------------------------------------------------

const SIGNATURE_DELIMITER = '\n\n-- \n';

/** Strips a previously-applied signature block (and its delimiter) from a compose body. */
export function stripSignature(body: string): string {
  const idx = body.indexOf(SIGNATURE_DELIMITER);
  return idx === -1 ? body : body.slice(0, idx);
}

/**
 * Applies (or swaps) an account's signature onto a compose body: any existing
 * signature block is replaced, so switching the From account mid-compose
 * updates the signature without duplicating it or disturbing what's above it.
 * An empty/blank signatureHtml clears the signature entirely.
 */
export function applySignature(body: string, signatureHtml: string): string {
  const withoutSignature = stripSignature(body);
  const signatureText = htmlToText(signatureHtml).trim();
  if (!signatureText) return withoutSignature;
  return `${withoutSignature}${SIGNATURE_DELIMITER}${signatureText}`;
}
