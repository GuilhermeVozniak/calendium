'use client';

import * as React from 'react';
import { QueryClient } from '@tanstack/react-query';
import { createAsyncStoragePersister } from '@tanstack/query-async-storage-persister';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { createIndexedDbKv, createMemoryKv, type AsyncKeyValueStore } from '@calendium/shared';

import { ThemeProvider } from '@/components/theme-provider';
import { Toaster } from '@/components/ui/sonner';
import { QUERY_CACHE_STORAGE_KEY } from '@/lib/offline/queue';

/** Bump on breaking query-shape changes — invalidates every persisted entry. */
const CACHE_BUSTER = 'calendium-cache-v1';

/** Persisted entries older than this are dropped on restore. */
const PERSIST_MAX_AGE = 7 * 24 * 60 * 60 * 1000; // 7 days

/**
 * Query-key prefixes worth persisting across sessions for offline reads.
 * `['api-online']` is deliberately NOT here — a cached reachability answer is
 * stale by definition. DEMO_MODE data flows through the same cache —
 * acceptable, it is labeled by `source` already.
 */
const PERSISTED_PREFIXES = new Set([
  'threads',
  'thread',
  'drafts',
  'events',
  'calendars',
  'snippets',
  'accounts',
  'search',
]);

/**
 * Persistence KV with the degrade path (M2.6 Task 3): quota/persist errors
 * from the shared KV layer propagate as rejections — catch them here and keep
 * the app running WITHOUT persistence (warn once) rather than crashing over a
 * cache. Failed reads behave as a cache miss.
 */
function createPersistKv(): AsyncKeyValueStore {
  if (typeof indexedDB === 'undefined') return createMemoryKv(); // SSR / unsupported browser
  const kv = createIndexedDbKv();
  let warned = false;
  const degrade = (err: unknown): null => {
    if (!warned) {
      warned = true;
      console.warn('calendium: query-cache persistence degraded — continuing without it', err);
    }
    return null;
  };
  return {
    getItem: (key) => kv.getItem(key).catch(degrade),
    setItem: async (key, value) => {
      await kv.setItem(key, value).catch(degrade);
    },
    removeItem: async (key) => {
      await kv.removeItem(key).catch(degrade);
    },
  };
}

export function Providers({ children }: { children: React.ReactNode }) {
  const [queryClient] = React.useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 30_000,
            // Must be ≥ the persister's maxAge, or restored queries (which
            // start with zero observers) are garbage-collected instantly.
            gcTime: PERSIST_MAX_AGE,
            retry: 1,
            refetchOnWindowFocus: false,
          },
        },
      })
  );
  const [persister] = React.useState(() =>
    createAsyncStoragePersister({
      storage: createPersistKv(),
      key: QUERY_CACHE_STORAGE_KEY,
      throttleTime: 1000,
    })
  );

  return (
    <ThemeProvider>
      <PersistQueryClientProvider
        client={queryClient}
        persistOptions={{
          persister,
          maxAge: PERSIST_MAX_AGE,
          buster: CACHE_BUSTER,
          dehydrateOptions: {
            shouldDehydrateQuery: (query) => PERSISTED_PREFIXES.has(String(query.queryKey[0])),
          },
        }}
      >
        {children}
        <Toaster />
      </PersistQueryClientProvider>
    </ThemeProvider>
  );
}
