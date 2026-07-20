'use client';

import * as React from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import type { Draft, InboxSplit, Label, Thread } from '@calendium/shared';
import { EMPTY_SELECTION, clearSelection, extendSelection, nextAfterRemoval, toggleSelected } from '@calendium/shared';
import { Loader2, MailOpen, Search, Sparkles, Star, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { BulkBar } from '@/components/app/bulk-bar';
import { CalendarPeek, readStoredCalendarPeekOpen } from '@/components/app/calendar-peek';
import { htmlToText, useCompose } from '@/components/app/compose';
import { GetMeToZero } from '@/components/app/get-me-to-zero';
import { InboxZero } from '@/components/app/inbox-zero';
import { LabelPicker } from '@/components/app/label-picker';
import { OpensFeed } from '@/components/app/opens-feed';
import { TimePickerDialog } from '@/components/app/snooze-menu';
import { ThreadView } from '@/components/app/thread-view';
import { AiDraftBadge } from '@/components/mail/ai-draft-badge';
import { ProposeEventDialog } from '@/components/mail/propose-event-dialog';
import { Button } from '@/components/ui/button';
import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { getApiClient } from '@/lib/api';
import { sharedLabelIds } from '@/lib/mail-helpers';
import {
  DEFAULT_SPLITS,
  formatListTime,
  formatOptionTime,
  onMailCommand,
  orderSplits,
  participantsLine,
  reminderOptions,
  snoozeOptions,
  takePendingMailCommand,
  type MailboxView,
  type MailCommand,
} from '@/lib/mail-utils';
import { usePrefs } from '@/lib/prefs-data';
import { setThreadDragData } from '@/lib/thread-drag';
import { useSelfEmails } from '@/lib/use-identity';
import { MOD_KEY, useChords, useShortcuts } from '@/lib/shortcuts';
import { useDraftActions, useDrafts, useLabels, useMailActions, useThreadList } from '@/lib/use-mail';
import { useNextPagePrefetch, usePrefetchNeighbors, useThreadHoverPrefetch } from '@/lib/use-prefetch';
import { cn } from '@/lib/utils';

const VIEW_TITLES: Record<MailboxView, string> = {
  starred: 'Starred',
  snoozed: 'Snoozed',
  sent: 'Sent',
  drafts: 'Drafts',
};

function isSplit(value: string | null): value is InboxSplit {
  return DEFAULT_SPLITS.some((s) => s.value === value);
}

function isView(value: string | null): value is MailboxView {
  return value !== null && value in VIEW_TITLES;
}

export default function MailPage() {
  return (
    <React.Suspense fallback={<MailSkeleton />}>
      <MailClient />
    </React.Suspense>
  );
}

function MailSkeleton() {
  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <Skeleton className="h-8 w-96" />
      {Array.from({ length: 10 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  );
}

function MailClient() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const view = isView(searchParams.get('view')) ? (searchParams.get('view') as MailboxView) : null;
  const split = isSplit(searchParams.get('split'))
    ? (searchParams.get('split') as InboxSplit)
    : 'important';
  const openThreadId = searchParams.get('t');

  const [q, setQ] = React.useState('');
  const deferredQ = React.useDeferredValue(q);
  const searchRef = React.useRef<HTMLInputElement>(null);

  const isDrafts = view === 'drafts';
  const selfEmails = useSelfEmails();
  const prefs = usePrefs();
  const splits = React.useMemo(
    () => orderSplits(DEFAULT_SPLITS, prefs.data?.prefs.splitOrder ?? []),
    [prefs.data]
  );
  const { data, isLoading, isError, fetchNextPage, hasNextPage, isFetchingNextPage } =
    useThreadList({
      split: view ? undefined : split,
      view: isDrafts ? undefined : (view ?? undefined),
      q: deferredQ.trim() || undefined,
      enabled: !isDrafts,
    });
  const threads = React.useMemo(
    () => (isDrafts ? [] : (data?.items ?? [])),
    [data, isDrafts]
  );

  // --- Selection -----------------------------------------------------------
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const selectedIndex = threads.findIndex((t) => t.id === selectedId);
  const selectedThread: Thread | null = threads[selectedIndex] ?? null;

  React.useEffect(() => {
    if (threads.length === 0) {
      setSelectedId(null);
    } else if (!threads.some((t) => t.id === selectedId)) {
      setSelectedId(threads[0]!.id);
    }
  }, [threads, selectedId]);

  const rowRefs = React.useRef(new Map<string, HTMLElement>());
  React.useEffect(() => {
    if (selectedId) rowRefs.current.get(selectedId)?.scrollIntoView({ block: 'nearest' });
  }, [selectedId]);

  // --- Preloading (M2.6 Task 7) --------------------------------------------
  // Hover intent + j/k neighbors warm ['thread', id]; the scroll sentinel
  // (10th-from-last row) pulls the next cursor page before the user hits the
  // bottom. All silent, debounced, and skipped while offline (use-prefetch).
  const { onHoverStart, onHoverEnd } = useThreadHoverPrefetch();
  usePrefetchNeighbors(threads, selectedId);

  const sentinelId = threads.length > 0 ? threads[Math.max(threads.length - 10, 0)]!.id : null;
  const [nearEnd, setNearEnd] = React.useState(false);
  React.useEffect(() => {
    setNearEnd(false);
    if (!sentinelId || typeof IntersectionObserver === 'undefined') return;
    const el = rowRefs.current.get(sentinelId);
    if (!el) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) setNearEnd(true);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [sentinelId]);
  useNextPagePrefetch({
    nearEnd,
    hasNextPage: hasNextPage ?? false,
    isFetching: isFetchingNextPage,
    fetchNextPage,
  });

  // --- Bulk range selection --------------------------------------------------
  const [selection, setSelection] = React.useState(EMPTY_SELECTION);
  const orderedIds = React.useMemo(() => threads.map((t) => t.id), [threads]);
  const selectedIds = React.useMemo(
    () => orderedIds.filter((id) => selection.ids.has(id)),
    [orderedIds, selection]
  );
  // Drop selected ids that dropped out of the (possibly refetched) list so a
  // stale selection never outlives the rows it points at.
  React.useEffect(() => {
    setSelection((s) => {
      if (s.ids.size === 0) return s;
      const idSet = new Set(orderedIds);
      if ([...s.ids].every((id) => idSet.has(id))) return s;
      return {
        anchorId: s.anchorId && idSet.has(s.anchorId) ? s.anchorId : null,
        ids: new Set([...s.ids].filter((id) => idSet.has(id))),
        cursor: s.cursor && idSet.has(s.cursor) ? s.cursor : null,
        rangeIds: new Set([...s.rangeIds].filter((id) => idSet.has(id))),
      };
    });
  }, [orderedIds]);

  // --- URL helpers ---------------------------------------------------------
  const navigate = React.useCallback(
    (next: { split?: InboxSplit; view?: MailboxView | null; t?: string | null }) => {
      const params = new URLSearchParams();
      const nextView = next.view === undefined ? view : next.view;
      const nextSplit = next.split ?? split;
      if (nextView) params.set('view', nextView);
      else if (nextSplit !== 'important') params.set('split', nextSplit);
      const nextT = next.t === undefined ? openThreadId : next.t;
      if (nextT) params.set('t', nextT);
      const qs = params.toString();
      router.replace(qs ? `/mail?${qs}` : '/mail', { scroll: false });
    },
    [router, split, view, openThreadId]
  );

  const openThread = React.useCallback(
    (id: string) => {
      setSelectedId(id);
      navigate({ t: id });
    },
    [navigate]
  );
  const closeThread = React.useCallback(() => navigate({ t: null }), [navigate]);

  // --- Actions -------------------------------------------------------------
  const { act, snooze, remind, undoLast, bulkAct, setLabel, unsubscribe } = useMailActions();
  const [snoozeOpen, setSnoozeOpen] = React.useState(false);
  const [remindOpen, setRemindOpen] = React.useState(false);
  const [zeroOpen, setZeroOpen] = React.useState(false);
  const [proposeEventOpen, setProposeEventOpen] = React.useState(false);

  // Calendar peek: a right-side panel showing today's calendar beside the
  // inbox (mod+shift+k / palette). Hydrated from localStorage after mount
  // (SSR-safe, mirrors theme-provider's pattern) rather than read eagerly in
  // useState, since the initial render may run on the server where
  // localStorage isn't available.
  const [peekOpen, setPeekOpen] = React.useState(false);
  React.useEffect(() => setPeekOpen(readStoredCalendarPeekOpen()), []);

  // Recent Opens feed (M2.5, task 15): a right-side panel showing sent
  // messages that have been opened, toggled the same way as CalendarPeek
  // above — a page-local boolean, no cross-route persistence.
  const [opensOpen, setOpensOpen] = React.useState(false);

  const undo = React.useCallback(() => {
    void undoLast().then((did) => {
      if (did) toast.success('Undone');
      else toast.message('Nothing to undo');
    });
  }, [undoLast]);

  const moveSelection = React.useCallback(
    (delta: number) => {
      if (threads.length === 0) return;
      const index = selectedIndex === -1 ? 0 : Math.min(Math.max(selectedIndex + delta, 0), threads.length - 1);
      const next = threads[index]!;
      setSelectedId(next.id);
      if (openThreadId) navigate({ t: next.id });
    },
    [threads, selectedIndex, openThreadId, navigate]
  );

  // --- Auto-advance logic ---
  const advancePastRemoved = React.useCallback(
    (removedId: string) => {
      const nextId = nextAfterRemoval(orderedIds, removedId);
      setSelectedId(nextId);
      if (openThreadId === removedId) navigate({ t: nextId });
    },
    [orderedIds, openThreadId, navigate]
  );

  /**
   * Shared shape behind archive/trash/snooze: advance past the removed
   * thread immediately (product speed bet — navigation is optimistic and not
   * fabricated state), then gate the success toast on the mutation's actual
   * outcome. On failure, runOptimistic (lib/use-mail) already reverts the
   * caches and shows its own error toast — no extra handling here, and no
   * pane restoration (the cache revert puts the thread back in the list).
   */
  const removeWithUndo = React.useCallback(
    (id: string, run: () => Promise<boolean>, message: string) => {
      advancePastRemoved(id);
      void run().then((ok) => {
        if (ok) {
          toast.success(message, {
            action: { label: 'Undo', onClick: () => void undoLast() },
          });
        }
      });
    },
    [advancePastRemoved, undoLast]
  );

  const archiveSelected = React.useCallback(() => {
    if (!selectedThread) return;
    const id = selectedThread.id;
    removeWithUndo(id, () => act(id, 'archive'), 'Archived');
  }, [selectedThread, removeWithUndo, act]);

  const toggleStar = React.useCallback(() => {
    if (!selectedThread) return;
    void act(selectedThread.id, selectedThread.starred ? 'unstar' : 'star');
  }, [selectedThread, act]);

  const toggleUnread = React.useCallback(() => {
    if (!selectedThread) return;
    void act(selectedThread.id, selectedThread.unread ? 'read' : 'unread');
  }, [selectedThread, act]);

  const markRead = React.useCallback(() => {
    if (!selectedThread) return;
    void act(selectedThread.id, 'read');
  }, [selectedThread, act]);

  const bulkArchive = React.useCallback(() => {
    if (selectedIds.length === 0) return;
    const ids = selectedIds;
    setSelection(clearSelection());
    void bulkAct(ids, 'archive').then(({ failedCount }) => {
      const succeeded = ids.length - failedCount;
      if (succeeded === 0) return;
      toast.success(`Archived ${succeeded} conversation${succeeded === 1 ? '' : 's'}`, {
        action: { label: 'Undo', onClick: () => void undoLast() },
      });
    });
  }, [selectedIds, bulkAct, undoLast]);

  const bulkMarkRead = React.useCallback(() => {
    if (selectedIds.length === 0) return;
    const ids = selectedIds;
    setSelection(clearSelection());
    void bulkAct(ids, 'read').then(({ failedCount }) => {
      const succeeded = ids.length - failedCount;
      if (succeeded > 0) toast.success(`Marked ${succeeded} read`);
    });
  }, [selectedIds, bulkAct]);

  const bulkUnsubscribe = React.useCallback(async () => {
    const targets = threads.filter(
      (t) => selection.ids.has(t.id) && (t.unsubscribeMailto || t.unsubscribeUrl)
    );
    if (targets.length === 0) {
      toast.message('No unsubscribe links in the selection');
      return;
    }
    const ids = targets.map((t) => t.id);
    setSelection(clearSelection());

    // Partition settled results by outcome AND by method: only one_click/mailto
    // actually complete server-side, so only those count as "unsubscribed" and
    // get archived. A fulfilled {method:'link', url} did nothing server-side —
    // the single-thread path (thread-view.tsx) opens the url itself, but the
    // bulk gesture can't fan that out to N tabs (popup blockers would eat
    // them), so link-method senders are surfaced distinctly instead.
    const results = await Promise.allSettled(ids.map((id) => unsubscribe(id)));
    const completedIds: string[] = [];
    const manualIds: string[] = [];
    let failedCount = 0;
    results.forEach((result, index) => {
      const id = ids[index]!;
      if (result.status === 'fulfilled') {
        if (result.value.method === 'link') manualIds.push(id);
        else completedIds.push(id);
      } else {
        failedCount++;
      }
    });

    // Archive only the ids that actually completed server-side.
    if (completedIds.length > 0) {
      void bulkAct(completedIds, 'archive');
    }

    if (completedIds.length > 0) {
      toast.success(
        `Unsubscribed from ${completedIds.length} sender${completedIds.length === 1 ? '' : 's'}`,
        { action: { label: 'Undo', onClick: () => void undoLast() } }
      );
    }
    if (manualIds.length > 0) {
      toast.message(
        `${manualIds.length} sender${manualIds.length === 1 ? '' : 's'} need${manualIds.length === 1 ? 's' : ''} manual unsubscribe — open each conversation and use Unsubscribe there`
      );
    }
    if (failedCount > 0) {
      toast.error(`Couldn't unsubscribe from ${failedCount} sender${failedCount === 1 ? '' : 's'}`);
    }
  }, [threads, selection, unsubscribe, bulkAct, undoLast]);

  const focusSearch = React.useCallback(() => searchRef.current?.focus(), []);

  // --- Labels ----------------------------------------------------------------
  const [labelPickerOpen, setLabelPickerOpen] = React.useState(false);
  const labelsQuery = useLabels();
  const activeLabelIds = React.useMemo(() => {
    if (selectedIds.length === 0) {
      return new Set(selectedThread?.labelIds ?? []);
    }
    // Bulk mode: compute intersection of label IDs across all selected threads
    const selectedThreads = selectedIds.map((id) => threads.find((t) => t.id === id)!);
    return sharedLabelIds(selectedThreads);
  }, [selectedIds, selectedThread, threads]);

  const pickLabel = React.useCallback(
    (label: Label, add: boolean) => {
      if (selectedIds.length > 0) {
        const ids = selectedIds;
        setSelection(clearSelection());
        void bulkAct(ids, add ? 'label' : 'unlabel', label.id).then(({ failedCount }) => {
          const succeeded = ids.length - failedCount;
          if (succeeded === 0) return;
          toast.success(
            `${add ? 'Labeled' : 'Unlabeled'} ${succeeded} conversation${succeeded === 1 ? '' : 's'} “${label.name}”`
          );
        });
      } else if (selectedThread) {
        const id = selectedThread.id;
        void setLabel(id, label.id, add).then((ok) => {
          if (!ok) return;
          toast.success(add ? `Labeled “${label.name}”` : `Removed “${label.name}”`);
        });
      }
    },
    [selectedIds, selectedThread, bulkAct, setLabel]
  );

  // --- Keyboard ------------------------------------------------------------
  useShortcuts([
    { keys: 'j', description: 'Next conversation', handler: () => moveSelection(1) },
    { keys: 'arrowdown', handler: () => moveSelection(1) },
    { keys: 'k', description: 'Previous conversation', handler: () => moveSelection(-1) },
    { keys: 'arrowup', handler: () => moveSelection(-1) },
    {
      keys: 'enter',
      description: 'Open conversation',
      handler: () => selectedThread && openThread(selectedThread.id),
    },
    {
      keys: 'escape',
      description: 'Close conversation',
      handler: () => {
        if (selection.ids.size > 0) setSelection(clearSelection());
        else if (openThreadId) closeThread();
        else if (q) setQ('');
      },
    },
    {
      keys: 'x',
      description: 'Select conversation',
      handler: () => selectedThread && setSelection((s) => toggleSelected(s, selectedThread.id)),
    },
    {
      keys: 'shift+j',
      description: 'Extend selection down',
      handler: () => {
        if (threads.length === 0) return;
        const index = selectedIndex === -1 ? 0 : Math.min(selectedIndex + 1, threads.length - 1);
        const next = threads[index]!;
        setSelectedId(next.id);
        setSelection((s) =>
          extendSelection(
            s.ids.size === 0 && selectedThread ? toggleSelected(s, selectedThread.id) : s,
            orderedIds,
            next.id
          )
        );
      },
    },
    {
      keys: 'shift+k',
      description: 'Extend selection up',
      handler: () => {
        if (threads.length === 0) return;
        const index = selectedIndex === -1 ? 0 : Math.max(selectedIndex - 1, 0);
        const prev = threads[index]!;
        setSelectedId(prev.id);
        setSelection((s) =>
          extendSelection(
            s.ids.size === 0 && selectedThread ? toggleSelected(s, selectedThread.id) : s,
            orderedIds,
            prev.id
          )
        );
      },
    },
    {
      keys: 'e',
      description: 'Archive',
      handler: () => (selection.ids.size > 0 ? bulkArchive() : archiveSelected()),
    },
    {
      keys: 's',
      description: 'Star',
      handler: () => {
        if (selection.ids.size > 0) {
          const ids = selectedIds;
          setSelection(clearSelection());
          void bulkAct(ids, 'star').then(({ failedCount }) => {
            const succeeded = ids.length - failedCount;
            if (succeeded > 0) {
              toast.success(`Starred ${succeeded} conversation${succeeded === 1 ? '' : 's'}`);
            }
          });
        } else {
          toggleStar();
        }
      },
    },
    { keys: 'u', description: 'Toggle unread', handler: toggleUnread },
    {
      keys: 'shift+i',
      description: 'Mark read',
      handler: () => (selection.ids.size > 0 ? bulkMarkRead() : markRead()),
    },
    { keys: 'h', description: 'Snooze', handler: () => selectedThread && setSnoozeOpen(true) },
    { keys: 'shift+h', description: 'Follow-up reminder', handler: () => selectedThread && setRemindOpen(true) },
    {
      keys: '#',
      description: 'Trash',
      handler: () => {
        if (!selectedThread) return;
        const id = selectedThread.id;
        removeWithUndo(id, () => act(id, 'trash'), 'Deleted');
      },
    },
    { keys: 'z', description: 'Undo last action', handler: undo },
    {
      keys: 'l',
      description: 'Label',
      handler: () => (selectedThread || selection.ids.size > 0) && setLabelPickerOpen(true),
    },
    { keys: '/', description: 'Search', handler: focusSearch },
    {
      keys: 'mod+shift+k',
      description: 'Toggle calendar peek',
      handler: () => setPeekOpen((o) => !o),
    },
  ]);

  useChords([{ keys: 'g o', description: 'Toggle Recent Opens', handler: () => setOpensOpen((o) => !o) }]);

  // --- Command palette bridge ----------------------------------------------
  // Same-route commands arrive as a DOM event; cross-route commands are queued
  // (lib/mail-utils) and consumed here on mount, so neither is dropped by a
  // listener that isn't attached yet.
  const commandHandlers = React.useRef({
    archiveSelected,
    toggleStar,
    toggleUnread,
    markRead,
    focusSearch,
    undo,
    handleArchive: () => (selection.ids.size > 0 ? bulkArchive() : archiveSelected()),
    handleMarkRead: () => (selection.ids.size > 0 ? bulkMarkRead() : markRead()),
  });
  commandHandlers.current = {
    archiveSelected,
    toggleStar,
    toggleUnread,
    markRead,
    focusSearch,
    undo,
    handleArchive: () => (selection.ids.size > 0 ? bulkArchive() : archiveSelected()),
    handleMarkRead: () => (selection.ids.size > 0 ? bulkMarkRead() : markRead()),
  };

  const runCommand = React.useCallback((command: MailCommand) => {
    const handlers = commandHandlers.current;
    if (command === 'archive') handlers.handleArchive();
    else if (command === 'star') handlers.toggleStar();
    else if (command === 'unread') handlers.toggleUnread();
    else if (command === 'mark-read') handlers.handleMarkRead();
    else if (command === 'search') handlers.focusSearch();
    else if (command === 'snooze') setSnoozeOpen(true);
    else if (command === 'reminder') setRemindOpen(true);
    else if (command === 'undo') handlers.undo();
    else if (command === 'label') setLabelPickerOpen(true);
    else if (command === 'get-me-to-zero') setZeroOpen(true);
    else if (command === 'toggle-calendar-peek') setPeekOpen((o) => !o);
    else if (command === 'propose-event' && openThreadId) setProposeEventOpen(true);
  }, [openThreadId]);

  React.useEffect(() => onMailCommand(runCommand), [runCommand]);

  // A command queued by the palette from another route runs once the list has a
  // selected thread, so selection-based actions have a target.
  const [pendingCommand, setPendingCommand] = React.useState<MailCommand | null>(null);
  React.useEffect(() => {
    setPendingCommand(takePendingMailCommand());
  }, []);
  React.useEffect(() => {
    if (!pendingCommand) return;
    const needsSelection =
      pendingCommand === 'archive' ||
      pendingCommand === 'star' ||
      pendingCommand === 'unread' ||
      pendingCommand === 'mark-read';
    if (needsSelection && !selectedThread) return;
    if (pendingCommand === 'propose-event' && !openThreadId) return;
    runCommand(pendingCommand);
    setPendingCommand(null);
  }, [pendingCommand, selectedThread, openThreadId, runCommand]);

  const title = view ? VIEW_TITLES[view] : null;

  return (
    <div className="flex h-full min-w-0">
      {/* List pane */}
      <section
        className={cn(
          'relative flex min-w-0 flex-col',
          openThreadId ? 'hidden w-[24rem] shrink-0 border-r lg:flex' : 'flex-1'
        )}
      >
        {/* Header: splits / view title + search */}
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2">
          {title ? (
            <h1 className="px-1 text-sm font-semibold">{title}</h1>
          ) : (
            <Tabs value={split} onValueChange={(value) => navigate({ split: value as InboxSplit, view: null, t: null })}>
              <TabsList className="h-8" data-tour="split-inbox">
                {splits.map((s) => (
                  <TabsTrigger key={s.value} value={s.value} className="px-2.5 text-xs">
                    {s.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="ml-auto size-8"
                aria-label="Toggle Recent Opens"
                aria-pressed={opensOpen}
                onClick={() => setOpensOpen((o) => !o)}
              >
                <MailOpen className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Recent opens <KbdGroup size="sm" keys={['G', 'O']} />
            </TooltipContent>
          </Tooltip>
          <div className="relative min-w-32 flex-1 sm:max-w-56">
            <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" />
            <input
              ref={searchRef}
              value={q}
              onChange={(event) => setQ(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  setQ('');
                  event.currentTarget.blur();
                }
              }}
              placeholder="Search"
              className="placeholder:text-muted-foreground bg-muted/50 focus:ring-ring/50 h-8 w-full rounded-md border pr-8 pl-8 text-sm outline-none focus:ring-2"
            />
            <Kbd size="sm" className="absolute top-1/2 right-2 -translate-y-1/2">
              /
            </Kbd>
          </div>
        </div>

        {/* Thread list (or the Drafts pseudo-view) */}
        <div className="min-h-0 flex-1 overflow-y-auto" data-tour="triage">
          {isDrafts ? (
            <DraftsPane />
          ) : isLoading ? (
            <div className="flex flex-col gap-px p-2">
              {Array.from({ length: 12 }).map((_, i) => (
                <Skeleton key={i} className="h-11 w-full" />
              ))}
            </div>
          ) : isError ? (
            <ErrorState />
          ) : threads.length === 0 ? (
            <EmptyState view={view} q={deferredQ} />
          ) : (
            <ul>
              {threads.map((thread) => (
                <ThreadRow
                  key={thread.id}
                  ref={(el) => {
                    if (el) rowRefs.current.set(thread.id, el);
                    else rowRefs.current.delete(thread.id);
                  }}
                  thread={thread}
                  selfEmails={selfEmails}
                  selected={thread.id === selectedId}
                  open={thread.id === openThreadId}
                  compact={!!openThreadId}
                  bulkSelected={selection.ids.has(thread.id)}
                  onSelect={() => setSelectedId(thread.id)}
                  onOpen={() => openThread(thread.id)}
                  onHoverStart={() => onHoverStart(thread.id)}
                  onHoverEnd={onHoverEnd}
                />
              ))}
            </ul>
          )}
        </div>

        <BulkBar
          count={selection.ids.size}
          onArchive={bulkArchive}
          onMarkRead={bulkMarkRead}
          onLabel={() => setLabelPickerOpen(true)}
          onUnsubscribe={() => void bulkUnsubscribe()}
          onClear={() => setSelection(clearSelection())}
        />

        {/* Shortcut hints */}
        <div className="text-muted-foreground hidden shrink-0 items-center gap-4 border-t px-4 py-1.5 text-xs lg:flex">
          <span className="flex items-center gap-1">
            <Kbd size="sm">J</Kbd>
            <Kbd size="sm">K</Kbd> navigate
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">E</Kbd> archive
          </span>
          <span className="flex items-center gap-1" data-tour="snooze">
            <Kbd size="sm">H</Kbd> snooze
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">L</Kbd> label
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">Z</Kbd> undo
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">C</Kbd> compose
          </span>
          <span className="ml-auto flex items-center gap-1" data-tour="command-palette">
            <Kbd size="sm">{MOD_KEY}</Kbd>
            <Kbd size="sm">K</Kbd> commands
          </span>
        </div>
      </section>

      {/* Thread pane */}
      {openThreadId && (
        <section className="min-w-0 flex-1">
          <ThreadView
            threadId={openThreadId}
            onClose={closeThread}
            onArchive={archiveSelected}
            onSnooze={() => setSnoozeOpen(true)}
            onProposeEvent={() => setProposeEventOpen(true)}
          />
        </section>
      )}

      <CalendarPeek open={peekOpen} onOpenChange={setPeekOpen} />
      <OpensFeed open={opensOpen} onOpenChange={setOpensOpen} />
      <ProposeEventDialog
        threadId={openThreadId}
        open={proposeEventOpen}
        onOpenChange={setProposeEventOpen}
      />

      <LabelPicker
        open={labelPickerOpen}
        onOpenChange={setLabelPickerOpen}
        labels={labelsQuery.data?.labels ?? []}
        activeLabelIds={activeLabelIds}
        onPick={pickLabel}
      />
      <TimePickerDialog
        open={snoozeOpen}
        onOpenChange={setSnoozeOpen}
        title="Snooze until…"
        options={snoozeOptions()}
        onPick={(when) => {
          if (!selectedThread) return;
          const id = selectedThread.id;
          removeWithUndo(id, () => snooze(id, when.toISOString()), `Snoozed until ${formatOptionTime(when)}`);
        }}
      />
      <TimePickerDialog
        open={remindOpen}
        onOpenChange={setRemindOpen}
        title="Remind me if no reply by…"
        options={reminderOptions()}
        onPick={(when) => {
          if (!selectedThread) return;
          void remind(selectedThread.id, when.toISOString());
          toast.success(`Reminder set for ${formatOptionTime(when)}`);
        }}
      />
      <GetMeToZero open={zeroOpen} onOpenChange={setZeroOpen} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Row
// ---------------------------------------------------------------------------

interface ThreadRowProps {
  thread: Thread;
  selfEmails: ReadonlySet<string>;
  selected: boolean;
  open: boolean;
  compact: boolean;
  bulkSelected: boolean;
  onSelect: () => void;
  onOpen: () => void;
  onHoverStart: () => void;
  onHoverEnd: () => void;
}

const ThreadRow = React.forwardRef<HTMLLIElement, ThreadRowProps>(function ThreadRow(
  { thread, selfEmails, selected, open, compact, bulkSelected, onSelect, onOpen, onHoverStart, onHoverEnd },
  ref
) {
  return (
    <li ref={ref}>
      <button
        type="button"
        onClick={onOpen}
        // Email-to-event drag (M2.8 Task 18): the row carries its own
        // subject + participants, so a calendar drop target can prefill an
        // event without another fetch. Keyboard users get the same outcome
        // via the thread view's "Propose event" action.
        draggable
        aria-roledescription="Draggable email conversation. Drop on the calendar to create an event."
        onDragStart={(e) =>
          setThreadDragData(e.dataTransfer, {
            threadId: thread.id,
            subject: thread.subject,
            participants: thread.participants,
          })
        }
        onMouseEnter={() => {
          onSelect();
          onHoverStart();
        }}
        onMouseLeave={onHoverEnd}
        className={cn(
          'relative flex w-full items-center gap-2.5 border-b px-4 py-0 text-left',
          compact ? 'h-14' : 'h-11',
          selected ? 'bg-accent/70' : 'hover:bg-accent/40',
          open && 'bg-accent',
          bulkSelected && 'bg-primary/10'
        )}
      >
        {/* Superhuman-style selection accent bar */}
        <span
          className={cn(
            'absolute top-0 left-0 h-full w-[3px]',
            selected ? 'bg-primary' : 'bg-transparent'
          )}
        />
        {bulkSelected && (
          <span className="bg-primary size-2 shrink-0 rounded-full" aria-hidden data-testid="bulk-selected-dot" />
        )}
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            thread.unread ? 'bg-primary' : 'bg-transparent'
          )}
          aria-hidden
        />
        {compact ? (
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="flex items-baseline justify-between gap-2">
              <span className={cn('truncate text-sm', thread.unread ? 'font-semibold' : 'font-medium')}>
                {participantsLine(thread, selfEmails)}
                {thread.messageCount > 1 && (
                  <span className="text-muted-foreground ml-1 text-xs font-normal">
                    {thread.messageCount}
                  </span>
                )}
              </span>
              <span className="text-muted-foreground shrink-0 text-xs">
                {formatListTime(thread.lastMessageAt)}
              </span>
            </span>
            <span className="text-muted-foreground truncate text-xs">
              <span className={cn(thread.unread && 'text-foreground font-medium')}>
                {thread.subject}
              </span>{' '}
              — {thread.snippet}
            </span>
          </span>
        ) : (
          <>
            <span
              className={cn(
                'w-44 shrink-0 truncate text-sm',
                thread.unread ? 'font-semibold' : 'font-medium'
              )}
            >
              {participantsLine(thread, selfEmails)}
              {thread.messageCount > 1 && (
                <span className="text-muted-foreground ml-1 text-xs font-normal">
                  {thread.messageCount}
                </span>
              )}
            </span>
            <span className="min-w-0 flex-1 truncate text-sm">
              <span className={cn(thread.unread ? 'font-medium' : 'text-foreground')}>
                {thread.subject}
              </span>
              <span className="text-muted-foreground"> — {thread.snippet}</span>
            </span>
          </>
        )}
        {thread.starred && (
          <Star className="size-3.5 shrink-0 fill-amber-400 text-amber-400" aria-label="Starred" />
        )}
        {!compact && (
          <span className="text-muted-foreground w-14 shrink-0 text-right text-xs">
            {formatListTime(thread.lastMessageAt)}
          </span>
        )}
      </button>
    </li>
  );
});

// ---------------------------------------------------------------------------
// Drafts pseudo-view
// ---------------------------------------------------------------------------

function draftRecipients(draft: Draft): string {
  const names = draft.to.map((a) => a.name ?? a.email);
  return names.length === 0 ? 'No recipients' : names.join(', ');
}

function DraftsPane() {
  const draftsQuery = useDrafts(true);
  const { remove } = useDraftActions();
  const { openCompose } = useCompose();
  const [openingId, setOpeningId] = React.useState<string | null>(null);
  const drafts = draftsQuery.data?.drafts ?? [];

  async function open(draft: Draft) {
    setOpeningId(draft.id);
    let full = draft;
    try {
      full = await getApiClient().getDraft(draft.id);
    } catch {
      // Fall back to the list copy if the fresh fetch fails.
    } finally {
      setOpeningId(null);
    }
    openCompose({
      accountId: full.accountId,
      draftId: full.id,
      to: full.to,
      cc: full.cc,
      bcc: full.bcc,
      subject: full.subject,
      body: htmlToText(full.bodyHtml),
      threadId: full.threadId,
      aiGenerated: full.aiGenerated,
    });
  }

  if (draftsQuery.isLoading) {
    return (
      <div className="flex flex-col gap-px p-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    );
  }
  if (draftsQuery.isError) return <ErrorState />;
  if (drafts.length === 0) return <EmptyState view="drafts" q="" />;

  return (
    <ul>
      {drafts.map((draft) => {
        const preview = htmlToText(draft.bodyHtml).replace(/\s+/g, ' ').trim();
        return (
          <li key={draft.id} className="group relative border-b">
            {/* A plain div (not a <button>) wraps the row: the real <button> below
                covers only the clickable text content, and the AI badge's own
                action buttons sit beside it as siblings rather than nesting
                inside another <button> (invalid HTML). */}
            <div className="hover:bg-accent/40 flex w-full items-center gap-2 py-2.5 pr-12 pl-4">
              <button
                type="button"
                onClick={() => void open(draft)}
                className="flex min-w-0 flex-1 items-center gap-2.5 text-left"
              >
                <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <span className="flex items-baseline justify-between gap-2">
                    <span className="truncate text-sm font-medium">
                      {draft.subject.trim() || '(no subject)'}
                    </span>
                    <span className="text-muted-foreground shrink-0 text-xs">
                      {formatListTime(draft.updatedAt)}
                    </span>
                  </span>
                  <span className="text-muted-foreground truncate text-xs">
                    <span className="text-foreground/70">{draftRecipients(draft)}</span>
                    {preview && <> — {preview}</>}
                  </span>
                  {draft.lastError && (
                    <span className="text-destructive truncate text-xs">
                      Last send failed: {draft.lastError}
                    </span>
                  )}
                </span>
                {openingId === draft.id && (
                  <Loader2 className="text-muted-foreground size-3.5 shrink-0 animate-spin" />
                )}
              </button>
              {draft.aiGenerated && (
                <AiDraftBadge
                  onDiscard={() => void remove(draft.id)}
                  onEditAndSend={() => void open(draft)}
                />
              )}
            </div>
            <button
              type="button"
              onClick={() => void remove(draft.id)}
              aria-label="Delete draft"
              className="text-muted-foreground hover:text-destructive absolute top-1/2 right-3 -translate-y-1/2 opacity-0 group-hover:opacity-100"
            >
              <Trash2 className="size-4" />
            </button>
          </li>
        );
      })}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// Empty / error states
// ---------------------------------------------------------------------------

function ErrorState() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <div className="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-full">
        <Sparkles className="size-5" />
      </div>
      <p className="text-sm font-medium">Couldn’t load your mail</p>
      <p className="text-muted-foreground max-w-xs text-xs text-balance">
        The Calendium API is unreachable right now. Check your connection and try again.
      </p>
    </div>
  );
}

function EmptyState({ view, q }: { view: MailboxView | null; q: string }) {
  if (!q.trim() && !view) {
    return (
      <div className="flex flex-1 flex-col">
        <InboxZero />
        <p className="text-muted-foreground flex items-center justify-center gap-1.5 pb-6 text-xs">
          <KbdGroup size="sm" keys={["⌘", "K"]} /> for commands
        </p>
      </div>
    );
  }

  let headline = "No results";
  let sub = "Nothing matches.";
  if (q.trim()) {
    headline = "No results";
    sub = `Nothing matches “${q.trim()}”.`;
  } else if (view === "starred") {
    headline = "No starred conversations";
    sub = "Press S on any conversation to star it.";
  } else if (view === "snoozed") {
    headline = "Nothing snoozed";
    sub = "Press H to snooze a conversation until later.";
  } else if (view === "sent") {
    headline = "No sent mail yet";
    sub = "Messages you send will appear here.";
  } else if (view === "drafts") {
    headline = "No drafts";
    sub = "Press C to start writing — drafts autosave.";
  }
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <div className="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-full">
        <Sparkles className="size-5" />
      </div>
      <p className="text-sm font-medium">{headline}</p>
      <p className="text-muted-foreground max-w-xs text-xs text-balance">{sub}</p>
    </div>
  );
}

