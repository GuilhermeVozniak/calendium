import type { BulkAction, InboxSplit, OpenEvent, OutboxAction, Thread } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { addHours, format, isToday } from 'date-fns';
import { ChevronsUpDown, CloudOff, Loader2, MailOpen, Star } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockAccounts, mockOpens, mockThreads } from '@/lib/mock';
import { isNetworkError, queueOffline, useQueuedCount } from '@/lib/offline';
import { isDemoMode } from '@/lib/server-config';
import {
  ACTION_INVERSE,
  EMPTY_SELECTION,
  clearSelection,
  extendSelection,
  inboxUndo,
  nextAfterRemoval,
  pruneSelection,
  toggleSelected,
  type Selection,
} from '@/lib/triage';
import { errorMessage, toast } from '@/lib/toast';
import { cn } from '@/lib/utils';
import { Badge } from '@/ui/badge';
import { Button } from '@/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/ui/dropdown';
import { Kbd } from '@/ui/kbd';
import { ThreadPane } from '@/views/ThreadPane';

export type MailAction = 'next' | 'prev' | 'archive' | 'star' | 'snooze';

const MAIL_ACTION_EVENT = 'calendium:mail-action';
const FOCUS_THREAD_EVENT = 'calendium:focus-thread';

/** Lets the command palette (or anything else) drive inbox actions. */
export function emitMailAction(action: MailAction) {
  window.dispatchEvent(new CustomEvent<MailAction>(MAIL_ACTION_EVENT, { detail: action }));
}

/** Selects a specific thread once its split is loaded (⌘K search results). */
export function emitFocusThread(threadId: string) {
  window.dispatchEvent(new CustomEvent<string>(FOCUS_THREAD_EVENT, { detail: threadId }));
}

/** localStorage key backing the persisted active-account selection (matches web). */
const ACTIVE_ACCOUNT_STORAGE_KEY = 'calendium.activeAccountId';

function readStoredAccountId(): string | null {
  try {
    return window.localStorage.getItem(ACTIVE_ACCOUNT_STORAGE_KEY);
  } catch {
    return null;
  }
}

/** Display label for the platform modifier in account-switch badges. */
const MOD_LABEL = /Mac/.test(navigator.platform) ? '⌘' : 'Ctrl+';

const SPLIT_LABELS: Record<InboxSplit, string> = {
  important: 'Important',
  vip: 'VIP',
  team: 'Team',
  calendar: 'Calendar',
  news: 'News',
  social: 'Social',
  other: 'Other',
};

function threadTime(isoDate: string): string {
  const d = new Date(isoDate);
  return isToday(d) ? format(d, 'HH:mm') : format(d, 'MMM d');
}

function ThreadRow({
  thread,
  active,
  bulkSelected,
  onClick,
}: {
  thread: Thread;
  active: boolean;
  bulkSelected: boolean;
  onClick: () => void;
}) {
  const sender = thread.participants[0];
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex w-full flex-col gap-0.5 border-b px-3 py-2 text-left transition-colors select-none',
        bulkSelected ? 'bg-primary/10' : active ? 'bg-accent' : 'hover:bg-accent/50'
      )}>
      <div className="flex items-center gap-1.5">
        {bulkSelected && <span className="bg-primary size-1.5 shrink-0 rounded-full" />}
        {thread.unread && <span className="bg-chart-1 size-1.5 shrink-0 rounded-full" />}
        <span className={cn('truncate text-sm', thread.unread ? 'font-semibold' : 'font-medium')}>
          {sender?.name ?? sender?.email ?? 'Unknown'}
        </span>
        {thread.starred && <Star className="fill-chart-4 text-chart-4 size-3 shrink-0" />}
        <span className="text-muted-foreground ml-auto shrink-0 text-[11px] tabular-nums">
          {threadTime(thread.lastMessageAt)}
        </span>
      </div>
      <div className={cn('truncate text-[13px]', thread.unread ? 'font-medium' : '')}>
        {thread.subject}
      </div>
      <div className="text-muted-foreground truncate text-xs">{thread.snippet}</div>
    </button>
  );
}

/** The Recent Opens feed (M2.5): sent messages the recipient has opened, newest first. */
function OpensFeedList({ opens, isLoading }: { opens: OpenEvent[]; isLoading: boolean }) {
  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="text-muted-foreground size-5 animate-spin" />
      </div>
    );
  }
  if (opens.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
        <MailOpen className="text-muted-foreground/50 size-6" />
        <p className="text-sm font-medium">No opens yet</p>
        <p className="text-muted-foreground text-xs">
          You'll see it here when a recipient opens a message you sent.
        </p>
      </div>
    );
  }
  return (
    <ul className="divide-y">
      {opens.map((open) => (
        <li key={open.messageId} className="flex flex-col gap-0.5 px-3 py-2">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm font-medium">{open.subject}</span>
            <span className="text-muted-foreground ml-auto shrink-0 text-[11px] tabular-nums">
              {threadTime(open.openedAt)}
            </span>
          </div>
          <div className="text-muted-foreground truncate text-xs">
            Opened by {open.recipients.map((r) => r.name ?? r.email).join(', ')}
          </div>
        </li>
      ))}
    </ul>
  );
}

export function InboxView({ split }: { split: InboxSplit }) {
  const queryClient = useQueryClient();
  // Real persisted-outbox size — the pill never claims more or less than
  // what is actually queued for replay (honesty policy).
  const queuedOffline = useQueuedCount();
  const [paneView, setPaneView] = useState<'inbox' | 'opens'>('inbox');
  // Connected accounts + active inbox scope for mod+1..9 switching (M2.6
  // task 12). The selection persists in localStorage, matching web.
  const { data: accounts = [] } = useQuery({
    queryKey: ['accounts'],
    queryFn: () =>
      orMock(
        () => api.listAccounts(),
        () => mockAccounts
      ),
  });
  const [activeAccountId, setActiveAccountIdState] = useState<string | null>(readStoredAccountId);
  const setActiveAccountId = useCallback((id: string | null) => {
    setActiveAccountIdState(id);
    try {
      if (id === null) window.localStorage.removeItem(ACTIVE_ACCOUNT_STORAGE_KEY);
      else window.localStorage.setItem(ACTIVE_ACCOUNT_STORAGE_KEY, id);
    } catch {
      // Best-effort persistence; the in-memory selection still applies.
    }
  }, []);
  const activeAccount = accounts.find((a) => a.id === activeAccountId) ?? null;

  // A persisted selection is only honest while that account still exists —
  // once the account list resolves without it, fall back to "all accounts".
  useEffect(() => {
    if (accounts.length === 0 || activeAccountId === null) return;
    if (!accounts.some((a) => a.id === activeAccountId)) setActiveAccountId(null);
  }, [accounts, activeAccountId, setActiveAccountId]);

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['threads', split, activeAccountId],
    queryFn: () =>
      orMock(
        async () =>
          (await api.listThreads({ split, limit: 50, accountId: activeAccountId ?? undefined }))
            .items,
        () => mockThreads(split, activeAccountId ?? undefined)
      ),
  });
  const { data: opens = [], isLoading: opensLoading } = useQuery({
    queryKey: ['opens'],
    enabled: paneView === 'opens',
    queryFn: () => orMock(async () => (await api.listOpens({ limit: 30 })).items, () => mockOpens()),
  });

  // Local working copy so j/k/e/s/h/z/x mutate optimistically.
  const [threads, setThreads] = useState<Thread[]>([]);
  const [cursor, setCursor] = useState(0);
  const [focusId, setFocusId] = useState<string | null>(null);
  const [selection, setSelection] = useState<Selection>(EMPTY_SELECTION);
  useEffect(() => {
    setThreads(data ?? []);
  }, [data]);
  useEffect(() => {
    setCursor(0);
    setSelection(EMPTY_SELECTION);
  }, [split, activeAccountId]);

  // Drop selected ids that fell out of a (possibly refetched) list so a stale
  // selection never outlives the rows it points at. Prune stale ids only;
  // keep valid ones and their anchor/cursor if still valid.
  useEffect(() => {
    setSelection((s) => {
      const idSet = new Set(threads.map((t) => t.id));
      return pruneSelection(s, idSet);
    });
  }, [threads]);

  // ⌘K search can request a specific thread; select it once it's loaded.
  useEffect(() => {
    const onFocus = (e: Event) => setFocusId((e as CustomEvent<string>).detail);
    window.addEventListener(FOCUS_THREAD_EVENT, onFocus);
    return () => window.removeEventListener(FOCUS_THREAD_EVENT, onFocus);
  }, []);
  useEffect(() => {
    if (!focusId || !data) return;
    const idx = data.findIndex((t) => t.id === focusId);
    if (idx >= 0) {
      setCursor(idx);
      setFocusId(null);
    }
  }, [data, focusId]);

  const selected = threads[cursor] ?? null;
  const orderedIds = threads.map((t) => t.id);

  // Opening a thread records real read state server-side (contract item 5).
  useEffect(() => {
    if (!selected?.unread) return;
    const id = selected.id;
    setThreads((prev) => prev.map((t) => (t.id === id ? { ...t, unread: false } : t)));
    if (!isDemoMode()) {
      void api.markThreadOpened(id).catch((e: unknown) => {
        // Offline: durably queue the open so real read state syncs on reconnect.
        if (isNetworkError(e)) void queueOffline({ kind: 'thread_open', threadId: id });
      });
    }
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Persists a mutation and reports whether it actually took effect. A
  // demo-mode "success" is honest without a network round-trip because the
  // local `threads` array (sourced from lib/mock.ts) already carries the
  // optimistic change — that array IS the demo store. Outside demo mode the
  // mutation must actually resolve before an undo entry is pushed or a
  // success toast shown; on failure it reverts the local state and surfaces
  // the error (never a fabricated success).
  //
  // Offline (M2.6): when the failure is network-level and a `queueAction` is
  // given, the action is durably queued instead — the optimistic UI stands
  // (it WILL replay, at-least-once) but commit still returns false so no undo
  // entry or success toast claims the server already applied it. If even
  // queueing fails (storage broken), it falls through to revert + error.
  async function commit(
    apiCall: () => Promise<unknown>,
    revert: () => void,
    errorTitle: string,
    queueAction?: OutboxAction
  ): Promise<boolean> {
    if (isDemoMode()) return true;
    try {
      await apiCall();
      return true;
    } catch (e) {
      if (queueAction && isNetworkError(e) && (await queueOffline(queueAction))) {
        toast({ title: 'Saved offline', description: 'Will sync when you reconnect.' });
        return false;
      }
      revert();
      toast({ title: errorTitle, description: errorMessage(e), variant: 'destructive' });
      return false;
    }
  }

  function deselect(sel: Selection, id: string): Selection {
    if (!sel.ids.has(id)) return sel;
    const ids = new Set(sel.ids);
    ids.delete(id);
    const rangeIds = new Set(sel.rangeIds);
    rangeIds.delete(id);
    return { ...sel, ids, rangeIds };
  }

  async function undo() {
    const entry = inboxUndo.pop();
    if (!entry) {
      toast({ title: 'Nothing to undo' });
      return;
    }
    try {
      await entry.undo();
      toast({ title: 'Undone' });
    } catch (e) {
      toast({
        title: `Could not undo: ${entry.label}`,
        description: errorMessage(e),
        variant: 'destructive',
      });
    }
  }

  function runAction(action: MailAction) {
    const current = threads[cursor];
    switch (action) {
      case 'next':
        setCursor((c) => Math.min(c + 1, Math.max(threads.length - 1, 0)));
        return;
      case 'prev':
        setCursor((c) => Math.max(c - 1, 0));
        return;
      case 'star': {
        if (!current) return;
        const wasStarred = current.starred;
        const nextAction = wasStarred ? 'unstar' : 'star';
        setThreads((prev) =>
          prev.map((t) => (t.id === current.id ? { ...t, starred: !t.starred } : t))
        );
        void commit(
          () => api.actOnThread(current.id, nextAction),
          () =>
            setThreads((prev) =>
              prev.map((t) => (t.id === current.id ? { ...t, starred: wasStarred } : t))
            ),
          'Could not update star',
          { kind: 'thread_action', threadId: current.id, action: nextAction }
        ).then((ok) => {
          if (!ok) return;
          const inverse = ACTION_INVERSE[nextAction];
          if (!inverse) return;
          inboxUndo.push({
            label: nextAction === 'star' ? 'Star' : 'Unstar',
            undo: async () => {
              if (!isDemoMode()) await api.actOnThread(current.id, inverse);
              setThreads((prev) =>
                prev.map((t) => (t.id === current.id ? { ...t, starred: wasStarred } : t))
              );
            },
          });
        });
        return;
      }
      case 'archive': {
        if (selection.ids.size > 0) {
          bulkArchive();
          return;
        }
        if (!current) return;
        archiveThread(current);
        return;
      }
      case 'snooze': {
        if (!current) return;
        snoozeThread(current);
        return;
      }
    }
  }

  // Auto-advance: archive/snooze remove the thread and move the cursor to
  // the id nextAfterRemoval picks, then push the inverse onto inboxUndo and
  // show an Undo-capable toast (contract item 4 — undo entries only for
  // mutations that actually took effect).
  function removeWithAdvance(thread: Thread) {
    const previousThreads = threads;
    const previousCursor = cursor;
    const nextId = nextAfterRemoval(orderedIds, thread.id);
    const remaining = threads.filter((t) => t.id !== thread.id);
    setThreads(remaining);
    const nextIdx = nextId ? remaining.findIndex((t) => t.id === nextId) : -1;
    setCursor(nextIdx >= 0 ? nextIdx : Math.max(remaining.length - 1, 0));
    setSelection((s) => deselect(s, thread.id));
    return { previousThreads, previousCursor };
  }

  function archiveThread(thread: Thread) {
    const { previousThreads, previousCursor } = removeWithAdvance(thread);
    void commit(
      () => api.actOnThread(thread.id, 'archive'),
      () => {
        setThreads(previousThreads);
        setCursor(previousCursor);
      },
      'Could not archive',
      { kind: 'thread_action', threadId: thread.id, action: 'archive' }
    ).then((ok) => {
      if (!ok) return;
      inboxUndo.push({
        label: 'Archive',
        undo: async () => {
          if (!isDemoMode()) await api.actOnThread(thread.id, ACTION_INVERSE.archive!);
          void queryClient.invalidateQueries({ queryKey: ['threads', split] });
          void refetch();
        },
      });
      toast({
        title: 'Archived',
        description: 'Press Z to undo',
        action: { label: 'Undo', onClick: () => void undo() },
      });
    });
  }

  function snoozeThread(thread: Thread) {
    const { previousThreads, previousCursor } = removeWithAdvance(thread);
    const until = addHours(new Date(), 3).toISOString();
    void commit(
      () => api.snoozeThread(thread.id, until),
      () => {
        setThreads(previousThreads);
        setCursor(previousCursor);
      },
      'Could not snooze',
      { kind: 'thread_snooze', threadId: thread.id, until }
    ).then((ok) => {
      if (!ok) return;
      inboxUndo.push({
        label: 'Snooze',
        undo: async () => {
          if (!isDemoMode()) await api.unsnoozeThread(thread.id);
          void queryClient.invalidateQueries({ queryKey: ['threads', split] });
          void refetch();
        },
      });
      toast({
        title: 'Snoozed 3h',
        description: 'Press Z to undo',
        action: { label: 'Undo', onClick: () => void undo() },
      });
    });
  }

  function markThreadRead(thread: Thread) {
    if (!thread.unread) return;
    setThreads((prev) => prev.map((t) => (t.id === thread.id ? { ...t, unread: false } : t)));
    void commit(
      () => api.actOnThread(thread.id, 'read'),
      () =>
        setThreads((prev) => prev.map((t) => (t.id === thread.id ? { ...t, unread: true } : t))),
      'Could not mark read',
      { kind: 'thread_action', threadId: thread.id, action: 'read' }
    ).then((ok) => {
      if (!ok) return;
      inboxUndo.push({
        label: 'Mark read',
        undo: async () => {
          if (!isDemoMode()) await api.actOnThread(thread.id, ACTION_INVERSE.read!);
          setThreads((prev) =>
            prev.map((t) => (t.id === thread.id ? { ...t, unread: true } : t))
          );
        },
      });
      toast({
        title: 'Marked read',
        description: 'Press Z to undo',
        action: { label: 'Undo', onClick: () => void undo() },
      });
    });
  }

  // Bulk archive/read (E / ⇧I with an active selection): calls
  // bulkThreadAction, clears the selection, reconciles any failedIds back
  // into the list (never a fabricated success), and pushes one undo entry
  // covering only the ids that actually succeeded.
  function runBulk(action: Extract<BulkAction, 'archive' | 'read'>) {
    const ids = [...selection.ids];
    if (ids.length === 0) return;
    const previousThreads = threads;
    const removes = action === 'archive';
    const remaining = removes
      ? threads.filter((t) => !selection.ids.has(t.id))
      : threads.map((t) => (selection.ids.has(t.id) ? { ...t, unread: false } : t));
    setThreads(remaining);
    if (removes) setCursor(0);
    setSelection(clearSelection());

    const label = action === 'archive' ? 'Archive' : 'Mark read';
    const successTitle = (n: number) =>
      action === 'archive'
        ? `Archived ${n} conversation${n === 1 ? '' : 's'}`
        : `Marked ${n} conversation${n === 1 ? '' : 's'} read`;

    if (isDemoMode()) {
      inboxUndo.push({
        label: `${label} ${ids.length} conversations`,
        undo: () => void refetch(),
      });
      toast({
        title: successTitle(ids.length),
        description: 'Press Z to undo',
        action: { label: 'Undo', onClick: () => void undo() },
      });
      return;
    }

    void api.bulkThreadAction({ threadIds: ids, action }).then(
      (res) => {
        const failedSet = new Set(res.failedIds);
        const succeeded = ids.filter((id) => !failedSet.has(id));
        if (res.failedIds.length > 0) {
          const previousById = new Map(previousThreads.map((t) => [t.id, t] as const));
          setThreads((prev) => {
            if (removes) {
              const restored = res.failedIds
                .map((id) => previousById.get(id))
                .filter((t): t is Thread => !!t);
              return [...prev, ...restored];
            }
            return prev.map((t) =>
              failedSet.has(t.id) ? (previousById.get(t.id) ?? t) : t
            );
          });
          toast({
            title: `${res.failedIds.length} conversation${res.failedIds.length === 1 ? '' : 's'} failed to update`,
            variant: 'destructive',
          });
        }
        if (succeeded.length === 0) return;
        const inverse = action === 'archive' ? ACTION_INVERSE.archive : ACTION_INVERSE.read;
        inboxUndo.push({
          label: `${label} ${succeeded.length} conversations`,
          undo: async () => {
            if (inverse) {
              const undoRes = await api.bulkThreadAction({ threadIds: succeeded, action: inverse });
              if (undoRes.failedIds.length > 0) throw new Error('Bulk undo partially failed');
            }
            void queryClient.invalidateQueries({ queryKey: ['threads', split] });
            void refetch();
          },
        });
        toast({
          title: successTitle(succeeded.length),
          description: 'Press Z to undo',
          action: { label: 'Undo', onClick: () => void undo() },
        });
      },
      (e) => {
        setThreads(previousThreads);
        toast({
          title: 'Could not update the selected conversations',
          description: errorMessage(e),
          variant: 'destructive',
        });
      }
    );
  }

  function bulkArchive() {
    runBulk('archive');
  }

  function bulkMarkRead() {
    runBulk('read');
  }

  function toggleSelect() {
    if (!selected) return;
    setSelection((s) => toggleSelected(s, selected.id));
  }

  function extendDown() {
    if (threads.length === 0) return;
    const nextIndex = Math.min(cursor + 1, threads.length - 1);
    const nextId = threads[nextIndex]!.id;
    setCursor(nextIndex);
    setSelection((s) =>
      extendSelection(s.ids.size === 0 && selected ? toggleSelected(s, selected.id) : s, orderedIds, nextId)
    );
  }

  function extendUp() {
    if (threads.length === 0) return;
    const prevIndex = Math.max(cursor - 1, 0);
    const prevId = threads[prevIndex]!.id;
    setCursor(prevIndex);
    setSelection((s) =>
      extendSelection(s.ids.size === 0 && selected ? toggleSelected(s, selected.id) : s, orderedIds, prevId)
    );
  }

  // j/k/e/s/h/z/x — the Superhuman keyboard loop, plus shift+j/k range
  // extension and shift+i bulk/single mark-read.
  useEffect(() => {
    const KEYMAP: Record<string, MailAction> = {
      j: 'next',
      k: 'prev',
      e: 'archive',
      s: 'star',
      h: 'snooze',
    };
    const onKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      const inEditable =
        !!target &&
        (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable);
      // mod+1..9 selects the nth account, mod+0 clears to all accounts (M2.6).
      // Handled before the modifier early-return below, with its own
      // editable-target guard so typing digits in an input never switches.
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && /^[0-9]$/.test(e.key)) {
        if (inEditable) return;
        if (e.key === '0') {
          e.preventDefault();
          setActiveAccountId(null);
          return;
        }
        const account = accounts[Number(e.key) - 1];
        if (account) {
          e.preventDefault();
          setActiveAccountId(account.id);
        }
        return;
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (inEditable) return;
      const key = e.key.toLowerCase();
      if (e.shiftKey && key === 'j') {
        e.preventDefault();
        extendDown();
        return;
      }
      if (e.shiftKey && key === 'k') {
        e.preventDefault();
        extendUp();
        return;
      }
      if (e.shiftKey && key === 'i') {
        e.preventDefault();
        if (selection.ids.size > 0) bulkMarkRead();
        else if (selected) markThreadRead(selected);
        return;
      }
      if (key === 'x') {
        e.preventDefault();
        toggleSelect();
        return;
      }
      if (key === 'z') {
        e.preventDefault();
        void undo();
        return;
      }
      if (key === 'escape' && selection.ids.size > 0) {
        e.preventDefault();
        setSelection(clearSelection());
        return;
      }
      const action = KEYMAP[key];
      if (!action) return;
      e.preventDefault();
      runAction(action);
    };
    const onMailAction = (e: Event) => runAction((e as CustomEvent<MailAction>).detail);
    window.addEventListener('keydown', onKeyDown);
    window.addEventListener(MAIL_ACTION_EVENT, onMailAction);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener(MAIL_ACTION_EVENT, onMailAction);
    };
  });

  return (
    <div className="flex h-full min-w-0">
      <section className="relative flex w-[380px] shrink-0 flex-col border-r">
        <header className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
          <h1 className="text-sm font-semibold">
            {paneView === 'opens' ? 'Recent opens' : SPLIT_LABELS[split]}
          </h1>
          <Badge variant="secondary">{paneView === 'opens' ? opens.length : threads.length}</Badge>
          {queuedOffline > 0 && (
            <Badge variant="outline" className="gap-1">
              <CloudOff className="size-3" />
              {queuedOffline} queued
            </Badge>
          )}
          {accounts.length > 0 && paneView === 'inbox' && (
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button variant="ghost" size="sm" className="max-w-44 gap-1 px-2">
                  <span className="truncate text-xs font-normal">
                    {activeAccount ? activeAccount.email : 'All accounts'}
                  </span>
                  <ChevronsUpDown className="size-3 shrink-0 opacity-60" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-64">
                <DropdownMenuLabel>Inbox scope</DropdownMenuLabel>
                <DropdownMenuItem onSelect={() => setActiveAccountId(null)}>
                  <span className="flex-1 truncate">All accounts</span>
                  <Kbd>{`${MOD_LABEL}0`}</Kbd>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                {accounts.map((account, index) => (
                  <DropdownMenuItem key={account.id} onSelect={() => setActiveAccountId(account.id)}>
                    <span className="flex-1 truncate">{account.email}</span>
                    {index < 9 && <Kbd>{`${MOD_LABEL}${index + 1}`}</Kbd>}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            onClick={() => setPaneView((v) => (v === 'opens' ? 'inbox' : 'opens'))}
          >
            <MailOpen className="size-3.5" />
            {paneView === 'opens' ? 'Back to inbox' : 'Opens'}
          </Button>
          {paneView === 'inbox' && (
            <div className="text-muted-foreground flex items-center gap-1 text-[11px]">
              <Kbd>J</Kbd>
              <Kbd>K</Kbd>
              <span>navigate</span>
            </div>
          )}
        </header>
        {paneView === 'inbox' && selection.ids.size > 0 && (
          <div className="bg-accent/60 text-accent-foreground flex h-7 shrink-0 items-center gap-1 border-b px-3 text-[11px]">
            <span className="font-medium">{selection.ids.size} selected</span>
            <span className="text-muted-foreground">
              {' '}
              — <Kbd>E</Kbd> archive · <Kbd>⇧I</Kbd> read · <Kbd>Esc</Kbd> clear
            </span>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto">
          {paneView === 'opens' ? (
            <OpensFeedList opens={opens} isLoading={opensLoading} />
          ) : isLoading ? (
            <div className="flex h-full items-center justify-center">
              <Loader2 className="text-muted-foreground size-5 animate-spin" />
            </div>
          ) : isError ? (
            <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
              <p className="text-sm font-medium">Couldn't load mail</p>
              <p className="text-muted-foreground text-xs">{errorMessage(error)}</p>
              <Button variant="outline" size="sm" onClick={() => void refetch()}>
                Retry
              </Button>
            </div>
          ) : threads.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
              <div
                aria-hidden
                className="h-24 w-40 rounded-xl bg-gradient-to-br from-sky-200 via-indigo-100 to-rose-100 shadow-inner dark:from-sky-950 dark:via-indigo-950 dark:to-rose-950"
              />
              <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                You&apos;re at Inbox Zero
              </p>
              <p className="text-sm font-medium">The desk is clear.</p>
            </div>
          ) : (
            threads.map((thread, i) => (
              <ThreadRow
                key={thread.id}
                thread={thread}
                active={i === cursor}
                bulkSelected={selection.ids.has(thread.id)}
                onClick={() => setCursor(i)}
              />
            ))
          )}
        </div>
        <footer className="text-muted-foreground flex h-8 shrink-0 items-center gap-3 border-t px-3 text-[11px]">
          <span className="inline-flex items-center gap-1">
            <Kbd>X</Kbd> select
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>E</Kbd> archive
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>S</Kbd> star
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>H</Kbd> snooze
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>Z</Kbd> undo
          </span>
        </footer>
      </section>
      <ThreadPane threadId={selected?.id ?? null} onAction={runAction} />
    </div>
  );
}
