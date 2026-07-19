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

  it('works as backing store for the outbox storage adapter', async () => {
    const kv = createIndexedDbKv('kv-test-outbox');
    const storage = createKvOutboxStorage(kv);
    const entries = [entry(1), entry(2), entry(3)];
    await storage.save(entries);
    const reopened = createKvOutboxStorage(createIndexedDbKv('kv-test-outbox'));
    await expect(reopened.load()).resolves.toEqual(entries);
  });
});
