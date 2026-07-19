import { describe, expect, it, vi } from 'vitest';

import type { ApiClient } from '../client';
import { ApiRequestError } from '../client';
import type { DraftInput } from '../types';
import {
  isLocalDraftId,
  LOCAL_DRAFT_PREFIX,
  Outbox,
  type OutboxEntry,
  type OutboxStorage,
} from './outbox';

class MemStorage implements OutboxStorage {
  data: OutboxEntry[] = [];
  saves = 0;

  load(): Promise<OutboxEntry[]> {
    return Promise.resolve(structuredClone(this.data));
  }

  save(entries: OutboxEntry[]): Promise<void> {
    this.saves += 1;
    this.data = structuredClone(entries);
    return Promise.resolve();
  }
}

function makeOutbox(storage: OutboxStorage = new MemStorage()) {
  let n = 0;
  const outbox = new Outbox(storage, () => `id-${++n}`);
  return outbox;
}

async function initOutbox(storage: OutboxStorage = new MemStorage()) {
  const outbox = makeOutbox(storage);
  await outbox.init();
  return outbox;
}

const DRAFT_INPUT: DraftInput = {
  accountId: 'acc1',
  threadId: null,
  to: [{ name: null, email: 'to@example.com' }],
  cc: [],
  bcc: [],
  subject: 'Hello',
  bodyHtml: '<p>Hi</p>',
  scheduledAt: null,
};

interface FakeClientMethods {
  actOnThread: ReturnType<typeof vi.fn>;
  snoozeThread: ReturnType<typeof vi.fn>;
  setThreadReminder: ReturnType<typeof vi.fn>;
  markThreadOpened: ReturnType<typeof vi.fn>;
  saveDraft: ReturnType<typeof vi.fn>;
  updateDraft: ReturnType<typeof vi.fn>;
  sendDraft: ReturnType<typeof vi.fn>;
}

function fakeClient(overrides: Partial<FakeClientMethods> = {}) {
  const methods: FakeClientMethods = {
    actOnThread: vi.fn().mockResolvedValue({}),
    snoozeThread: vi.fn().mockResolvedValue({}),
    setThreadReminder: vi.fn().mockResolvedValue({}),
    markThreadOpened: vi.fn().mockResolvedValue(undefined),
    saveDraft: vi.fn().mockResolvedValue({ id: 'srv-draft-1' }),
    updateDraft: vi.fn().mockResolvedValue({}),
    sendDraft: vi.fn().mockResolvedValue({}),
    ...overrides,
  };
  return { methods, client: methods as unknown as ApiClient };
}

// ---------------------------------------------------------------------------
// isLocalDraftId
// ---------------------------------------------------------------------------

describe('isLocalDraftId', () => {
  it('recognizes local draft ids by prefix', () => {
    expect(isLocalDraftId(`${LOCAL_DRAFT_PREFIX}abc`)).toBe(true);
    expect(isLocalDraftId('draft-123')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// enqueue + persistence
// ---------------------------------------------------------------------------

describe('enqueue and persistence', () => {
  it('persists enqueued entries and round-trips through init() on a fresh Outbox', async () => {
    const storage = new MemStorage();
    const a = await initOutbox(storage);
    await a.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await a.enqueue({ kind: 'thread_snooze', threadId: 't2', until: '2026-08-01T00:00:00Z' });

    const b = makeOutbox(storage);
    await b.init();
    expect(b.pending).toHaveLength(2);
    expect(b.pending.map((e) => e.action.kind)).toEqual(['thread_action', 'thread_snooze']);
    expect(b.queuedCount).toBe(2);

    // seq continues past what was loaded
    const next = await b.enqueue({ kind: 'thread_open', threadId: 't3' });
    expect(next?.seq).toBeGreaterThan(Math.max(...b.pending.slice(0, 2).map((e) => e.seq)));
  });

  it('star then unstar while offline coalesces to an empty queue', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'star' });
    const result = await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'unstar' });
    expect(result).toBeNull();
    expect(outbox.pending).toHaveLength(0);
  });

  it('cancels archive with move_to_inbox and read with unread', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'move_to_inbox' });
    await outbox.enqueue({ kind: 'thread_action', threadId: 't2', action: 'read' });
    await outbox.enqueue({ kind: 'thread_action', threadId: 't2', action: 'unread' });
    expect(outbox.pending).toHaveLength(0);
  });

  it('does not cancel across different threads', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'star' });
    await outbox.enqueue({ kind: 'thread_action', threadId: 't2', action: 'unstar' });
    expect(outbox.pending).toHaveLength(2);
  });

  it('dedupes duplicate thread_actions, returning the existing entry', async () => {
    const outbox = await initOutbox();
    const first = await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    const second = await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    expect(second).toBe(first);
    expect(outbox.pending).toHaveLength(1);
  });

  it('replaces a queued snooze for the same thread, keeping the original seq', async () => {
    const outbox = await initOutbox();
    const first = await outbox.enqueue({
      kind: 'thread_snooze',
      threadId: 't1',
      until: '2026-08-01T00:00:00Z',
    });
    const second = await outbox.enqueue({
      kind: 'thread_snooze',
      threadId: 't1',
      until: '2026-09-01T00:00:00Z',
    });
    expect(second?.id).toBe(first?.id);
    expect(second?.seq).toBe(first?.seq);
    expect(outbox.pending).toHaveLength(1);
    expect(outbox.pending[0].action).toEqual({
      kind: 'thread_snooze',
      threadId: 't1',
      until: '2026-09-01T00:00:00Z',
    });
  });

  it('replaces a queued reminder and open for the same thread', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_reminder', threadId: 't1', remindAt: '2026-08-01T00:00:00Z' });
    await outbox.enqueue({ kind: 'thread_reminder', threadId: 't1', remindAt: null });
    await outbox.enqueue({ kind: 'thread_open', threadId: 't1' });
    await outbox.enqueue({ kind: 'thread_open', threadId: 't1' });
    expect(outbox.pending).toHaveLength(2);
    expect(outbox.pending[0].action).toEqual({ kind: 'thread_reminder', threadId: 't1', remindAt: null });
  });

  it('draft_save replace keeps its seq ahead of a queued draft_send', async () => {
    const outbox = await initOutbox();
    const save = await outbox.enqueue({
      kind: 'draft_save',
      draftId: 'local-1',
      accountId: 'acc1',
      input: DRAFT_INPUT,
    });
    const send = await outbox.enqueue({ kind: 'draft_send', draftId: 'local-1' });
    const updatedInput = { ...DRAFT_INPUT, subject: 'Updated' };
    const replaced = await outbox.enqueue({
      kind: 'draft_save',
      draftId: 'local-1',
      accountId: 'acc1',
      input: updatedInput,
    });
    expect(replaced?.id).toBe(save?.id);
    expect(replaced?.seq).toBe(save?.seq);
    expect(replaced!.seq).toBeLessThan(send!.seq);
    expect(outbox.pending).toHaveLength(2);
    const saved = outbox.pending.find((e) => e.action.kind === 'draft_save');
    expect(saved?.action).toMatchObject({ input: { subject: 'Updated' } });
  });

  it('never coalesces draft_send entries', async () => {
    const outbox = await initOutbox();
    // A queued send is not replaced by another send for the same draft
    // (removeForDraft is the undo path); it must not merge with saves either.
    const first = await outbox.enqueue({ kind: 'draft_send', draftId: 'd1' });
    const second = await outbox.enqueue({ kind: 'draft_send', draftId: 'd1' });
    expect(second?.id).not.toBe(first?.id);
    expect(outbox.pending).toHaveLength(2);
  });

  it('notifies subscribers on changes and stops after unsubscribe', async () => {
    const outbox = await initOutbox();
    const seen: number[] = [];
    const unsubscribe = outbox.subscribe((entries) => seen.push(entries.length));
    await outbox.enqueue({ kind: 'thread_open', threadId: 't1' });
    expect(seen).toEqual([1]);
    unsubscribe();
    await outbox.enqueue({ kind: 'thread_open', threadId: 't2' });
    expect(seen).toEqual([1]);
  });
});

// ---------------------------------------------------------------------------
// removeForDraft / dismiss
// ---------------------------------------------------------------------------

describe('removeForDraft', () => {
  it('clears queued save and send entries for the draft only', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'draft_save', draftId: 'd1', accountId: 'acc1', input: DRAFT_INPUT });
    await outbox.enqueue({ kind: 'draft_send', draftId: 'd1' });
    await outbox.enqueue({ kind: 'draft_send', draftId: 'd2' });
    await outbox.enqueue({ kind: 'thread_open', threadId: 't1' });

    await outbox.removeForDraft('d1');
    expect(outbox.pending.map((e) => e.action.kind)).toEqual(['draft_send', 'thread_open']);
  });
});

describe('dismiss', () => {
  it('removes a surfaced entry by id', async () => {
    const outbox = await initOutbox();
    const entry = await outbox.enqueue({ kind: 'thread_open', threadId: 't1' });
    await outbox.dismiss(entry!.id);
    expect(outbox.pending).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// replay
// ---------------------------------------------------------------------------

describe('replay', () => {
  it('replays queued entries in seq order, removing them on success', async () => {
    const storage = new MemStorage();
    const outbox = await initOutbox(storage);
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await outbox.enqueue({ kind: 'thread_snooze', threadId: 't2', until: '2026-08-01T00:00:00Z' });
    await outbox.enqueue({ kind: 'thread_open', threadId: 't3' });

    const calls: string[] = [];
    const { client } = fakeClient({
      actOnThread: vi.fn().mockImplementation(() => {
        calls.push('actOnThread');
        return Promise.resolve({});
      }),
      snoozeThread: vi.fn().mockImplementation(() => {
        calls.push('snoozeThread');
        return Promise.resolve({});
      }),
      markThreadOpened: vi.fn().mockImplementation(() => {
        calls.push('markThreadOpened');
        return Promise.resolve(undefined);
      }),
    });

    const report = await outbox.replay(client);
    expect(calls).toEqual(['actOnThread', 'snoozeThread', 'markThreadOpened']);
    expect(report.replayed).toHaveLength(3);
    expect(report.replayed.map((e) => e.seq)).toEqual([...report.replayed.map((e) => e.seq)].sort((x, y) => x - y));
    expect(report.interrupted).toBe(false);
    expect(outbox.pending).toHaveLength(0);
    // outcome persisted only after observation: storage reflects the empty queue
    expect(storage.data).toHaveLength(0);
  });

  it('rewrites a local draft id to the server id for the queued send', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'draft_save', draftId: 'local-1', accountId: 'acc1', input: DRAFT_INPUT });
    await outbox.enqueue({ kind: 'draft_send', draftId: 'local-1' });

    const { client, methods } = fakeClient({
      saveDraft: vi.fn().mockResolvedValue({ id: 'srv-9' }),
    });
    const report = await outbox.replay(client);
    expect(methods.saveDraft).toHaveBeenCalledWith({ ...DRAFT_INPUT, accountId: 'acc1' });
    expect(methods.updateDraft).not.toHaveBeenCalled();
    expect(methods.sendDraft).toHaveBeenCalledWith('srv-9');
    expect(report.replayed).toHaveLength(2);
    expect(outbox.pending).toHaveLength(0);
  });

  it('updates (not creates) drafts that already have a server id', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'draft_save', draftId: 'srv-1', accountId: 'acc1', input: DRAFT_INPUT });
    const { client, methods } = fakeClient();
    await outbox.replay(client);
    expect(methods.updateDraft).toHaveBeenCalledWith('srv-1', DRAFT_INPUT);
    expect(methods.saveDraft).not.toHaveBeenCalled();
  });

  it('marks unrecoverable 4xx entries conflict and keeps replaying', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await outbox.enqueue({ kind: 'thread_snooze', threadId: 't2', until: '2026-08-01T00:00:00Z' });

    const { client, methods } = fakeClient({
      actOnThread: vi.fn().mockRejectedValue(new ApiRequestError(404, 'not_found', 'thread not found')),
    });
    const report = await outbox.replay(client);
    expect(report.conflicts).toHaveLength(1);
    expect(report.conflicts[0].status).toBe('conflict');
    expect(report.conflicts[0].lastError).toBe('thread not found');
    expect(report.interrupted).toBe(false);
    expect(methods.snoozeThread).toHaveBeenCalled();
    // conflict entry stays surfaced for the user; snooze replayed away
    expect(outbox.pending).toHaveLength(1);
    expect(outbox.pending[0].status).toBe('conflict');
  });

  it('backs off on 5xx, bumping attempts, and dead-letters after MAX_ATTEMPTS', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    const { client } = fakeClient({
      actOnThread: vi.fn().mockRejectedValue(new ApiRequestError(500, 'internal', 'boom')),
    });

    for (let attempt = 1; attempt <= 4; attempt++) {
      const report = await outbox.replay(client);
      expect(report.interrupted).toBe(true);
      expect(report.failed).toHaveLength(0);
      expect(outbox.pending[0].status).toBe('queued');
      expect(outbox.pending[0].attempts).toBe(attempt);
    }

    const fifth = await outbox.replay(client);
    expect(fifth.failed).toHaveLength(1);
    expect(fifth.failed[0].status).toBe('failed');
    expect(fifth.failed[0].attempts).toBe(5);
    expect(fifth.interrupted).toBe(false);
    expect(outbox.pending[0].status).toBe('failed');
    expect(outbox.pending[0].lastError).toBe('boom');
  });

  it('stops on a network error leaving the entry queued and untouched', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    const { client } = fakeClient({
      actOnThread: vi.fn().mockRejectedValue(new TypeError('fetch failed')),
    });
    const report = await outbox.replay(client);
    expect(report.interrupted).toBe(true);
    expect(report.replayed).toHaveLength(0);
    expect(outbox.pending[0].status).toBe('queued');
    expect(outbox.pending[0].attempts).toBe(0);
  });

  it('stops on 401 without touching entries', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });
    await outbox.enqueue({ kind: 'thread_open', threadId: 't2' });
    const { client, methods } = fakeClient({
      actOnThread: vi.fn().mockRejectedValue(new ApiRequestError(401, 'unauthorized', 'token expired')),
    });
    const report = await outbox.replay(client);
    expect(report.interrupted).toBe(true);
    expect(report.replayed).toHaveLength(0);
    expect(methods.markThreadOpened).not.toHaveBeenCalled();
    expect(outbox.pending).toHaveLength(2);
    expect(outbox.pending.every((e) => e.status === 'queued' && e.attempts === 0)).toBe(true);
  });

  it('no-ops on a re-entrant replay call while one is in flight', async () => {
    const outbox = await initOutbox();
    await outbox.enqueue({ kind: 'thread_action', threadId: 't1', action: 'archive' });

    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { client } = fakeClient({
      actOnThread: vi.fn().mockImplementation(async () => {
        await gate;
        return {};
      }),
    });

    const first = outbox.replay(client);
    const second = await outbox.replay(client);
    expect(second).toEqual({ replayed: [], conflicts: [], failed: [], interrupted: false });

    release();
    const firstReport = await first;
    expect(firstReport.replayed).toHaveLength(1);
    expect(outbox.pending).toHaveLength(0);
  });

  it('no-ops before init() has loaded storage', async () => {
    const outbox = makeOutbox();
    const { client, methods } = fakeClient();
    const report = await outbox.replay(client);
    expect(report).toEqual({ replayed: [], conflicts: [], failed: [], interrupted: false });
    expect(methods.actOnThread).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// MAX_ENTRIES shedding
// ---------------------------------------------------------------------------

describe('capacity shedding', () => {
  function seedEntry(seq: number, status: OutboxEntry['status'] = 'queued'): OutboxEntry {
    return {
      id: `seed-${seq}`,
      seq,
      createdAt: '2026-07-01T00:00:00Z',
      attempts: 0,
      status,
      lastError: null,
      action: { kind: 'thread_open', threadId: `t${seq}` },
    };
  }

  it('sheds a non-queued entry first when over capacity', async () => {
    const storage = new MemStorage();
    storage.data = Array.from({ length: 500 }, (_, i) =>
      seedEntry(i + 1, i === 249 ? 'conflict' : 'queued')
    );
    const outbox = await initOutbox(storage);

    await outbox.enqueue({ kind: 'thread_open', threadId: 'overflow' });
    expect(outbox.pending).toHaveLength(500);
    expect(outbox.pending.find((e) => e.id === 'seed-250')).toBeUndefined();
    expect(outbox.pending.some((e) => e.status !== 'queued')).toBe(false);
  });

  it('sheds the oldest entry when everything is queued', async () => {
    const storage = new MemStorage();
    storage.data = Array.from({ length: 500 }, (_, i) => seedEntry(i + 1));
    const outbox = await initOutbox(storage);

    await outbox.enqueue({ kind: 'thread_open', threadId: 'overflow' });
    expect(outbox.pending).toHaveLength(500);
    expect(outbox.pending.find((e) => e.id === 'seed-1')).toBeUndefined();
    expect(
      outbox.pending.some(
        (e) => e.action.kind === 'thread_open' && e.action.threadId === 'overflow'
      )
    ).toBe(true);
  });
});
