import type { Conferencing, Event } from '@calendium/shared';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { JoinButton } from './join-button';

function makeEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 'evt-1',
    calendarId: 'cal-1',
    title: 'Standup',
    description: null,
    location: null,
    start: new Date('2026-08-01T09:00:00Z').toISOString(),
    end: new Date('2026-08-01T09:30:00Z').toISOString(),
    allDay: false,
    recurrenceRule: null,
    attendees: [],
    conferencing: null,
    status: 'confirmed',
    visibility: 'default',
    reminderMinutes: [],
    ...overrides,
  };
}

describe('JoinButton', () => {
  it('renders nothing when no conference link is detected', () => {
    const { container } = render(<JoinButton event={makeEvent()} />);
    expect(container).toBeEmptyDOMElement();
  });

  describe('provider labels', () => {
    it('labels a Zoom link', () => {
      const event = makeEvent({ location: 'https://zoom.us/j/1234567890?pwd=abc' });
      render(<JoinButton event={event} />);
      expect(screen.getByRole('button', { name: /join zoom/i })).toBeInTheDocument();
    });

    it('labels a Google Meet link', () => {
      const event = makeEvent({ location: 'https://meet.google.com/abc-defg-hij' });
      render(<JoinButton event={event} />);
      expect(screen.getByRole('button', { name: /join meet/i })).toBeInTheDocument();
    });

    it('labels a Teams link', () => {
      const event = makeEvent({ location: 'https://teams.live.com/meet/12345' });
      render(<JoinButton event={event} />);
      expect(screen.getByRole('button', { name: /join teams/i })).toBeInTheDocument();
    });

    it('labels a Webex link', () => {
      const event = makeEvent({ location: 'https://acme.webex.com/meet/jdoe' });
      render(<JoinButton event={event} />);
      expect(screen.getByRole('button', { name: /join webex/i })).toBeInTheDocument();
    });

    it('labels from the structured conferencing field over location/description', () => {
      const conferencing: Conferencing = { provider: 'teams', url: 'https://teams.microsoft.com/l/meetup-join/x' };
      const event = makeEvent({
        conferencing,
        location: 'https://meet.google.com/abc-defg-hij',
      });
      render(<JoinButton event={event} />);
      expect(screen.getByRole('button', { name: /join teams/i })).toBeInTheDocument();
    });
  });

  describe('joinable window (now vs. start/end)', () => {
    const start = new Date('2026-08-01T09:00:00Z');
    const end = new Date('2026-08-01T09:30:00Z');

    function eventAt() {
      return makeEvent({
        location: 'https://meet.google.com/abc-defg-hij',
        start: start.toISOString(),
        end: end.toISOString(),
      });
    }

    it('is prominent (solid) exactly 5 minutes before start', () => {
      render(<JoinButton event={eventAt()} now={new Date('2026-08-01T08:55:00Z')} />);
      const button = screen.getByRole('button', { name: /join meet/i });
      expect(button).toHaveAttribute('data-joinable', 'true');
    });

    it('is not yet joinable one millisecond before the lead window opens', () => {
      render(<JoinButton event={eventAt()} now={new Date('2026-08-01T08:54:59.999Z')} />);
      const button = screen.getByRole('button', { name: /join meet/i });
      expect(button).toHaveAttribute('data-joinable', 'false');
    });

    it('is prominent during the event', () => {
      render(<JoinButton event={eventAt()} now={new Date('2026-08-01T09:10:00Z')} />);
      const button = screen.getByRole('button', { name: /join meet/i });
      expect(button).toHaveAttribute('data-joinable', 'true');
    });

    it('is not joinable at or after the end time', () => {
      render(<JoinButton event={eventAt()} now={end} />);
      const button = screen.getByRole('button', { name: /join meet/i });
      expect(button).toHaveAttribute('data-joinable', 'false');
    });
  });

  describe('default `now`', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });
    afterEach(() => {
      vi.useRealTimers();
    });

    it('falls back to the current time when `now` is omitted', () => {
      vi.setSystemTime(new Date('2026-08-01T09:10:00Z'));
      const event = makeEvent({
        location: 'https://meet.google.com/abc-defg-hij',
        start: new Date('2026-08-01T09:00:00Z').toISOString(),
        end: new Date('2026-08-01T09:30:00Z').toISOString(),
      });
      render(<JoinButton event={event} />);
      const button = screen.getByRole('button', { name: /join meet/i });
      expect(button).toHaveAttribute('data-joinable', 'true');
    });
  });

  describe('click behavior', () => {
    it('opens the conference url in a new tab and does not bubble the click', () => {
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      const onOuterClick = vi.fn();
      const event = makeEvent({ location: 'https://zoom.us/j/1234567890' });
      render(
        // biome-ignore lint/a11y/noStaticElementInteractions: test harness asserting the click doesn't bubble, not real UI.
        // biome-ignore lint/a11y/useKeyWithClickEvents: same — no keyboard affordance needed for this assertion-only wrapper.
        <div onClick={onOuterClick}>
          <JoinButton event={event} now={new Date('2026-08-01T09:10:00Z')} />
        </div>
      );
      screen.getByRole('button', { name: /join zoom/i }).click();
      expect(openSpy).toHaveBeenCalledWith(
        'https://zoom.us/j/1234567890',
        '_blank',
        'noopener,noreferrer'
      );
      expect(onOuterClick).not.toHaveBeenCalled();
      openSpy.mockRestore();
    });
  });

  describe('size prop', () => {
    it('accepts a "sm" size', () => {
      const event = makeEvent({ location: 'https://meet.google.com/abc-defg-hij' });
      render(<JoinButton event={event} now={new Date('2026-08-01T09:10:00Z')} size="sm" />);
      expect(screen.getByRole('button', { name: /join meet/i })).toBeInTheDocument();
    });
  });
});
