'use client';

import { useQuery } from '@tanstack/react-query';
import { fetchInstance, type InstanceInfo } from '@calendium/shared';

export const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080';

/**
 * Server capability discovery (GET /v1/instance). Gate UI on this instead of
 * assuming Cloud: features.billing, features.ai, features.push, authProviders.
 */
export function useInstance() {
  return useQuery<InstanceInfo>({
    queryKey: ['instance', API_BASE_URL],
    queryFn: () => fetchInstance(API_BASE_URL),
    staleTime: 5 * 60 * 1000,
    retry: 1,
  });
}
