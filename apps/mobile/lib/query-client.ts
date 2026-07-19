import AsyncStorage from '@react-native-async-storage/async-storage';
import { createAsyncStoragePersister } from '@tanstack/query-async-storage-persister';
import { QueryClient } from '@tanstack/react-query';
import type { PersistQueryClientOptions } from '@tanstack/react-query-persist-client';

/** Cache lifetime shared by the persister (maxAge) and react-query GC (gcTime). */
const SEVEN_DAYS_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * Query-key prefixes worth restoring on a cold offline start: real mail and
 * calendar data. Ephemeral/liveness keys are deliberately excluded — notably
 * `api-online` (a persisted "online" flag would fabricate connectivity) and
 * AI suggestion keys (`instant-replies`, `send-suggestion`), which must be
 * regenerated, never replayed from disk as if fresh.
 */
export const PERSISTED_PREFIXES: readonly string[] = [
  'threads',
  'thread',
  'opens',
  'events',
  'calendars',
  'event-templates',
  'accounts',
  'subscription',
  'classifiers',
];

/**
 * The app-wide react-query client. Kept in its own module (rather than inline in
 * context/providers) so non-React code — notably auth sign-out — can clear the
 * cache directly (queryClient.clear()) without depending on the provider tree.
 * This prevents one account's cached mail/calendar from bleeding into the next
 * sign-in on a shared device. gcTime matches the persister's maxAge so queries
 * survive in cache long enough to actually be persisted for 7 days.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
      gcTime: SEVEN_DAYS_MS,
    },
  },
});

/** AsyncStorage-backed persister so the last-known mail/calendar survives restarts. */
export const persister = createAsyncStoragePersister({ storage: AsyncStorage, key: 'rq:v1' });

/**
 * Options for PersistQueryClientProvider (context/providers.tsx). The buster
 * matches the other Calendium apps so a shape-breaking cache change can
 * invalidate every platform at once.
 */
export const persistOptions: Omit<PersistQueryClientOptions, 'queryClient'> = {
  persister,
  maxAge: SEVEN_DAYS_MS,
  buster: 'calendium-cache-v1',
  dehydrateOptions: {
    // Only persist real, successfully-fetched data on the allowlist — an
    // in-flight or errored query persisted to disk would replay as fake state.
    shouldDehydrateQuery: (query) =>
      query.state.status === 'success' && PERSISTED_PREFIXES.includes(String(query.queryKey[0])),
  },
};
