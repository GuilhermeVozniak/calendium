import type { Conferencing } from './types';

/**
 * Conference-link detection shared by web, desktop, and mobile: a structured
 * `conferencing` field always wins (set when the backend created the meeting),
 * otherwise a provider URL is sniffed out of the free-text location, then the
 * description, in that order.
 */
export type ConferenceProvider = 'meet' | 'zoom' | 'teams' | 'webex' | 'other';

export interface DetectedConference {
  provider: ConferenceProvider;
  url: string;
}

const PATTERNS: ReadonlyArray<readonly [ConferenceProvider, RegExp]> = [
  ['zoom', /https?:\/\/(?:[\w-]+\.)?zoom\.us\/(?:j|my|s|w)\/[\w?=&.-]+/i],
  ['meet', /https?:\/\/meet\.google\.com\/[a-z]{3}-[a-z]{4}-[a-z]{3}(?:\?[\w=&-]*)?/i],
  ['teams', /https?:\/\/teams\.(?:microsoft|live)\.com\/(?:l\/meetup-join|meet)\/[\w%/=?.&-]+/i],
  ['webex', /https?:\/\/(?:[\w-]+\.)?webex\.com\/(?:meet|join|wbxmjs|[\w-]+\/j\.php)[\w%/=?.&-]*/i],
];

/**
 * Detects a conference join link on an event. Detection order: structured
 * `conferencing` field wins, then `location`, then `description`.
 */
export function detectConference(ev: {
  conferencing: Conferencing | null;
  location: string | null;
  description: string | null;
}): DetectedConference | null {
  if (ev.conferencing) return { provider: ev.conferencing.provider, url: ev.conferencing.url };
  for (const text of [ev.location, ev.description]) {
    if (!text) continue;
    for (const [provider, re] of PATTERNS) {
      const m = re.exec(text);
      if (m) return { provider, url: m[0] };
    }
  }
  return null;
}

/** Join is offered from `leadMinutes` before start until the event ends. */
export function isJoinable(now: Date, start: Date, end: Date, leadMinutes = 5): boolean {
  return now.getTime() >= start.getTime() - leadMinutes * 60_000 && now < end;
}
