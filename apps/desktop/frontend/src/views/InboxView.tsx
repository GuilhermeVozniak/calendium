import type { InboxSplit, Thread } from '@calendium/shared';
import { useQuery } from '@tanstack/react-query';
import { addHours, format, isToday } from 'date-fns';
import { Star } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, orMock } from '@/lib/api';
import { mockThreads } from '@/lib/mock';
import { cn } from '@/lib/utils';
import { desktop } from '@/lib/wails';
import { Badge } from '@/ui/badge';
import { Kbd } from '@/ui/kbd';
import { ThreadPane } from '@/views/ThreadPane';

export type MailAction = 'next' | 'prev' | 'archive' | 'star' | 'snooze';

const MAIL_ACTION_EVENT = 'calendium:mail-action';

/** Lets the command palette (or anything else) drive inbox actions. */
export function emitMailAction(action: MailAction) {
  window.dispatchEvent(new CustomEvent<MailAction>(MAIL_ACTION_EVENT, { detail: action }));
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
  const { data } = useQuery({
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
  useEffect(() => {
    setThreads(data ?? []);
  }, [data]);
  useEffect(() => {
    setCursor(0);
  }, [split]);

  const selected = threads[cursor] ?? null;

  // Dock badge reflects unread count (NotifyBadge is a host-side stub today).
  useEffect(() => {
    void desktop.NotifyBadge(threads.filter((t) => t.unread).length);
  }, [threads]);

  // Opening a thread marks it read (write-through, mock-safe).
  useEffect(() => {
    if (!selected || !selected.unread) return;
    const id = selected.id;
    setThreads((prev) => prev.map((t) => (t.id === id ? { ...t, unread: false } : t)));
    void orMock(
      () => api.actOnThread(id, 'read'),
      () => selected
    );
  }, [selected?.id]); // eslint-disable-line react-hooks/exhaustive-deps

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
        void orMock(
          () => api.actOnThread(current.id, current.starred ? 'unstar' : 'star'),
          () => current
        );
        return;
      }
      case 'archive':
      case 'snooze': {
        if (!current) return;
        setThreads((prev) => prev.filter((t) => t.id !== current.id));
        setCursor((c) => Math.max(Math.min(c, threads.length - 2), 0));
        if (action === 'archive') {
          void orMock(
            () => api.actOnThread(current.id, 'archive'),
            () => current
          );
        } else {
          const until = addHours(new Date(), 3).toISOString();
          void orMock(
            () => api.snoozeThread(current.id, until),
            () => current
          );
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
          {threads.length === 0 ? (
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
