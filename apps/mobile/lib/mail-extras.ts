// Small M2.5 compose/contact helpers shared by the composer, thread reader,
// and settings screens: signature HTML<->plain-text conversion, the fixed
// reaction emoji set, and Smart Send suggestion copy.
import type { SendSuggestion } from '@calendium/shared';
import { formatTime } from '@/lib/format';

/**
 * Best-effort `htmlToText`-equivalent strip for signatures: mobile has no
 * rich-text editor, so a stored (possibly rich) `signatureHtml` is rendered
 * down to plain text before being appended to a plain-text compose body.
 * Not a full HTML parser — good enough for the simple markup this app itself
 * produces (see `plainTextToHtml` below) and for common signatures written
 * elsewhere (paragraphs/line breaks + a few entities).
 */
export function htmlToPlainText(html: string | null | undefined): string {
  if (!html) return '';
  return html
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<\/(p|div|li)>/gi, '\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&#39;/gi, "'")
    .replace(/&quot;/gi, '"')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

/** Inverse of `htmlToPlainText`, for saving a plain-input signature/edit as HTML. */
export function plainTextToHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .split('\n')
    .join('<br/>');
}

/** Fixed quick-reaction set for the thread reader's long-press picker. */
export const REACTION_EMOJIS = ['👍', '❤️', '😂', '🎉', '✅'] as const;

/**
 * Human copy for the composer's Smart Send suggestion line. `suggestedAt` is
 * the backend's inferred next-open-window instant (see
 * backend/internal/service/smartsend.go), so this is a straight time format,
 * not a client-side recomputation.
 */
export function formatSendSuggestion(suggestion: SendSuggestion): string {
  const opens = suggestion.sampleSize === 1 ? '1 past open' : `${suggestion.sampleSize} past opens`;
  return `Smart Send: they tend to open mail around ${formatTime(suggestion.suggestedAt)} (${opens})`;
}
