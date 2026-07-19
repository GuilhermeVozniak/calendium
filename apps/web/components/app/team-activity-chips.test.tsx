import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { TeamThreadActivity } from '@calendium/shared';

import type { CollabEvent } from '@/lib/collab-stream';

import { TeamActivityChips, summarizeActivity } from './team-activity-chips';

const row = (over: Partial<TeamThreadActivity>): TeamThreadActivity => ({
  teamId: 'team1',
  userId: 'mate',
  conversationKey: '<k@x>',
  openedAt: null,
  repliedAt: null,
  ...over,
});

/** Subscribe seam that captures the handler so tests can push events. */
function fakeStream() {
  let handler: ((ev: CollabEvent) => void) | undefined;
  const unsubscribe = vi.fn();
  const subscribe = (onEvent: (ev: CollabEvent) => void) => {
    handler = onEvent;
    return unsubscribe;
  };
  return { subscribe, emit: (ev: CollabEvent) => handler?.(ev), unsubscribe };
}

describe('TeamActivityChips', () => {
  it('renders nothing when there is no activity', async () => {
    const fetchActivity = vi.fn(async () => [] as TeamThreadActivity[]);
    const { subscribe } = fakeStream();
    render(<TeamActivityChips threadId="t1" fetchActivity={fetchActivity} subscribe={subscribe} />);
    await waitFor(() => expect(fetchActivity).toHaveBeenCalledWith('t1'));
    expect(screen.queryByTestId('team-activity-chips')).not.toBeInTheDocument();
  });

  it('shows seen and replied chips per teammate', async () => {
    const fetchActivity = vi.fn(async () => [
      row({ userId: 'ana', openedAt: '2026-07-18T10:00:00Z' }),
      row({ userId: 'bob', openedAt: '2026-07-18T10:00:00Z', repliedAt: '2026-07-18T10:05:00Z' }),
    ]);
    const { subscribe } = fakeStream();
    render(<TeamActivityChips threadId="t1" fetchActivity={fetchActivity} subscribe={subscribe} />);
    expect(await screen.findByText('ana seen')).toBeInTheDocument();
    expect(screen.getByText('bob replied')).toBeInTheDocument();
  });

  it('refetches on activity.updated collab events only', async () => {
    let calls = 0;
    const fetchActivity = vi.fn(async () => {
      calls++;
      return calls === 1
        ? [row({ userId: 'ana', openedAt: '2026-07-18T10:00:00Z' })]
        : [row({ userId: 'ana', openedAt: '2026-07-18T10:00:00Z', repliedAt: '2026-07-18T10:07:00Z' })];
    });
    const stream = fakeStream();
    render(<TeamActivityChips threadId="t1" fetchActivity={fetchActivity} subscribe={stream.subscribe} />);
    expect(await screen.findByText('ana seen')).toBeInTheDocument();

    stream.emit({ topic: 'team:team1', type: 'comment.created', payload: {} });
    expect(fetchActivity).toHaveBeenCalledTimes(1); // foreign event types are ignored

    stream.emit({ topic: 'team:team1', type: 'activity.updated', payload: {} });
    expect(await screen.findByText('ana replied')).toBeInTheDocument();
    expect(fetchActivity).toHaveBeenCalledTimes(2);
  });

  it('unsubscribes from the stream on unmount', async () => {
    const fetchActivity = vi.fn(async () => [] as TeamThreadActivity[]);
    const stream = fakeStream();
    const { unmount } = render(
      <TeamActivityChips threadId="t1" fetchActivity={fetchActivity} subscribe={stream.subscribe} />
    );
    await waitFor(() => expect(fetchActivity).toHaveBeenCalled());
    unmount();
    expect(stream.unsubscribe).toHaveBeenCalled();
  });
});

describe('summarizeActivity', () => {
  it('dedupes a teammate across teams, replied wins, newest timestamps kept', () => {
    const got = summarizeActivity([
      row({ teamId: 'team1', userId: 'ana', openedAt: '2026-07-18T10:00:00Z' }),
      row({ teamId: 'team2', userId: 'ana', openedAt: '2026-07-18T11:00:00Z', repliedAt: '2026-07-18T11:05:00Z' }),
    ]);
    expect(got).toHaveLength(1);
    expect(got[0].openedAt).toBe('2026-07-18T11:00:00Z');
    expect(got[0].repliedAt).toBe('2026-07-18T11:05:00Z');
  });
});
