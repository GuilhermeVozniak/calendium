import type { OutboxEntry, OutboxStorage } from './outbox';

/** Matches @react-native-async-storage/async-storage AND
 *  @tanstack/query-async-storage-persister's expected storage shape. */
export interface AsyncKeyValueStore {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
}

function promisify<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('IndexedDB request failed'));
  });
}

/** Browser IndexedDB-backed KV (web + desktop webview). Lazily opens on first call. */
export function createIndexedDbKv(
  dbName = 'calendium-offline',
  storeName = 'kv'
): AsyncKeyValueStore {
  let dbPromise: Promise<IDBDatabase> | null = null;

  const open = (): Promise<IDBDatabase> => {
    dbPromise ??= new Promise((resolve, reject) => {
      const request = indexedDB.open(dbName, 1);
      request.onupgradeneeded = () => {
        request.result.createObjectStore(storeName);
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => {
        dbPromise = null; // allow a retry on the next call
        reject(request.error ?? new Error(`IndexedDB open failed: ${dbName}`));
      };
    });
    return dbPromise;
  };

  const run = async <T>(
    mode: IDBTransactionMode,
    op: (store: IDBObjectStore) => IDBRequest<T>
  ): Promise<T> => {
    const db = await open();
    return promisify(op(db.transaction(storeName, mode).objectStore(storeName)));
  };

  return {
    async getItem(key) {
      const value = await run('readonly', (store) => store.get(key));
      return typeof value === 'string' ? value : null;
    },
    async setItem(key, value) {
      await run('readwrite', (store) => store.put(value, key));
    },
    async removeItem(key) {
      await run('readwrite', (store) => store.delete(key));
    },
  };
}

/** In-memory KV for tests and non-browser environments. */
export function createMemoryKv(): AsyncKeyValueStore {
  const data = new Map<string, string>();
  return {
    getItem: (key) => Promise.resolve(data.get(key) ?? null),
    setItem: (key, value) => {
      data.set(key, value);
      return Promise.resolve();
    },
    removeItem: (key) => {
      data.delete(key);
      return Promise.resolve();
    },
  };
}

const OUTBOX_KEY = 'outbox:v1';

/** OutboxStorage over any KV: JSON array under key 'outbox:v1'.
 *  Corrupt/unparseable payloads load as [] (never crash the app over cache);
 *  save failures propagate — a failed persist must not report success. */
export function createKvOutboxStorage(kv: AsyncKeyValueStore): OutboxStorage {
  return {
    async load() {
      const raw = await kv.getItem(OUTBOX_KEY);
      if (raw === null) return [];
      try {
        const parsed: unknown = JSON.parse(raw);
        return Array.isArray(parsed) ? (parsed as OutboxEntry[]) : [];
      } catch {
        return [];
      }
    },
    save(entries) {
      return kv.setItem(OUTBOX_KEY, JSON.stringify(entries));
    },
  };
}
