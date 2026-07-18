'use client';

import { Video } from 'lucide-react';

import type { ConferenceProvider, Event } from '@calendium/shared';
import { detectConference, isJoinable } from '@calendium/shared';

import { Button } from '@/components/ui/button';

const PROVIDER_LABEL: Record<ConferenceProvider, string> = {
  meet: 'Meet',
  zoom: 'Zoom',
  teams: 'Teams',
  webex: 'Webex',
  other: 'call',
};

export interface JoinButtonProps {
  event: Event;
  /** Defaults to the current time; pass explicitly in tests for determinism. */
  now?: Date;
  size?: 'sm' | 'default';
}

/**
 * One-click join affordance (Task 13). Renders a provider-labeled button
 * ("Join Zoom" / "Join Meet" / "Join Teams" / "Join Webex") that opens the
 * detected conference URL in a new tab. Solid/primary from `leadMinutes`
 * before start until the event ends (see `isJoinable`); a quieter ghost style
 * outside that window. Renders nothing when no conference link is detected.
 */
export function JoinButton({ event, now, size = 'default' }: JoinButtonProps) {
  const conference = detectConference(event);
  if (!conference) return null;

  const joinable = isJoinable(now ?? new Date(), new Date(event.start), new Date(event.end));
  const label = PROVIDER_LABEL[conference.provider];

  return (
    <Button
      type="button"
      variant={joinable ? 'default' : 'ghost'}
      size={size}
      data-joinable={joinable}
      onClick={(e) => {
        e.stopPropagation();
        window.open(conference.url, '_blank', 'noopener,noreferrer');
      }}
    >
      <Video />
      Join {label}
    </Button>
  );
}
