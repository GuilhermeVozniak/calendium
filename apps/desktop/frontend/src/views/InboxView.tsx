import type { BulkAction, InboxSplit, Thread } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { addHours, format, isToday } from 'date-fns';
import { Loader2, Star } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockThreads } from '@/lib/mock';
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

export function InboxView({ split }: { split: InboxSplit }) {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['threads', split],
    queryFn: () =>
      orMock(
        async () => (await api.listThreads({ split, limit: 50 })).items,
        () => mockThreads(split)
      ),
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
  }, [split]);

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
    if (!isDemoMode()) void api.markThreadOpened(id).catch(() => {});
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Persists a mutation and reports whether it actually took effect. A
  // demo-mode "success" is honest without a network round-trip because the
  // local `threads` array (sourced from lib/mock.ts) already carries the
  // optimistic change — that array IS the demo store. Outside demo mode the
  // mutation must actually resolve before an undo entry is pushed or a
  // success toast shown; on failure it reverts the local state and surfaces
  // the error (never a fabricated success).
  async function commit(
    apiCall: () => Promise<unknown>,
    revert: () => void,
    errorTitle: string
  ): Promise<boolean> {
    if (isDemoMode()) return true;
    try {
      await apiCall();
      return true;
    } catch (e) {
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
          'Could not update star'
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
      'Could not archive'
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
      'Could not snooze'
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
      'Could not mark read'
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
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      ) {
        return;
      }
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
          <h1 className="text-sm font-semibold">{SPLIT_LABELS[split]}</h1>
          <Badge variant="secondary">{threads.length}</Badge>
          <div className="text-muted-foreground ml-auto flex items-center gap-1 text-[11px]">
            <Kbd>J</Kbd>
            <Kbd>K</Kbd>
            <span>navigate</span>
          </div>
        </header>
        {selection.ids.size > 0 && (
          <div className="bg-accent/60 text-accent-foreground flex h-7 shrink-0 items-center gap-1 border-b px-3 text-[11px]">
            <span className="font-medium">{selection.ids.size} selected</span>
            <span className="text-muted-foreground">
              {' '}
              — <Kbd>E</Kbd> archive · <Kbd>⇧I</Kbd> read · <Kbd>Esc</Kbd> clear
            </span>
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto">
          {isLoading ? (
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
