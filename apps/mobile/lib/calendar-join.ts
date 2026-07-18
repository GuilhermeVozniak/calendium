// Join-button visibility for the calendar tab (Task 20, mirroring Task 13's
// web JoinButton semantics with a mobile-appropriate twist): web keeps the
// button always mounted and just dims it outside the joinable window, but
// the mobile row/detail sheet has no room for a persistently-visible ghost
// button, so here the affordance simply doesn't exist until `isJoinable`.
import { detectConference, isJoinable } from '@calendium/shared';
import type { ConferenceProvider, Conferencing } from '@calendium/shared';

const PROVIDER_LABEL: Record<ConferenceProvider, string> = {
  meet: 'Meet',
  zoom: 'Zoom',
  teams: 'Teams',
  webex: 'Webex',
  other: 'call',
};

export interface JoinInfo {
  url: string;
  label: string;
}

/**
 * Returns the Join button's url + label ("Join Zoom", "Join Meet", …) only
 * when a conference link is detected AND the event is currently inside its
 * joinable window (`leadMinutes` before start until end, see
 * `@calendium/shared`'s `isJoinable`). Returns null otherwise so callers can
 * render nothing rather than a disabled/dimmed control.
 */
export function getJoinInfo(
  event: {
    conferencing: Conferencing | null;
    location: string | null;
    description: string | null;
    start: string;
    end: string;
  },
  now: Date = new Date()
): JoinInfo | null {
  const conference = detectConference(event);
  if (!conference) return null;
  if (!isJoinable(now, new Date(event.start), new Date(event.end))) return null;
  return { url: conference.url, label: `Join ${PROVIDER_LABEL[conference.provider]}` };
}
