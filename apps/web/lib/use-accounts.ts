'use client';

import * as React from 'react';

import type { ConnectedAccount } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';

import { fetchAccounts } from '@/lib/settings-data';

/** localStorage key backing the persisted active-account selection. */
export const ACTIVE_ACCOUNT_STORAGE_KEY = 'calendium.activeAccountId';

/**
 * Connected accounts for the app shell. Shares the ['accounts'] cache (and
 * the settings surface's fetchAccounts wrapper) so the switcher and settings
 * never disagree about what is connected.
 */
export function useAccounts() {
  return useQuery({ queryKey: ['accounts'], queryFn: fetchAccounts, staleTime: 60_000 });
}

export interface ActiveAccountContextValue {
  accounts: ConnectedAccount[];
  /** null = all accounts (no filter). */
  activeAccountId: string | null;
  setActiveAccountId: (id: string | null) => void;
}

// Default value keeps consumers (palette, hooks) working outside the provider
// as an honest "all accounts, none connected" state.
const ActiveAccountContext = React.createContext<ActiveAccountContextValue>({
  accounts: [],
  activeAccountId: null,
  setActiveAccountId: () => {},
});

function readStoredAccountId(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage.getItem(ACTIVE_ACCOUNT_STORAGE_KEY);
  } catch {
    return null;
  }
}

/**
 * Active-account scope for the inbox (M2.6 task 12): mod+1..9 switches to the
 * nth connected account, mod+0 back to all. The selection persists in
 * localStorage and is restored truthfully — once the real account list
 * resolves without the stored id (account disconnected elsewhere), the
 * selection falls back to "all accounts" rather than filtering by a ghost.
 */
export function ActiveAccountProvider({ children }: { children: React.ReactNode }) {
  const { data, isSuccess } = useAccounts();
  const accounts = React.useMemo(() => data ?? [], [data]);
  const [activeAccountId, setActive] = React.useState<string | null>(readStoredAccountId);

  const setActiveAccountId = React.useCallback((id: string | null) => {
    setActive(id);
    try {
      if (id === null) window.localStorage.removeItem(ACTIVE_ACCOUNT_STORAGE_KEY);
      else window.localStorage.setItem(ACTIVE_ACCOUNT_STORAGE_KEY, id);
    } catch {
      // Persistence is best-effort; the in-memory selection still applies.
    }
  }, []);

  React.useEffect(() => {
    if (!isSuccess || activeAccountId === null) return;
    if (!accounts.some((a) => a.id === activeAccountId)) setActiveAccountId(null);
  }, [isSuccess, accounts, activeAccountId, setActiveAccountId]);

  const value = React.useMemo(
    () => ({ accounts, activeAccountId, setActiveAccountId }),
    [accounts, activeAccountId, setActiveAccountId]
  );
  return React.createElement(ActiveAccountContext.Provider, { value }, children);
}

/** Active-account context; outside the provider it resolves to "all accounts". */
export function useActiveAccount(): ActiveAccountContextValue {
  return React.useContext(ActiveAccountContext);
}
