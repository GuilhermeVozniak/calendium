'use client';

import { useQuery } from '@tanstack/react-query';
import { fetchInstance, type InstanceInfo } from '@calendium/shared';

import { DEMO_MODE } from '@/lib/demo';

export const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080';

/**
 * Offline demo descriptor — used only in explicit demo mode (lib/demo.ts)
 * when the real GET /v1/instance is unreachable, so the AI suite (and other
 * feature-gated UI) has something to render for Playwright/local demo
 * browsing without a live backend. `ai: true` is the one that matters here;
 * billing/push stay off to match the pre-existing (no-backend) demo shell.
 */
const DEMO_INSTANCE: InstanceInfo = {
  name: 'Calendium (demo)',
  mode: 'cloud',
  version: 'demo',
  authBaseUrl: `${API_BASE_URL}/api/auth`,
  authProviders: ['email', 'google', 'microsoft'],
  undoSendSeconds: 15,
  features: { billing: false, google: true, microsoft: true, ai: true, push: false },
};

/**
 * Server capability discovery (GET /v1/instance). Gate UI on this instead of
 * assuming Cloud: features.billing, features.ai, features.push, authProviders.
 */
export function useInstance() {
  return useQuery<InstanceInfo>({
    queryKey: ['instance', API_BASE_URL],
    queryFn: async () => {
      try {
        return await fetchInstance(API_BASE_URL);
      } catch (err) {
        if (DEMO_MODE) return DEMO_INSTANCE;
        throw err;
      }
    },
    staleTime: 5 * 60 * 1000,
    retry: 1,
  });
}
