import { describe, expect, it } from 'vitest';

import {
  applyMockLabel,
  getMockLabels,
  getMockThread,
  getMockThreads,
  mockArchiveOlderThan,
  mockBulkAction,
  mockReactToMessage,
  mockRemoveReaction,
  mockUnsubscribe,
} from './mail-mock';

describe('mail-mock triage extensions', () => {
  it('seeds demo labels on the demo account', () => {
    const labels = getMockLabels();
    expect(labels.length).toBeGreaterThanOrEqual(3);
    expect(labels.every((l) => l.accountId === 'acc_demo')).toBe(true);
  });

  it('applyMockLabel toggles labelIds on a thread', () => {
    const anyThread = getMockThreads({}).items[0]!;
    const label = getMockLabels()[0]!;
    applyMockLabel(anyThread.id, label.id, true);
    expect(getMockThread(anyThread.id)!.thread.labelIds).toContain(label.id);
    applyMockLabel(anyThread.id, label.id, false);
    expect(getMockThread(anyThread.id)!.thread.labelIds).not.toContain(label.id);
  });

  it('mockBulkAction archives every id given', () => {
    const items = getMockThreads({ split: 'news' }).items;
    expect(items.length).toBeGreaterThanOrEqual(2);
    const ids = items.slice(0, 2).map((t) => t.id);
    mockBulkAction(ids, 'archive');
    const after = getMockThreads({ split: 'news' }).items.map((t) => t.id);
    for (const id of ids) expect(after).not.toContain(id);
    mockBulkAction(ids, 'move_to_inbox'); // restore for other tests
  });

  it('mockUnsubscribe reports the strongest available method', () => {
    const news = getMockThreads({ split: 'news' }).items;
    const oneClick = news.find((t) => t.unsubscribeOneClick);
    expect(oneClick).toBeDefined();
    expect(mockUnsubscribe(oneClick!.id).method).toBe('one_click');
  });

  it('mockUnsubscribe reports "link" with a url for a link-only sender (thr_21)', () => {
    // thr_21 (Changelog News) only carries an unsubscribe url — no mailto, no
    // one-click support — so it must never be reported as completed.
    const result = mockUnsubscribe('thr_21');
    expect(result.method).toBe('link');
    expect(result.url).toBe('https://changelog.com/news/unsubscribe');
  });

  it('mockArchiveOlderThan only archives strictly older inbox threads', () => {
    const before = getMockThreads({}).items.length;
    const count = mockArchiveOlderThan(new Date(Date.now() - 365 * 24 * 3_600_000).toISOString());
    expect(count).toBe(0); // nothing in the demo set is a year old
    expect(getMockThreads({}).items.length).toBe(before);
  });
});

describe('mail-mock reactions (M2.5)', () => {
  const otherMessageId = 'thr_01_m1'; // from Priya, not the demo account itself
  const ownMessageId = 'thr_01_m2'; // from the demo account (me@calendium.app)

  it('adds a local reaction when no tiny reply is requested', () => {
    const result = mockReactToMessage(otherMessageId, '👍', false);
    expect(result.reaction.emoji).toBe('👍');
    expect(result.reaction.delivery).toBe('local');
    expect(result.draftId).toBeNull();
    expect(getMockThread('thr_01')!.messages.find((m) => m.id === otherMessageId)!.reactions).toContainEqual(
      result.reaction
    );
    mockRemoveReaction(otherMessageId, '👍'); // restore for other tests
  });

  it('reports delivery "sent" with a draftId when sendReply is requested for someone else\'s message', () => {
    const result = mockReactToMessage(otherMessageId, '🎉', true);
    expect(result.reaction.delivery).toBe('sent');
    expect(result.draftId).not.toBeNull();
    mockRemoveReaction(otherMessageId, '🎉'); // restore for other tests
  });

  it('never tiny-replies to your own message even when sendReply is requested', () => {
    const result = mockReactToMessage(ownMessageId, '✅', true);
    expect(result.reaction.delivery).toBe('local');
    expect(result.draftId).toBeNull();
    mockRemoveReaction(ownMessageId, '✅'); // restore for other tests
  });

  it('is idempotent: reacting with an emoji already present returns the existing row and no new draft', () => {
    const first = mockReactToMessage(otherMessageId, '❤️', true);
    const second = mockReactToMessage(otherMessageId, '❤️', true);
    expect(second.reaction).toEqual(first.reaction);
    expect(second.draftId).toBeNull();
    mockRemoveReaction(otherMessageId, '❤️'); // restore for other tests
  });

  it('mockRemoveReaction removes only the given emoji', () => {
    mockReactToMessage(otherMessageId, '👍', false);
    mockReactToMessage(otherMessageId, '🎉', false);
    mockRemoveReaction(otherMessageId, '👍');
    const reactions = getMockThread('thr_01')!.messages.find((m) => m.id === otherMessageId)!.reactions;
    expect(reactions.map((r) => r.emoji)).toEqual(['🎉']);
    mockRemoveReaction(otherMessageId, '🎉'); // restore for other tests
  });
});
