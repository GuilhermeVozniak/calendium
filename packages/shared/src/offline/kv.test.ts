import 'fake-indexeddb/auto';

import { describe, expect, it } from 'vitest';

import type { OutboxEntry } from './outbox';
import {
  type AsyncKeyValueStore,
  createIndexedDbKv,
  createKvOutboxStorage,
  createMemoryKv,
} from './kv';

function entry(seq: number): OutboxEntry {
  return {
    id: `e-${seq}`,
    seq,
    createdAt: '2026-07-19T00:00:00Z',
    attempts: 0,
    status: 'queued',
    lastError: null,
    action: { kind: 'thread_open', threadId: `t${seq}` },
  };
}

// ---------------------------------------------------------------------------
// createMemoryKv
// ---------------------------------------------------------------------------

describe('createMemoryKv', () => {
  it('returns null for a missing key', async () => {
    const kv = createMemoryKv();
    await expect(kv.getItem('nope')).resolves.toBeNull();
  });

  it('round-trips set/get and overwrites on repeated set', async () => {
    const kv = createMemoryKv();
    await kv.setItem('k', 'v1');
    await expect(kv.getItem('k')).resolves.toBe('v1');
    await kv.setItem('k', 'v2');
    await expect(kv.getItem('k')).resolves.toBe('v2');
  });

  it('removes a key, and removing a missing key is a no-op', async () => {
    const kv = createMemoryKv();
    await kv.setItem('k', 'v');
    await kv.removeItem('k');
    await expect(kv.getItem('k')).resolves.toBeNull();
    await expect(kv.removeItem('k')).resolves.toBeUndefined();
  });

  it('stores empty string values distinctly from missing keys', async () => {
    const kv = createMemoryKv();
    await kv.setItem('k', '');
    await expect(kv.getItem('k')).resolves.toBe('');
  });

  it('isolates instances from each other', async () => {
    const a = createMemoryKv();
    const b = createMemoryKv();
    await a.setItem('k', 'v');
    await expect(b.getItem('k')).resolves.toBeNull();
  });
});

// ---------------------------------------------------------------------------
// createKvOutboxStorage
// ---------------------------------------------------------------------------

describe('createKvOutboxStorage', () => {
  it('loads [] when nothing was saved', async () => {
    const storage = createKvOutboxStorage(createMemoryKv());
    await expect(storage.load()).resolves.toEqual([]);
  });

  it('round-trips entries under the outbox:v1 key', async () => {
    const kv = createMemoryKv();
    const storage = createKvOutboxStorage(kv);
    const entries = [entry(1), entry(2)];
    await storage.save(entries);
    await expect(storage.load()).resolves.toEqual(entries);
    await expect(kv.getItem('outbox:v1')).resolves.toBe(JSON.stringify(entries));
  });

  it('saving [] then loading returns []', async () => {
    const kv = createMemoryKv();
    const storage = createKvOutboxStorage(kv);
    await storage.save([entry(1)]);
    await storage.save([]);
    await expect(storage.load()).resolves.toEqual([]);
  });

  it('loads corrupt JSON as [] instead of crashing', async () => {
    const kv = createMemoryKv();
    await kv.setItem('outbox:v1', '{not json');
    const storage = createKvOutboxStorage(kv);
    await expect(storage.load()).resolves.toEqual([]);
  });

  it('loads a non-array JSON payload as []', async () => {
    const kv = createMemoryKv();
    await kv.setItem('outbox:v1', '{"hello":"world"}');
    const storage = createKvOutboxStorage(kv);
    await expect(storage.load()).resolves.toEqual([]);
  });

  it('propagates persist failures from the underlying KV', async () => {
    const failing: AsyncKeyValueStore = {
      getItem: () => Promise.resolve(null),
      setItem: () => Promise.reject(new Error('quota exceeded')),
      removeItem: () => Promise.resolve(),
    };
    const storage = createKvOutboxStorage(failing);
    await expect(storage.save([entry(1)])).rejects.toThrow('quota exceeded');
  });
});

// ---------------------------------------------------------------------------
// createIndexedDbKv (fake-indexeddb)
// ---------------------------------------------------------------------------

/**
 * Simulates a commit-phase abort (e.g. quota exceeded surfacing only on
 * transaction.onabort): each write request succeeds at the request level,
 * then the transaction is aborted before it can commit. fake-indexeddb has
 * no quota to exhaust, so tx.abort() after request success is the closest
 * spec-accurate stand-in. Returns a restore function.
 */
function abortWritesAfterRequestSuccess(): () => void {
  const original = IDBDatabase.prototype.transaction;
  IDBDatabase.prototype.transaction = function (
    this: IDBDatabase,
    ...args: Parameters<IDBDatabase['transaction']>
  ): IDBTransaction {
    const tx = original.apply(this, args);
    const originalObjectStore = tx.objectStore.bind(tx);
    tx.objectStore = (name: string): IDBObjectStore => {
      const store = originalObjectStore(name);
      for (const method of ['put', 'delete'] as const) {
        const fn = (store[method] as (...a: unknown[]) => IDBRequest).bind(store);
        (store as unknown as Record<string, unknown>)[method] = (...a: unknown[]) => {
          const request = fn(...a);
          request.addEventListener('success', () => tx.abort());
          return request;
        };
      }
      return store;
    };
    return tx;
  };
  return () => {
    IDBDatabase.prototype.transaction = original;
  };
}

describe('createIndexedDbKv', () => {
  it('returns null for a missing key', async () => {
    const kv = createIndexedDbKv('kv-test-missing');
    await expect(kv.getItem('nope')).resolves.toBeNull();
  });

  it('round-trips set/get/remove', async () => {
    const kv = createIndexedDbKv('kv-test-roundtrip');
    await kv.setItem('k', 'v1');
    await expect(kv.getItem('k')).resolves.toBe('v1');
    await kv.setItem('k', 'v2');
    await expect(kv.getItem('k')).resolves.toBe('v2');
    await kv.removeItem('k');
    await expect(kv.getItem('k')).resolves.toBeNull();
  });

  it('persists across two instances with the same dbName', async () => {
    const first = createIndexedDbKv('kv-test-persist');
    await first.setItem('durable', 'yes');
    const second = createIndexedDbKv('kv-test-persist');
    await expect(second.getItem('durable')).resolves.toBe('yes');
  });

  it('isolates databases by dbName', async () => {
    const a = createIndexedDbKv('kv-test-iso-a');
    await a.setItem('k', 'v');
    const b = createIndexedDbKv('kv-test-iso-b');
    await expect(b.getItem('k')).resolves.toBeNull();
  });

  it('handles interleaved operations on many keys', async () => {
    const kv = createIndexedDbKv('kv-test-many');
    await Promise.all(
      Array.from({ length: 20 }, (_, i) => kv.setItem(`k${i}`, `v${i}`))
    );
    await expect(kv.getItem('k7')).resolves.toBe('v7');
    await expect(kv.getItem('k19')).resolves.toBe('v19');
  });

  it('rejects setItem when the transaction aborts after request success, and does not persist', async () => {
    const kv = createIndexedDbKv('kv-test-abort-set');
    await kv.setItem('warm', 'up'); // open the db before installing the hook
    const restore = abortWritesAfterRequestSuccess();
    try {
      await expect(kv.setItem('k', 'v')).rejects.toThrow(/abort/i);
    } finally {
      restore();
    }
    await expect(kv.getItem('k')).resolves.toBeNull();
  });

  it('rejects removeItem when the transaction aborts, leaving the value intact', async () => {
    const kv = createIndexedDbKv('kv-test-abort-remove');
    await kv.setItem('keep', 'me');
    const restore = abortWritesAfterRequestSuccess();
    try {
      await expect(kv.removeItem('keep')).rejects.toThrow(/abort/i);
    } finally {
      restore();
    }
    await expect(kv.getItem('keep')).resolves.toBe('me');
  });

  it('does not resolve setItem before the transaction commit event fires', async () => {
    const kv = createIndexedDbKv('kv-test-commit-order');
    await kv.setItem('warm', 'up');
    let committed = false;
    const original = IDBDatabase.prototype.transaction;
    IDBDatabase.prototype.transaction = function (
      this: IDBDatabase,
      ...args: Parameters<IDBDatabase['transaction']>
    ): IDBTransaction {
      const tx = original.apply(this, args);
      // Registered before kv.ts assigns tx.oncomplete, so it fires first.
      tx.addEventListener('complete', () => {
        committed = true;
      });
      return tx;
    };
    try {
      await kv.setItem('k', 'v');
      expect(committed).toBe(true);
    } finally {
      IDBDatabase.prototype.transaction = original;
    }
  });

  it('closes its connection on versionchange so another tab can upgrade', async () => {
    const dbName = 'kv-test-versionchange';
    const kv = createIndexedDbKv(dbName);
    await kv.setItem('k', 'v');
    // A version-2 open only completes if the kv connection closes itself;
    // otherwise this open stays blocked and the test times out.
    const upgraded = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open(dbName, 2);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error ?? new Error('open failed'));
    });
    expect(upgraded.version).toBe(2);
    upgraded.close();
  });

  it('works as backing store for the outbox storage adapter', async () => {
    const kv = createIndexedDbKv('kv-test-outbox');
    const storage = createKvOutboxStorage(kv);
    const entries = [entry(1), entry(2), entry(3)];
    await storage.save(entries);
    const reopened = createKvOutboxStorage(createIndexedDbKv('kv-test-outbox'));
    await expect(reopened.load()).resolves.toEqual(entries);
  });
});
