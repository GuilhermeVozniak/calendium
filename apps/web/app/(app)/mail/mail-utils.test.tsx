import { describe, expect, it } from 'vitest';
import type { Thread } from '@calendium/shared';
import { sharedLabelIds } from '@/lib/mail-helpers';

const createThread = (id: string, labelIds: string[]): Thread => ({
  id,
  accountId: 'acc',
  subject: 'Test',
  snippet: 'Test snippet',
  participants: [],
  labelIds,
  split: 'important',
  messageCount: 1,
  unread: false,
  starred: false,
  lastMessageAt: new Date().toISOString(),
  openedAt: null,
  snoozedUntil: null,
  remindAt: null,
  unsubscribeMailto: null,
  unsubscribeUrl: null,
  unsubscribeOneClick: false,
});

describe('sharedLabelIds', () => {
  it('returns empty set for empty threads array', () => {
    expect(sharedLabelIds([])).toEqual(new Set());
  });

  it('returns all label IDs for single thread', () => {
    const thread = createThread('t1', ['label_a', 'label_b', 'label_c']);
    expect(sharedLabelIds([thread])).toEqual(new Set(['label_a', 'label_b', 'label_c']));
  });

  it('returns intersection of label IDs for multiple threads', () => {
    const t1 = createThread('t1', ['label_a', 'label_b', 'label_c']);
    const t2 = createThread('t2', ['label_b', 'label_c', 'label_d']);
    const t3 = createThread('t3', ['label_b', 'label_e']);

    expect(sharedLabelIds([t1, t2])).toEqual(new Set(['label_b', 'label_c']));
    expect(sharedLabelIds([t1, t2, t3])).toEqual(new Set(['label_b']));
  });

  it('returns empty set when threads share no labels', () => {
    const t1 = createThread('t1', ['label_a']);
    const t2 = createThread('t2', ['label_b']);

    expect(sharedLabelIds([t1, t2])).toEqual(new Set());
  });

  it('returns empty set when a thread has no labels', () => {
    const t1 = createThread('t1', ['label_a', 'label_b']);
    const t2 = createThread('t2', []);

    expect(sharedLabelIds([t1, t2])).toEqual(new Set());
  });
});
