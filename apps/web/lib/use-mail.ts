'use client';

import type {
  AiComposeRequest,
  AiComposeResponse,
  Draft,
  InboxSplit,
  Message,
  Page,
  Thread,
  ThreadAction,
} from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
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
 * Mail data layer: TanStack Query hooks against the Calendium API. Outside
 * explicit demo mode (lib/demo.ts) it surfaces real loading / empty / error
 * states and never fabricates success; only when DEMO_MODE is on does it fall
 * back to the offline sample dataset (lib/mail-mock.ts). Results carry their
 * `source` so the UI can label demo data.
 */

export type DataSource = 'api' | 'demo';

export interface MailListParams {
  split?: InboxSplit;
  view?: MailboxView;
  q?: string;
  /** Skip fetching (e.g. while the Drafts pseudo-view is showing instead). */
  enabled?: boolean;
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

export interface DraftListResult {
  drafts: Draft[];
  source: DataSource;
}

/** Lightweight reachability probe driving the offline/demo banner. */
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
  // 'drafts' is not a thread view — it lists drafts via useDrafts, so never
  // send it as a listThreads view.
  const view = params.view && params.view !== 'drafts' ? params.view : undefined;
  return useQuery({
    queryKey: ['threads', params.split ?? null, view ?? null, params.q ?? ''],
    enabled: params.enabled ?? true,
    queryFn: async (): Promise<ThreadListResult> => {
      try {
        const page = await getApiClient().listThreads({
          split: view ? undefined : (params.split ?? 'important'),
          view,
          q: params.q || undefined,
          limit: 100,
        });
        return { page, source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { page: getMockThreads(params), source: 'demo' };
        throw err;
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
      } catch (err) {
        if (DEMO_MODE) {
          const mock = getMockThread(threadId);
          return mock ? { ...mock, source: 'demo' } : null;
        }
        throw err;
      }
    },
  });
}

/** Server-persisted drafts (GET /v1/mail/drafts) for the Drafts pseudo-view. */
export function useDrafts(enabled: boolean) {
  return useQuery({
    queryKey: ['drafts'],
    enabled,
    queryFn: async (): Promise<DraftListResult> => {
      try {
        const drafts = await getApiClient().listDrafts();
        return { drafts, source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { drafts: [], source: 'demo' };
        throw err;
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
// Mutations (optimistic; failures revert + surface unless in demo mode)
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

const ACTION_ERROR: Partial<Record<ThreadAction, string>> = {
  archive: 'Could not archive the conversation.',
  trash: 'Could not delete the conversation.',
  spam: 'Could not report spam.',
  star: 'Could not update the star.',
  unstar: 'Could not update the star.',
  read: 'Could not mark the conversation read.',
  unread: 'Could not mark the conversation unread.',
  move_to_inbox: 'Could not move the conversation to the inbox.',
};

export function useDraftActions() {
  const queryClient = useQueryClient();

  async function remove(draftId: string): Promise<void> {
    const previous = queryClient.getQueryData<DraftListResult>(['drafts']);
    queryClient.setQueryData<DraftListResult | undefined>(['drafts'], (data) =>
      data ? { ...data, drafts: data.drafts.filter((d) => d.id !== draftId) } : data
    );
    try {
      await getApiClient().deleteDraft(draftId);
    } catch (err) {
      if (DEMO_MODE) return;
      if (previous) queryClient.setQueryData(['drafts'], previous);
      toast.error('Could not delete the draft.');
      throw err;
    }
  }

  return { remove };
}

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

  /**
   * Applies an optimistic cache patch, calls the API, and — outside demo mode —
   * reverts the caches and toasts on failure so a rejected mutation is never
   * shown as success.
   */
  async function runOptimistic(
    threadId: string,
    patch: (t: Thread) => Thread,
    removeFromLists: boolean,
    apiCall: () => Promise<unknown>,
    mockApply: () => void,
    errorMessage: string
  ): Promise<void> {
    const previousLists = queryClient.getQueriesData<ThreadListResult | undefined>({
      queryKey: ['threads'],
    });
    const previousDetail = queryClient.getQueryData<ThreadDetailResult | null | undefined>([
      'thread',
      threadId,
    ]);
    updateCaches(threadId, patch, removeFromLists);
    if (DEMO_MODE) mockApply();
    try {
      await apiCall();
    } catch {
      if (DEMO_MODE) return;
      for (const [key, data] of previousLists) queryClient.setQueryData(key, data);
      queryClient.setQueryData(['thread', threadId], previousDetail);
      toast.error(errorMessage);
    }
  }

  async function act(threadId: string, action: ThreadAction): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => applyActionToThread(t, action),
      REMOVES_FROM_LIST.has(action),
      () => getApiClient().actOnThread(threadId, action),
      () => applyMockAction(threadId, action),
      ACTION_ERROR[action] ?? 'Could not update the conversation.'
    );
  }

  async function snooze(threadId: string, until: string): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => ({ ...t, snoozedUntil: until }),
      true,
      () => getApiClient().snoozeThread(threadId, until),
      () => mockSnoozeThread(threadId, until),
      'Could not snooze the conversation.'
    );
  }

  async function remind(threadId: string, remindAt: string | null): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => ({ ...t, remindAt }),
      false,
      () => getApiClient().setThreadReminder(threadId, remindAt),
      () => mockRemindThread(threadId, remindAt),
      'Could not set the reminder.'
    );
  }

  /**
   * Records that the owner opened a thread (POST .../open): marks it read
   * server-side and stamps real openedAt. Idempotent and low-stakes, so the
   * optimistic read state is left in place on failure (it reconciles on the
   * next refetch) rather than flipping the unread dot back mid-view.
   */
  async function markOpened(threadId: string): Promise<void> {
    const nowIso = new Date().toISOString();
    updateCaches(threadId, (t) => ({ ...t, unread: false, openedAt: t.openedAt ?? nowIso }), false);
    if (DEMO_MODE) applyMockAction(threadId, 'read');
    try {
      await getApiClient().markThreadOpened(threadId);
    } catch {
      // Reconciles on the next refetch.
    }
  }

  return { act, snooze, remind, markOpened };
}

// ---------------------------------------------------------------------------
// AI (real API; demo fallback only in DEMO_MODE)
// ---------------------------------------------------------------------------

export async function runAiCompose(
  req: AiComposeRequest
): Promise<AiComposeResponse & { source: DataSource }> {
  try {
    const res = await getApiClient().aiCompose(req);
    return { ...res, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) return { ...mockAiCompose(req), source: 'demo' };
    throw err;
  }
}

export async function runAiSummarize(
  threadId: string
): Promise<{ text: string; source: DataSource }> {
  try {
    const res = await getApiClient().aiSummarize({ threadId, prompt: '' });
    return { text: res.text, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) {
      return { text: mockAiCompose({ action: 'summarize', prompt: '', threadId }).text, source: 'demo' };
    }
    throw err;
  }
}

export async function runAiAsk(
  threadId: string,
  prompt: string
): Promise<{ text: string; source: DataSource }> {
  try {
    const res = await getApiClient().aiAsk({ threadId, prompt });
    return { text: res.text, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) {
      return { text: mockAiCompose({ action: 'ask', prompt, threadId }).text, source: 'demo' };
    }
    throw err;
  }
}
