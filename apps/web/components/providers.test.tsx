import 'fake-indexeddb/auto';

import { IDBFactory } from 'fake-indexeddb';
import { useQuery } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { createIndexedDbKv } from '@calendium/shared';
import { beforeEach, describe, expect, it } from 'vitest';

import { Providers } from '@/components/providers';

/**
 * Queries under a persisted prefix (['threads', …]) must land in the
 * IndexedDB-backed store after the persister's throttle window; the
 * ['api-online'] reachability probe must never be persisted.
 */
function Probe() {
  const persisted = useQuery({
    queryKey: ['threads', 'probe'],
    queryFn: async () => ({ marker: 'persisted-thread-data' }),
  });
  useQuery({
    queryKey: ['api-online'],
    queryFn: async () => true,
  });
  return <div>{persisted.data ? 'probe-loaded' : 'probe-loading'}</div>;
}

beforeEach(() => {
  // Fresh IndexedDB universe per test: both the Providers persister and the
  // assertion KV below lazily open against the CURRENT global.
  globalThis.indexedDB = new IDBFactory();
});

describe('Providers', () => {
  it('renders children', () => {
    render(
      <Providers>
        <span>hello-child</span>
      </Providers>
    );
    expect(screen.getByText('hello-child')).toBeInTheDocument();
  });

  it('persists whitelisted query prefixes to IndexedDB, but never api-online', async () => {
    render(
      <Providers>
        <Probe />
      </Providers>
    );
    await screen.findByText('probe-loaded');

    const kv = createIndexedDbKv();
    await waitFor(
      async () => {
        const raw = await kv.getItem('rq:v1');
        expect(raw).not.toBeNull();
        expect(raw).toContain('persisted-thread-data');
      },
      { timeout: 8000, interval: 250 }
    );

    const raw = await kv.getItem('rq:v1');
    expect(raw).toContain('calendium-cache-v1'); // buster stamped into the payload
    expect(raw).not.toContain('api-online');
  }, 15_000);
});
