'use client';

import type {
  AiComposeRequest,
  AiComposeResponse,
  InboxSplit,
  Message,
  Page,
  Thread,
  ThreadAction,
} from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';

import { getApiClient } from '@/lib/api';
import {
  applyMockAction,
  getMockThread,
  getMockThreads,
  mockAiCompose,
  mockRemindThread,
  mockSnoozeThread,
} from '@/lib/mail-mock';
import { fetchSnippets } from '@/lib/settings-data';
import type { MailboxView } from '@/lib/mail-utils';

/**
 * Mail data layer: TanStack Query hooks against the Calendium API with a
 * transparent fallback to the offline demo dataset (lib/mail-mock.ts) when the
 * API is unreachable. Every result carries its `source` so the UI can surface
 * demo mode.
 */

export type DataSource = 'api' | 'demo';

export interface MailListParams {
  split?: InboxSplit;
  view?: MailboxView;
  q?: string;
}

export interface ThreadListResult {
  page: Page<Thread>;
  source: DataSource;
}

export interface ThreadDetailResult {
  thread: Thread;
  messages: Message[];
  source: DataSource;
}

/** Lightweight reachability probe driving the offline-demo banner. */
export function useApiOnline(): boolean {
  const { data } = useQuery({
    queryKey: ['api-online'],
    queryFn: async () => {
      try {
        await getApiClient().getMe();
        return true;
      } catch {
        return false;
      }
    },
    retry: false,
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
  // Undefined while probing — don't flash the banner before we know.
  return data !== false;
}

export function useThreadList(params: MailListParams) {
  return useQuery({
    queryKey: ['threads', params.split ?? null, params.view ?? null, params.q ?? ''],
    queryFn: async (): Promise<ThreadListResult> => {
      try {
        const page = await getApiClient().listThreads({
          split: params.view ? undefined : (params.split ?? 'important'),
          labelId: params.view,
          q: params.q || undefined,
          limit: 100,
        });
        return { page, source: 'api' };
      } catch {
        return { page: getMockThreads(params), source: 'demo' };
      }
    },
    placeholderData: (previous) => previous,
  });
}

export function useThreadDetail(threadId: string | null) {
  return useQuery({
    queryKey: ['thread', threadId],
    enabled: threadId !== null,
    queryFn: async (): Promise<ThreadDetailResult | null> => {
      if (!threadId) return null;
      try {
        const detail = await getApiClient().getThread(threadId);
        return { ...detail, source: 'api' };
      } catch {
        const mock = getMockThread(threadId);
        return mock ? { ...mock, source: 'demo' } : null;
      }
    },
  });
}

// Shares the single ['snippets'] cache + fetcher with the settings surface so
// compose and settings never seed the cache from divergent mock stores.
export function useSnippets() {
  return useQuery({
    queryKey: ['snippets'],
    queryFn: fetchSnippets,
    staleTime: 5 * 60_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations (optimistic; demo store kept in sync so refetches don't undo)
// ---------------------------------------------------------------------------

function applyActionToThread(thread: Thread, action: ThreadAction): Thread {
  switch (action) {
    case 'star':
      return { ...thread, starred: true };
    case 'unstar':
      return { ...thread, starred: false };
    case 'read':
      return { ...thread, unread: false };
    case 'unread':
      return { ...thread, unread: true };
    default:
      return thread;
  }
}

const REMOVES_FROM_LIST: ReadonlySet<ThreadAction> = new Set(['archive', 'trash', 'spam']);

export function useMailActions() {
  const queryClient = useQueryClient();

  function updateCaches(threadId: string, patch: (t: Thread) => Thread, removeFromLists: boolean) {
    queryClient.setQueriesData<ThreadListResult | undefined>(
      { queryKey: ['threads'] },
      (data) => {
        if (!data) return data;
        const items = removeFromLists
          ? data.page.items.filter((t) => t.id !== threadId)
          : data.page.items.map((t) => (t.id === threadId ? patch(t) : t));
        return { ...data, page: { ...data.page, items } };
      }
    );
    queryClient.setQueryData<ThreadDetailResult | null | undefined>(
      ['thread', threadId],
      (data) => (data ? { ...data, thread: patch(data.thread) } : data)
    );
  }

  async function act(threadId: string, action: ThreadAction): Promise<void> {
    updateCaches(threadId, (t) => applyActionToThread(t, action), REMOVES_FROM_LIST.has(action));
    applyMockAction(threadId, action);
    try {
      await getApiClient().actOnThread(threadId, action);
    } catch {
      // Demo mode — the optimistic update is the source of truth.
    }
  }

  async function snooze(threadId: string, until: string): Promise<void> {
    updateCaches(threadId, (t) => ({ ...t, snoozedUntil: until }), true);
    mockSnoozeThread(threadId, until);
    try {
      await getApiClient().snoozeThread(threadId, until);
    } catch {
      // Demo mode.
    }
  }

  async function remind(threadId: string, remindAt: string | null): Promise<void> {
    updateCaches(threadId, (t) => ({ ...t, remindAt }), false);
    mockRemindThread(threadId, remindAt);
    try {
      await getApiClient().setThreadReminder(threadId, remindAt);
    } catch {
      // Demo mode.
    }
  }

  return { act, snooze, remind };
}

// ---------------------------------------------------------------------------
// AI compose (graceful demo fallback)
// ---------------------------------------------------------------------------

export async function runAiCompose(
  req: AiComposeRequest
): Promise<AiComposeResponse & { source: DataSource }> {
  try {
    const res = await getApiClient().aiCompose(req);
    return { ...res, source: 'api' };
  } catch {
    return { ...mockAiCompose(req), source: 'demo' };
  }
}
