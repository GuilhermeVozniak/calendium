import type { ConnectedAccount } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type * as React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listThreadsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({ listThreads: (...args: unknown[]) => listThreadsMock(...args) }),
}));

const fetchAccountsMock = vi.fn();
vi.mock('@/lib/settings-data', () => ({
  fetchAccounts: (...args: unknown[]) => fetchAccountsMock(...args),
  fetchSnippets: vi.fn(async () => []),
}));

import { ACTIVE_ACCOUNT_STORAGE_KEY, ActiveAccountProvider } from '@/lib/use-accounts';
import { useThreadList } from '@/lib/use-mail';

function makeAccount(id: string, email: string): ConnectedAccount {
  return {
    id,
    provider: 'google',
    email,
    status: 'active',
    scopes: [],
    vipSenders: [],
    signatureHtml: '',
    autoBcc: [],
    lastSyncedAt: null,
    createdAt: new Date().toISOString(),
  };
}

function makeWrapper(client: QueryClient, withProvider: boolean) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    const inner = withProvider ? (
      <ActiveAccountProvider>{children}</ActiveAccountProvider>
    ) : (
      children
    );
    return <QueryClientProvider client={client}>{inner}</QueryClientProvider>;
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  listThreadsMock.mockResolvedValue({ items: [], nextCursor: null });
  fetchAccountsMock.mockResolvedValue([]);
});

describe('useThreadList — account scoping', () => {
  it('passes an explicit accountId to listThreads and keys the cache by it', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderHook(() => useThreadList({ split: 'important', accountId: 'acc9' }), {
      wrapper: makeWrapper(client, false),
    });
    await waitFor(() => expect(listThreadsMock).toHaveBeenCalled());
    expect(listThreadsMock).toHaveBeenCalledWith(
      expect.objectContaining({ split: 'important', accountId: 'acc9' })
    );
    const keys = client
      .getQueryCache()
      .findAll()
      .map((q) => q.queryKey);
    expect(keys).toContainEqual(['threads', 'important', null, '', 'acc9']);
  });

  it('scopes by the persisted active account from context', async () => {
    window.localStorage.setItem(ACTIVE_ACCOUNT_STORAGE_KEY, 'acc2');
    fetchAccountsMock.mockResolvedValue([
      makeAccount('acc1', 'ada@calendium.app'),
      makeAccount('acc2', 'work@acme.com'),
    ]);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderHook(() => useThreadList({ split: 'important' }), {
      wrapper: makeWrapper(client, true),
    });
    await waitFor(() =>
      expect(listThreadsMock).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc2' }))
    );
  });

  it('omits accountId entirely when no account is active (all accounts)', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderHook(() => useThreadList({ split: 'important' }), {
      wrapper: makeWrapper(client, false),
    });
    await waitFor(() => expect(listThreadsMock).toHaveBeenCalled());
    expect(listThreadsMock).toHaveBeenCalledWith(
      expect.objectContaining({ accountId: undefined })
    );
    const keys = client
      .getQueryCache()
      .findAll()
      .map((q) => q.queryKey);
    expect(keys).toContainEqual(['threads', 'important', null, '', null]);
  });
});
