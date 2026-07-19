'use client';

import type {
  AiComposeRequest,
  AiComposeResponse,
  AiEditAction,
  AiEventProposal,
  AiSource,
  AttachmentHit,
  BulkAction,
  ContactSummary,
  Draft,
  InboxSplit,
  Label,
  Message,
  OpenEvent,
  OutboxAction,
  Page,
  Reaction,
  SendSuggestion,
  Thread,
  ThreadAction,
  UnsubscribeResult,
} from '@calendium/shared';
import { ApiRequestError, UndoStack } from '@calendium/shared';
import { useInfiniteQuery, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { getAccessToken } from '@/lib/auth-client';
import { DEMO_MODE } from '@/lib/demo';
import { env } from '@/lib/env';
import { isOnline, reportApiReachable } from '@/lib/offline/connectivity';
import { isNetworkError, queueAction } from '@/lib/offline/queue';
import {
  applyMockAction,
  applyMockLabel,
  getMockAttachmentBlob,
  getMockContact,
  getMockLabels,
  getMockOpens,
  getMockThread,
  getMockThreads,
  mockAiAskCited,
  mockAiCompose,
  mockArchiveOlderThan,
  mockBulkAction,
  mockInstantReplies,
  mockProposeEvent,
  mockReactToMessage,
  mockRemindThread,
  mockRemoveReaction,
  mockSendSuggestion,
  mockSnoozeThread,
  mockUnsnoozeThread,
  mockUnsubscribe,
  searchMockAttachments,
} from '@/lib/mail-mock';
import { fetchSnippets } from '@/lib/settings-data';
import type { MailboxView } from '@/lib/mail-utils';

/** Module singleton backing the global "undo anything" (Z) shortcut. */
export const mailUndo = new UndoStack();

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

export interface ContactResult {
  contact: ContactSummary;
  source: DataSource;
}

/** Outcome of a bulk mutation — callers derive the succeeded count from it. */
export interface BulkActResult {
  failedCount: number;
}

/**
 * Aggregated sender insights for the contact pane (GET /v1/mail/contacts/{email}).
 * Outside demo mode this surfaces genuine loading/error/empty states — a
 * contact with no shared history resolves to `null` rather than a fabricated
 * summary. `email` may be blank while the caller hasn't resolved a contact yet.
 */
export function useContact(email: string | null) {
  return useQuery({
    queryKey: ['contact', email ?? null],
    enabled: !!email,
    queryFn: async (): Promise<ContactResult | null> => {
      if (!email) return null;
      try {
        const contact = await getApiClient().getContact(email);
        return { contact, source: 'api' };
      } catch (err) {
        if (DEMO_MODE) {
          const contact = getMockContact(email);
          return contact ? { contact, source: 'demo' } : null;
        }
        throw err;
      }
    },
  });
}

/** Lightweight reachability probe driving the offline/demo banner. */
export function useApiOnline(): boolean {
  const { data } = useQuery({
    queryKey: ['api-online'],
    queryFn: async () => {
      try {
        await getApiClient().getMe();
        // Fold real reachability into the connectivity tracker — except in
        // demo mode, where the API is unreachable by design and demo data is
        // served locally (see lib/offline/connectivity.ts).
        if (!DEMO_MODE) reportApiReachable(true);
        return true;
      } catch {
        if (!DEMO_MODE) reportApiReachable(false);
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

/**
 * Smart Send recommendation for a recipient (M2.5). A 404 means the backend
 * doesn't have enough open-time history yet — the NORMAL case for most
 * recipients — so it resolves to `null` rather than surfacing an error;
 * callers render nothing rather than inventing a send time. Any other
 * failure is treated the same way (quietly absent), outside DEMO_MODE where
 * a couple of seeded contacts have a scripted suggestion.
 */
export function useSendSuggestion(email: string | null) {
  return useQuery({
    queryKey: ['send-suggestion', email],
    enabled: email !== null && email !== '',
    staleTime: 5 * 60_000,
    retry: false,
    queryFn: async (): Promise<SendSuggestion | null> => {
      try {
        return await getApiClient().getSendSuggestion(email!);
      } catch {
        if (DEMO_MODE) return mockSendSuggestion(email!);
        return null;
      }
    },
  });
}

export function useLabels() {
  return useQuery({
    queryKey: ['labels'],
    staleTime: 5 * 60_000,
    queryFn: async (): Promise<{ labels: Label[]; source: DataSource }> => {
      try {
        return { labels: await getApiClient().listLabels(), source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { labels: getMockLabels(), source: 'demo' };
        throw err;
      }
    },
  });
}

/**
 * Recent Opens feed (M2.5, task 15): sent messages the recipient has opened,
 * newest first, paged via the server's keyset cursor. `fetchNextPage` fetches
 * the next real page and appends it — it never refetches from the top and
 * pretends to append, so a load-more failure just leaves the already-loaded
 * pages in place. Outside DEMO_MODE, a 402 (no active subscription) propagates
 * as an ApiRequestError so the panel can show the upgrade prompt instead of a
 * generic error.
 */
export function useOpensFeed() {
  return useInfiniteQuery({
    queryKey: ['opens'],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }): Promise<Page<OpenEvent>> => {
      try {
        return await getApiClient().listOpens({ cursor: pageParam, limit: 25 });
      } catch (err) {
        if (DEMO_MODE) return getMockOpens({ cursor: pageParam, limit: 25 });
        throw err;
      }
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor || undefined,
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

const ACTION_INVERSE: Partial<Record<ThreadAction, ThreadAction>> = {
  archive: 'move_to_inbox',
  trash: 'move_to_inbox',
  spam: 'move_to_inbox',
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  move_to_inbox: 'archive',
};

const ACTION_UNDO_LABEL: Partial<Record<ThreadAction, string>> = {
  archive: 'Archive',
  trash: 'Delete',
  spam: 'Report spam',
  star: 'Star',
  unstar: 'Unstar',
  read: 'Mark read',
  unread: 'Mark unread',
  move_to_inbox: 'Move to inbox',
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
   * shown as success. Returns whether the mutation actually took effect: a
   * resolved real API call, (DEMO_MODE) the local fallback applied above, or
   * — when `offlineAction` is provided — a durable outbox enqueue on a
   * transport failure (the action WILL replay; the optimistic patch is kept).
   * Callers use this to decide whether pushing an undo entry is honest — an
   * undo of a queued action enqueues the inverse, which coalesces away in the
   * outbox.
   */
  async function runOptimistic(
    threadId: string,
    patch: (t: Thread) => Thread,
    removeFromLists: boolean,
    apiCall: () => Promise<unknown>,
    mockApply: () => void,
    errorMessage: string,
    offlineAction?: OutboxAction
  ): Promise<boolean> {
    const previousLists = queryClient.getQueriesData<ThreadListResult | undefined>({
      queryKey: ['threads'],
    });
    const previousDetail = queryClient.getQueryData<ThreadDetailResult | null | undefined>([
      'thread',
      threadId,
    ]);
    updateCaches(threadId, patch, removeFromLists);
    if (DEMO_MODE) mockApply();
    if (offlineAction && !isOnline()) {
      // Known-offline: don't burn a doomed request — queue for replay and
      // keep the optimistic patch. (In DEMO_MODE the mock fallback above
      // already applied, so the demo store stays consistent too.)
      await queueAction(offlineAction);
      return true;
    }
    try {
      await apiCall();
      return true;
    } catch (err) {
      if (DEMO_MODE) return true;
      if (offlineAction && isNetworkError(err)) {
        await queueAction(offlineAction); // keep the optimistic patch; no revert
        return true;
      }
      for (const [key, data] of previousLists) queryClient.setQueryData(key, data);
      queryClient.setQueryData(['thread', threadId], previousDetail);
      toast.error(errorMessage);
      return false;
    }
  }

  async function act(
    threadId: string,
    action: ThreadAction,
    opts?: { undoable?: boolean }
  ): Promise<boolean> {
    const ok = await runOptimistic(
      threadId,
      (t) => applyActionToThread(t, action),
      REMOVES_FROM_LIST.has(action),
      () => getApiClient().actOnThread(threadId, action),
      () => applyMockAction(threadId, action),
      ACTION_ERROR[action] ?? 'Could not update the conversation.',
      { kind: 'thread_action', threadId, action }
    );
    const inverse = ACTION_INVERSE[action];
    if (ok && opts?.undoable !== false && inverse) {
      mailUndo.push({
        label: ACTION_UNDO_LABEL[action] ?? action,
        undo: () =>
          act(threadId, inverse, { undoable: false }).then((undone) => {
            if (!undone) throw new Error(`Could not undo: ${ACTION_UNDO_LABEL[action] ?? action}`);
            void queryClient.invalidateQueries({ queryKey: ['threads'] });
          }),
      });
    }
    return ok;
  }

  async function snooze(threadId: string, until: string): Promise<boolean> {
    const ok = await runOptimistic(
      threadId,
      (t) => ({ ...t, snoozedUntil: until }),
      true,
      () => getApiClient().snoozeThread(threadId, until),
      () => mockSnoozeThread(threadId, until),
      'Could not snooze the conversation.',
      { kind: 'thread_snooze', threadId, until }
    );
    if (ok) {
      mailUndo.push({
        label: 'Snooze',
        undo: async () => {
          const undone = await unsnooze(threadId);
          if (!undone) throw new Error('Could not undo snooze');
        },
      });
    }
    return ok;
  }

  async function remind(threadId: string, remindAt: string | null): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => ({ ...t, remindAt }),
      false,
      () => getApiClient().setThreadReminder(threadId, remindAt),
      () => mockRemindThread(threadId, remindAt),
      'Could not set the reminder.',
      { kind: 'thread_reminder', threadId, remindAt }
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
    } catch (err) {
      if (!DEMO_MODE && isNetworkError(err)) {
        // Queue silently — a read receipt shouldn't toast on every open.
        await queueAction({ kind: 'thread_open', threadId }, { silent: true });
        return;
      }
      // Reconciles on the next refetch.
    }
  }

  /**
   * Restores just the failed ids to their pre-mutation snapshot across every
   * matched ['threads'] list — reinserting them if the optimistic update
   * removed them (archive/trash/spam), or reverting their fields otherwise —
   * while leaving the succeeded ids in their new (mutated) state.
   */
  function restoreFailedIds(
    previousLists: Array<[QueryKey, ThreadListResult | undefined]>,
    failedIds: string[]
  ): void {
    if (failedIds.length === 0) return;
    const failedSet = new Set(failedIds);
    for (const [key, prevData] of previousLists) {
      if (!prevData) continue;
      const prevById = new Map(prevData.page.items.map((t) => [t.id, t] as const));
      queryClient.setQueryData<ThreadListResult | undefined>(key, (current) => {
        if (!current) return current;
        const currentIds = new Set(current.page.items.map((t) => t.id));
        const items = current.page.items.map((t) =>
          failedSet.has(t.id) && prevById.has(t.id) ? prevById.get(t.id)! : t
        );
        for (const id of failedIds) {
          if (prevById.has(id) && !currentIds.has(id)) items.push(prevById.get(id)!);
        }
        return { ...current, page: { ...current.page, items } };
      });
    }
  }

  async function bulkAct(
    threadIds: string[],
    action: BulkAction,
    labelId?: string
  ): Promise<BulkActResult> {
    const previousLists = queryClient.getQueriesData<ThreadListResult | undefined>({
      queryKey: ['threads'],
    });
    const removes = action === 'archive' || action === 'trash' || action === 'spam';
    const isLabelAction = action === 'label' || action === 'unlabel';
    const idSet = new Set(threadIds);
    queryClient.setQueriesData<ThreadListResult | undefined>({ queryKey: ['threads'] }, (data) => {
      if (!data) return data;
      const items = removes
        ? data.page.items.filter((t) => !idSet.has(t.id))
        : data.page.items.map((t) =>
            idSet.has(t.id) && !isLabelAction ? applyActionToThread(t, action as ThreadAction) : t
          );
      return { ...data, page: { ...data.page, items } };
    });
    if (DEMO_MODE) {
      if (isLabelAction) {
        for (const id of threadIds) applyMockLabel(id, labelId ?? '', action === 'label');
      } else {
        mockBulkAction(threadIds, action);
      }
    }

    // Labels aren't reflected in the optimistic list patch above (bulk label
    // affects filtering/visibility in ways the cache can't fake), so success
    // is surfaced by invalidating the affected list + detail queries instead.
    function invalidateLabelCaches() {
      void queryClient.invalidateQueries({ queryKey: ['threads'] });
      for (const id of threadIds) void queryClient.invalidateQueries({ queryKey: ['thread', id] });
    }

    // Only push an undo entry for the ids that actually succeeded — never for
    // label/unlabel (no inverse tracked per-id here, matching prior behavior).
    function pushBulkUndo(succeededIds: string[]) {
      if (isLabelAction || succeededIds.length === 0) return;
      const inverse = ACTION_INVERSE[action as ThreadAction];
      if (!inverse) return;
      mailUndo.push({
        label: `${ACTION_UNDO_LABEL[action as ThreadAction] ?? action} ${succeededIds.length} conversations`,
        undo: async () => {
          try {
            const res = await getApiClient().bulkThreadAction({
              threadIds: succeededIds,
              action: inverse,
            });
            if (res.failedIds.length > 0) throw new Error('Bulk undo partially failed');
          } catch (err) {
            if (!DEMO_MODE) throw err;
            mockBulkAction(succeededIds, inverse);
          }
          void queryClient.invalidateQueries({ queryKey: ['threads'] });
        },
      });
    }

    try {
      const res = await getApiClient().bulkThreadAction({ threadIds, action, labelId });
      if (res.failedIds.length > 0) {
        restoreFailedIds(previousLists, res.failedIds);
        toast.error(
          `${res.failedIds.length} conversation${res.failedIds.length === 1 ? '' : 's'} failed to update.`
        );
      }
      if (isLabelAction) invalidateLabelCaches();
      const succeededIds = threadIds.filter((id) => !res.failedIds.includes(id));
      pushBulkUndo(succeededIds);
      return { failedCount: res.failedIds.length };
    } catch {
      if (DEMO_MODE) {
        // Mock fallback already applied optimistically above — every id
        // "succeeded" locally, so undo should target all of them.
        if (isLabelAction) invalidateLabelCaches();
        pushBulkUndo(threadIds);
        return { failedCount: 0 };
      }
      for (const [key, data] of previousLists) queryClient.setQueryData(key, data);
      toast.error('Could not update the selected conversations.');
      return { failedCount: threadIds.length };
    }
  }

  async function unsnooze(threadId: string): Promise<boolean> {
    return runOptimistic(
      threadId,
      (t) => ({ ...t, snoozedUntil: null }),
      false,
      () => getApiClient().unsnoozeThread(threadId),
      () => mockUnsnoozeThread(threadId),
      'Could not cancel the snooze.'
    );
  }

  async function setLabel(
    threadId: string,
    labelId: string,
    add: boolean,
    opts?: { undoable?: boolean }
  ): Promise<boolean> {
    const ok = await runOptimistic(
      threadId,
      (t) => ({
        ...t,
        labelIds: add
          ? [...t.labelIds.filter((id) => id !== labelId), labelId]
          : t.labelIds.filter((id) => id !== labelId),
      }),
      false,
      () => getApiClient().setThreadLabel(threadId, labelId, add),
      () => applyMockLabel(threadId, labelId, add),
      'Could not update the label.'
    );
    if (ok && opts?.undoable !== false) {
      mailUndo.push({
        label: add ? 'Label' : 'Remove label',
        undo: async () => {
          // {undoable:false}: this closure's own setLabel call must not push a
          // fresh undo entry, or Z would toggle the label forever instead of
          // draining the stack (only the entry currently being undone should
          // ever be popped).
          const undone = await setLabel(threadId, labelId, !add, { undoable: false });
          if (!undone) throw new Error('Could not undo label change');
        },
      });
    }
    return ok;
  }

  async function unsubscribe(threadId: string): Promise<UnsubscribeResult> {
    try {
      return await getApiClient().unsubscribeThread(threadId);
    } catch (err) {
      if (DEMO_MODE) return mockUnsubscribe(threadId);
      throw err;
    }
  }

  async function getMeToZero(olderThanIso: string): Promise<number> {
    try {
      const res = await getApiClient().archiveOlderThan(olderThanIso);
      void queryClient.invalidateQueries({ queryKey: ['threads'] });
      return res.archivedCount;
    } catch (err) {
      if (DEMO_MODE) {
        const count = mockArchiveOlderThan(olderThanIso);
        void queryClient.invalidateQueries({ queryKey: ['threads'] });
        return count;
      }
      throw err;
    }
  }

  async function undoLast(): Promise<boolean> {
    const entry = mailUndo.pop();
    if (!entry) return false;
    try {
      await entry.undo();
      return true;
    } catch {
      toast.error(`Could not undo: ${entry.label}.`);
      return false;
    }
  }

  return {
    act,
    snooze,
    remind,
    markOpened,
    bulkAct,
    unsnooze,
    setLabel,
    unsubscribe,
    getMeToZero,
    undoLast,
  };
}

// ---------------------------------------------------------------------------
// Emoji reactions (M2.5) — honesty policy: chips render from real data +
// mutation RESPONSES only. The optimistic patch below never claims
// delivery: "sent" ahead of the server's answer, and a failed request
// reverts it rather than leaving a fabricated chip behind.
// ---------------------------------------------------------------------------

function upsertReaction(m: Message, reaction: Reaction): Message {
  return { ...m, reactions: [...m.reactions.filter((r) => r.emoji !== reaction.emoji), reaction] };
}

function dropReaction(m: Message, emoji: string): Message {
  return { ...m, reactions: m.reactions.filter((r) => r.emoji !== emoji) };
}

/**
 * Undoes a just-sent tiny reply. On success, `onUndone` flips the reaction
 * chip's cached `delivery` back to 'local' — the message is back in drafts,
 * so the chip claiming it was "sent" would now be a lie (M2.5 review fix,
 * MINOR f).
 */
async function undoTinyReply(draftId: string, onUndone: () => void): Promise<void> {
  try {
    await getApiClient().unsendDraft(draftId);
    toast.success('Send undone — the message is back in your drafts.');
    onUndone();
  } catch (err) {
    if (err instanceof ApiRequestError && err.status === 409) {
      toast.error('Too late — that message already went out.');
    } else {
      toast.error('Could not undo the send.');
    }
  }
}

/**
 * Surfaces the undo-capable toast the moment a reaction's response reports a
 * real tiny-reply send — draftId is only ever non-null when the backend's
 * OBSERVED delivery was "sent" (see mail.go ReactToMessage), so this never
 * fires on a merely-requested-but-undelivered reply. `onUndone` is forwarded
 * to undoTinyReply to reconcile the chip if the undo succeeds.
 */
function notifyTinyReply(draftId: string | null, onUndone: () => void): void {
  if (!draftId) return;
  toast.success('Sent a tiny reply', {
    action: { label: 'Undo', onClick: () => void undoTinyReply(draftId, onUndone) },
  });
}

export function useReactToMessage() {
  const queryClient = useQueryClient();

  function patchMessage(threadId: string, messageId: string, patch: (m: Message) => Message) {
    queryClient.setQueryData<ThreadDetailResult | null | undefined>(['thread', threadId], (data) =>
      data ? { ...data, messages: data.messages.map((m) => (m.id === messageId ? patch(m) : m)) } : data
    );
  }

  /**
   * Adds (or replaces) a reaction on a message. Optimistically shows a
   * `delivery: 'local'` chip immediately — never `'sent'`, since whether a
   * tiny reply actually went out is the server's call, not a client guess —
   * then reconciles with the real ReactionResult once it resolves. Only the
   * resolved response's `draftId` (which the backend only sets when delivery
   * really is "sent") triggers the undo-capable toast.
   */
  async function react(
    threadId: string,
    messageId: string,
    emoji: string,
    sendReply: boolean
  ): Promise<void> {
    const previous = queryClient.getQueryData<ThreadDetailResult | null | undefined>([
      'thread',
      threadId,
    ]);
    const pending: Reaction = {
      id: `pending:${messageId}:${emoji}`,
      messageId,
      emoji,
      delivery: 'local',
      createdAt: new Date().toISOString(),
    };
    patchMessage(threadId, messageId, (m) => upsertReaction(m, pending));
    if (DEMO_MODE) {
      const result = mockReactToMessage(messageId, emoji, sendReply);
      patchMessage(threadId, messageId, (m) => upsertReaction(m, result.reaction));
      notifyTinyReply(result.draftId, () =>
        patchMessage(threadId, messageId, (m) => upsertReaction(m, { ...result.reaction, delivery: 'local' }))
      );
    }
    try {
      const result = await getApiClient().reactToMessage(messageId, emoji, sendReply);
      patchMessage(threadId, messageId, (m) => upsertReaction(m, result.reaction));
      notifyTinyReply(result.draftId, () =>
        patchMessage(threadId, messageId, (m) => upsertReaction(m, { ...result.reaction, delivery: 'local' }))
      );
    } catch {
      if (DEMO_MODE) return;
      queryClient.setQueryData(['thread', threadId], previous);
      toast.error('Could not add the reaction.');
    }
  }

  /** Removes a reaction; reverts the optimistic removal on a real failure. */
  async function removeReaction(threadId: string, messageId: string, emoji: string): Promise<void> {
    const previous = queryClient.getQueryData<ThreadDetailResult | null | undefined>([
      'thread',
      threadId,
    ]);
    patchMessage(threadId, messageId, (m) => dropReaction(m, emoji));
    if (DEMO_MODE) mockRemoveReaction(messageId, emoji);
    try {
      await getApiClient().removeReaction(messageId, emoji);
    } catch {
      if (DEMO_MODE) return;
      queryClient.setQueryData(['thread', threadId], previous);
      toast.error('Could not remove the reaction.');
    }
  }

  return { react, removeReaction };
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

/**
 * Maps an AI call failure to a friendly, user-facing message. 429 is the
 * daily per-user AI budget (see backend/internal/service/ai_jobs.go); 502/503
 * mean the upstream model provider is temporarily down. Anything else falls
 * back to a generic "try again" message rather than leaking internals.
 */
export function aiErrorMessage(err: unknown): string {
  if (err instanceof ApiRequestError) {
    if (err.status === 429) return "You've reached your daily AI limit — try again tomorrow.";
    if (err.status === 502 || err.status === 503) {
      return 'AI is temporarily unavailable. Please try again shortly.';
    }
  }
  return 'AI is unavailable right now. Please try again.';
}

/** Cited Q&A over the mailbox (or one thread when threadId is set) — powers the Ask AI sidebar. */
export async function runAiAskCited(
  question: string,
  threadId?: string
): Promise<{ answer: string; model: string; sources: AiSource[]; source: DataSource }> {
  try {
    const res = await getApiClient().aiAskCited({ question, threadId });
    return { ...res, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) return { ...mockAiAskCited(question, threadId), source: 'demo' };
    throw err;
  }
}

/** AI-generated quick-reply suggestions for a thread (used when thread.instantReplies isn't cached yet). */
export async function runInstantReplies(
  threadId: string
): Promise<{ replies: string[]; source: DataSource }> {
  try {
    const res = await getApiClient().getInstantReplies(threadId);
    return { replies: res.replies, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) return { replies: mockInstantReplies(threadId), source: 'demo' };
    throw err;
  }
}

/** Improves/shortens/simplifies/fixes grammar on, or changes the tone of, an existing draft in place. */
export async function runAiEditDraft(
  action: AiEditAction,
  draftId: string,
  tone?: string
): Promise<AiComposeResponse & { source: DataSource }> {
  try {
    const res = await getApiClient().aiEditDraft(action, draftId, tone);
    return { ...res, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) {
      return { ...mockAiCompose({ action, draftId, prompt: '', tone }), source: 'demo' };
    }
    throw err;
  }
}

/** Instant Event AI: proposes a calendar event derived from a thread. */
export async function runProposeEvent(
  threadId: string
): Promise<AiEventProposal & { source: DataSource }> {
  try {
    const res = await getApiClient().proposeEvent(threadId);
    return { ...res, source: 'api' };
  } catch (err) {
    if (DEMO_MODE) return { ...mockProposeEvent(threadId), source: 'demo' };
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Attachments (M2.5) — quick access + inline preview (Task 16)
// ---------------------------------------------------------------------------

export interface AttachmentSearchParams {
  q?: string;
  contact?: string;
  threadId?: string;
}

export interface AttachmentSearchPage {
  page: Page<AttachmentHit>;
  source: DataSource;
}

const ATTACHMENT_PAGE_LIMIT = 30;

/**
 * Cursor-paginated attachment search (GET /v1/mail/attachments) as an infinite
 * query — `fetchNextPage` walks the keyset cursor the backend returns. Demo
 * fallback only kicks in (DEMO_MODE) when the real request fails, same as
 * every other hook in this module.
 */
export function useAttachmentSearch(params: AttachmentSearchParams) {
  return useInfiniteQuery({
    queryKey: ['attachments', params.q ?? '', params.contact ?? '', params.threadId ?? ''],
    queryFn: async ({ pageParam }: { pageParam?: string }): Promise<AttachmentSearchPage> => {
      try {
        const page = await getApiClient().searchAttachments({
          q: params.q || undefined,
          contact: params.contact || undefined,
          threadId: params.threadId || undefined,
          cursor: pageParam || undefined,
          limit: ATTACHMENT_PAGE_LIMIT,
        });
        return { page, source: 'api' };
      } catch (err) {
        if (DEMO_MODE) {
          return {
            page: searchMockAttachments(params, pageParam, ATTACHMENT_PAGE_LIMIT),
            source: 'demo',
          };
        }
        throw err;
      }
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.page.nextCursor ?? undefined,
  });
}

/**
 * Parses filename="..." out of a Content-Disposition header value (RFC 6266
 * quoted-string form — matches escapeQuotedString on the backend). Exported
 * for direct unit testing (see use-mail-attachments.test.ts).
 */
export function parseContentDispositionFilename(header: string | null): string | null {
  if (!header) return null;
  const match = header.match(/filename="((?:[^"\\]|\\.)*)"/);
  if (!match) return null;
  return match[1]!.replace(/\\(.)/g, '$1');
}

export interface AttachmentBlobResult {
  blobUrl: string;
  filename: string;
  mimeType: string;
}

/**
 * Fetches an attachment's real bytes (authed GET of attachmentContentPath)
 * and wraps them as an object URL for inline preview or download. Outside
 * demo mode this always hits the real API; DEMO_MODE only kicks in as a
 * fallback when the fetch fails, mirroring every other data-layer function
 * here. Callers must revoke the returned blobUrl once done with it.
 */
export async function fetchAttachmentBlob(attachmentId: string): Promise<AttachmentBlobResult> {
  try {
    const token = await getAccessToken();
    const res = await fetch(`${env.apiUrl}${getApiClient().attachmentContentPath(attachmentId)}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    if (!res.ok) {
      throw new ApiRequestError(res.status, 'unknown', `Request failed with status ${res.status}`);
    }
    const blob = await res.blob();
    const filename =
      parseContentDispositionFilename(res.headers.get('Content-Disposition')) ?? 'attachment';
    const mimeType = res.headers.get('Content-Type') || blob.type || 'application/octet-stream';
    return { blobUrl: URL.createObjectURL(blob), filename, mimeType };
  } catch (err) {
    if (DEMO_MODE) {
      const mock = getMockAttachmentBlob(attachmentId);
      if (mock) {
        return {
          blobUrl: URL.createObjectURL(mock.blob),
          filename: mock.filename,
          mimeType: mock.mimeType,
        };
      }
    }
    throw err;
  }
}
