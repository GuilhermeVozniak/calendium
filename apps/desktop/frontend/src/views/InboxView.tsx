import type { InboxSplit, Thread } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { addHours, format, isToday } from 'date-fns';
import { Loader2, Star } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockThreads } from '@/lib/mock';
import { isDemoMode } from '@/lib/server-config';
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
  onClick,
}: {
  thread: Thread;
  active: boolean;
  onClick: () => void;
}) {
  const sender = thread.participants[0];
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex w-full select-none flex-col gap-0.5 border-b px-3 py-2 text-left transition-colors',
        active ? 'bg-accent' : 'hover:bg-accent/50'
      )}
    >
      <div className="flex items-center gap-1.5">
        {thread.unread && <span className="size-1.5 shrink-0 rounded-full bg-chart-1" />}
        <span className={cn('truncate text-sm', thread.unread ? 'font-semibold' : 'font-medium')}>
          {sender?.name ?? sender?.email ?? 'Unknown'}
        </span>
        {thread.starred && <Star className="size-3 shrink-0 fill-chart-4 text-chart-4" />}
        <span className="ml-auto shrink-0 text-[11px] tabular-nums text-muted-foreground">
          {threadTime(thread.lastMessageAt)}
        </span>
      </div>
      <div className={cn('truncate text-[13px]', thread.unread ? 'font-medium' : '')}>
        {thread.subject}
      </div>
      <div className="truncate text-xs text-muted-foreground">{thread.snippet}</div>
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

  // Local working copy so j/k/e/s/z mutate optimistically.
  const [threads, setThreads] = useState<Thread[]>([]);
  const [cursor, setCursor] = useState(0);
  const [focusId, setFocusId] = useState<string | null>(null);
  useEffect(() => {
    setThreads(data ?? []);
  }, [data]);
  useEffect(() => {
    setCursor(0);
  }, [split]);

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

  // Opening a thread records real read state server-side (contract item 5).
  useEffect(() => {
    if (!selected || !selected.unread) return;
    const id = selected.id;
    setThreads((prev) => prev.map((t) => (t.id === id ? { ...t, unread: false } : t)));
    if (!isDemoMode()) void api.markThreadOpened(id).catch(() => {});
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Persists a mutation; on failure surfaces a toast and reverts by refetching
  // (honesty policy — never a silent fake-success). No-ops in demo mode.
  function persist(fn: () => Promise<unknown>, title: string) {
    if (isDemoMode()) return;
    void fn().catch((e) => {
      toast({ title, description: errorMessage(e), variant: 'destructive' });
      void queryClient.invalidateQueries({ queryKey: ['threads', split] });
    });
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
        setThreads((prev) =>
          prev.map((t) => (t.id === current.id ? { ...t, starred: !t.starred } : t))
        );
        persist(
          () => api.actOnThread(current.id, current.starred ? 'unstar' : 'star'),
          'Could not update star'
        );
        return;
      }
      case 'archive':
      case 'snooze': {
        if (!current) return;
        setThreads((prev) => prev.filter((t) => t.id !== current.id));
        setCursor((c) => Math.max(Math.min(c, threads.length - 2), 0));
        if (action === 'archive') {
          persist(() => api.actOnThread(current.id, 'archive'), 'Could not archive');
        } else {
          const until = addHours(new Date(), 3).toISOString();
          persist(() => api.snoozeThread(current.id, until), 'Could not snooze');
        }
        return;
      }
    }
  }

  // j/k/e/s/z — the Superhuman keyboard loop.
  useEffect(() => {
    const KEYMAP: Record<string, MailAction> = {
      j: 'next',
      k: 'prev',
      e: 'archive',
      s: 'star',
      z: 'snooze',
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
      const action = KEYMAP[e.key.toLowerCase()];
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
      <section className="flex w-[380px] shrink-0 flex-col border-r">
        <header className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
          <h1 className="text-sm font-semibold">{SPLIT_LABELS[split]}</h1>
          <Badge variant="secondary">{threads.length}</Badge>
          <div className="ml-auto flex items-center gap-1 text-[11px] text-muted-foreground">
            <Kbd>J</Kbd>
            <Kbd>K</Kbd>
            <span>navigate</span>
          </div>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="flex h-full items-center justify-center">
              <Loader2 className="size-5 animate-spin text-muted-foreground" />
            </div>
          ) : isError ? (
            <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center">
              <p className="text-sm font-medium">Couldn't load mail</p>
              <p className="text-xs text-muted-foreground">{errorMessage(error)}</p>
              <Button variant="outline" size="sm" onClick={() => void refetch()}>
                Retry
              </Button>
            </div>
          ) : threads.length === 0 ? (
            <div className="flex h-full flex-col items-center justify-center gap-1 p-6 text-center">
              <p className="text-sm font-medium">Inbox zero</p>
              <p className="text-xs text-muted-foreground">
                Nothing in {SPLIT_LABELS[split]} — enjoy the quiet.
              </p>
            </div>
          ) : (
            threads.map((thread, i) => (
              <ThreadRow
                key={thread.id}
                thread={thread}
                active={i === cursor}
                onClick={() => setCursor(i)}
              />
            ))
          )}
        </div>
        <footer className="flex h-8 shrink-0 items-center gap-3 border-t px-3 text-[11px] text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <Kbd>E</Kbd> archive
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>S</Kbd> star
          </span>
          <span className="inline-flex items-center gap-1">
            <Kbd>Z</Kbd> snooze
          </span>
        </footer>
      </section>
      <ThreadPane threadId={selected?.id ?? null} onAction={runAction} />
    </div>
  );
}
