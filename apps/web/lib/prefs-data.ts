'use client';

import type { UserPrefs } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import type { DataSource } from '@/lib/use-mail';

// Demo-only prefs store (honesty policy: reached only when
// DEMO_MODE is on and the real API is unreachable).
// Uses localStorage for persistence across page reloads in tests/demos.
function getDemoPrefs(): UserPrefs {
  if (typeof window === 'undefined') return { splitOrder: [] };
  const stored = window.localStorage.getItem('calendium.demo-prefs');
  if (stored) {
    try {
      return JSON.parse(stored);
    } catch {
      return { splitOrder: [] };
    }
  }
  return { splitOrder: [] };
}

function setDemoPrefs(prefs: UserPrefs): void {
  if (typeof window === 'undefined') return;
  window.localStorage.setItem('calendium.demo-prefs', JSON.stringify(prefs));
}

export interface PrefsResult {
  prefs: UserPrefs;
  source: DataSource;
}

export function usePrefs() {
  return useQuery({
    queryKey: ['prefs'],
    staleTime: 60_000,
    queryFn: async (): Promise<PrefsResult> => {
      try {
        return { prefs: await getApiClient().getPrefs(), source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { prefs: getDemoPrefs(), source: 'demo' };
        throw err;
      }
    },
  });
}

export function useUpdatePrefs() {
  const queryClient = useQueryClient();
  return async function updatePrefs(prefs: UserPrefs): Promise<void> {
    const previous = queryClient.getQueryData<PrefsResult>(['prefs']);
    queryClient.setQueryData<PrefsResult | undefined>(['prefs'], (data) =>
      data ? { ...data, prefs } : { prefs, source: 'api' }
    );
    if (DEMO_MODE) setDemoPrefs(prefs);
    try {
      await getApiClient().updatePrefs(prefs);
    } catch (err) {
      if (DEMO_MODE) return;
      if (previous) queryClient.setQueryData(['prefs'], previous);
      toast.error('Could not save your preferences.');
      throw err;
    }
  };
}
