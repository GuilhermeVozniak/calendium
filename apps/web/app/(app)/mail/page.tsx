'use client';

import * as React from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import type { Draft, InboxSplit, Thread } from '@calendium/shared';
import { Loader2, Search, Sparkles, Star, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { htmlToText, useCompose } from '@/components/app/compose';
import { TimePickerDialog } from '@/components/app/snooze-menu';
import { ThreadView } from '@/components/app/thread-view';
import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { getApiClient } from '@/lib/api';
import {
  formatListTime,
  formatOptionTime,
  onMailCommand,
  participantsLine,
  reminderOptions,
  snoozeOptions,
  takePendingMailCommand,
  type MailboxView,
  type MailCommand,
} from '@/lib/mail-utils';
import { useSelfEmails } from '@/lib/use-identity';
import { MOD_KEY, useShortcuts } from '@/lib/shortcuts';
import { useDraftActions, useDrafts, useMailActions, useThreadList } from '@/lib/use-mail';
import { cn } from '@/lib/utils';

const SPLITS: { value: InboxSplit; label: string }[] = [
  { value: 'important', label: 'Important' },
  { value: 'vip', label: 'VIP' },
  { value: 'team', label: 'Team' },
  { value: 'calendar', label: 'Calendar' },
  { value: 'news', label: 'News' },
  { value: 'social', label: 'Social' },
  { value: 'other', label: 'Other' },
];

const VIEW_TITLES: Record<MailboxView, string> = {
  starred: 'Starred',
  snoozed: 'Snoozed',
  sent: 'Sent',
  drafts: 'Drafts',
};

function isSplit(value: string | null): value is InboxSplit {
  return SPLITS.some((s) => s.value === value);
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
  const { data, isLoading, isError } = useThreadList({
    split: view ? undefined : split,
    view: isDrafts ? undefined : (view ?? undefined),
    q: deferredQ.trim() || undefined,
    enabled: !isDrafts,
  });
  const threads = React.useMemo(
    () => (isDrafts ? [] : (data?.page.items ?? [])),
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
  const { act, snooze, remind } = useMailActions();
  const [snoozeOpen, setSnoozeOpen] = React.useState(false);
  const [remindOpen, setRemindOpen] = React.useState(false);

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

  const archiveSelected = React.useCallback(() => {
    if (!selectedThread) return;
    const archivedId = selectedThread.id;
    const next = threads[selectedIndex + 1] ?? threads[selectedIndex - 1] ?? null;
    setSelectedId(next?.id ?? null);
    if (openThreadId === archivedId) navigate({ t: next?.id ?? null });
    void act(archivedId, 'archive');
    toast.success('Archived', {
      action: { label: 'Undo', onClick: () => void act(archivedId, 'move_to_inbox') },
    });
  }, [selectedThread, threads, selectedIndex, openThreadId, navigate, act]);

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

  const focusSearch = React.useCallback(() => searchRef.current?.focus(), []);

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
        if (openThreadId) closeThread();
        else if (q) setQ('');
      },
    },
    { keys: 'e', description: 'Archive', handler: archiveSelected },
    { keys: 's', description: 'Star', handler: toggleStar },
    { keys: 'u', description: 'Toggle unread', handler: toggleUnread },
    { keys: 'shift+i', description: 'Mark read', handler: markRead },
    { keys: 'z', description: 'Snooze', handler: () => selectedThread && setSnoozeOpen(true) },
    { keys: 'h', description: 'Follow-up reminder', handler: () => selectedThread && setRemindOpen(true) },
    { keys: '/', description: 'Search', handler: focusSearch },
  ]);

  // --- Command palette bridge ----------------------------------------------
  // Same-route commands arrive as a DOM event; cross-route commands are queued
  // (lib/mail-utils) and consumed here on mount, so neither is dropped by a
  // listener that isn't attached yet.
  const commandHandlers = React.useRef({ archiveSelected, toggleStar, toggleUnread, markRead, focusSearch });
  commandHandlers.current = { archiveSelected, toggleStar, toggleUnread, markRead, focusSearch };

  const runCommand = React.useCallback((command: MailCommand) => {
    const handlers = commandHandlers.current;
    if (command === 'archive') handlers.archiveSelected();
    else if (command === 'star') handlers.toggleStar();
    else if (command === 'unread') handlers.toggleUnread();
    else if (command === 'mark-read') handlers.markRead();
    else if (command === 'search') handlers.focusSearch();
    else if (command === 'snooze') setSnoozeOpen(true);
    else if (command === 'reminder') setRemindOpen(true);
  }, []);

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
    runCommand(pendingCommand);
    setPendingCommand(null);
  }, [pendingCommand, selectedThread, runCommand]);

  const title = view ? VIEW_TITLES[view] : null;

  return (
    <div className="flex h-full min-w-0">
      {/* List pane */}
      <section
        className={cn(
          'flex min-w-0 flex-col',
          openThreadId ? 'hidden w-[24rem] shrink-0 border-r lg:flex' : 'flex-1'
        )}
      >
        {/* Header: splits / view title + search */}
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2">
          {title ? (
            <h1 className="px-1 text-sm font-semibold">{title}</h1>
          ) : (
            <Tabs value={split} onValueChange={(value) => navigate({ split: value as InboxSplit, view: null, t: null })}>
              <TabsList className="h-8">
                {SPLITS.map((s) => (
                  <TabsTrigger key={s.value} value={s.value} className="px-2.5 text-xs">
                    {s.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          )}
          <div className="relative ml-auto min-w-32 flex-1 sm:max-w-56">
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
        <div className="min-h-0 flex-1 overflow-y-auto">
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
                  onSelect={() => setSelectedId(thread.id)}
                  onOpen={() => openThread(thread.id)}
                />
              ))}
            </ul>
          )}
        </div>

        {/* Shortcut hints */}
        <div className="text-muted-foreground hidden shrink-0 items-center gap-4 border-t px-4 py-1.5 text-xs lg:flex">
          <span className="flex items-center gap-1">
            <Kbd size="sm">J</Kbd>
            <Kbd size="sm">K</Kbd> navigate
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">E</Kbd> archive
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">Z</Kbd> snooze
          </span>
          <span className="flex items-center gap-1">
            <Kbd size="sm">C</Kbd> compose
          </span>
          <span className="ml-auto flex items-center gap-1">
            <Kbd size="sm">{MOD_KEY}</Kbd>
            <Kbd size="sm">K</Kbd> commands
          </span>
        </div>
      </section>

      {/* Thread pane */}
      {openThreadId && (
        <section className="min-w-0 flex-1">
          <ThreadView threadId={openThreadId} onClose={closeThread} />
        </section>
      )}

      <TimePickerDialog
        open={snoozeOpen}
        onOpenChange={setSnoozeOpen}
        title="Snooze until…"
        options={snoozeOptions()}
        onPick={(when) => {
          if (!selectedThread) return;
          const id = selectedThread.id;
          if (openThreadId === id) closeThread();
          void snooze(id, when.toISOString());
          toast.success(`Snoozed until ${formatOptionTime(when)}`);
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
  onSelect: () => void;
  onOpen: () => void;
}

const ThreadRow = React.forwardRef<HTMLLIElement, ThreadRowProps>(function ThreadRow(
  { thread, selfEmails, selected, open, compact, onSelect, onOpen },
  ref
) {
  return (
    <li ref={ref}>
      <button
        type="button"
        onClick={onOpen}
        onMouseEnter={onSelect}
        className={cn(
          'relative flex w-full items-center gap-2.5 border-b px-4 py-0 text-left',
          compact ? 'h-14' : 'h-11',
          selected ? 'bg-accent/70' : 'hover:bg-accent/40',
          open && 'bg-accent'
        )}
      >
        {/* Superhuman-style selection accent bar */}
        <span
          className={cn(
            'absolute top-0 left-0 h-full w-[3px]',
            selected ? 'bg-primary' : 'bg-transparent'
          )}
        />
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
            <button
              type="button"
              onClick={() => void open(draft)}
              className="hover:bg-accent/40 flex w-full items-center gap-2.5 py-2.5 pr-12 pl-4 text-left"
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
  let headline = "You're at Inbox Zero";
  let sub = 'Nothing needs your attention here. Enjoy the calm.';
  if (q.trim()) {
    headline = 'No results';
    sub = `Nothing matches “${q.trim()}”.`;
  } else if (view === 'starred') {
    headline = 'No starred conversations';
    sub = 'Press S on any conversation to star it.';
  } else if (view === 'snoozed') {
    headline = 'Nothing snoozed';
    sub = 'Press Z to snooze a conversation until later.';
  } else if (view === 'sent') {
    headline = 'No sent mail yet';
    sub = 'Messages you send will appear here.';
  } else if (view === 'drafts') {
    headline = 'No drafts';
    sub = 'Press C to start writing — drafts autosave.';
  }
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <div className="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-full">
        <Sparkles className="size-5" />
      </div>
      <p className="text-sm font-medium">{headline}</p>
      <p className="text-muted-foreground max-w-xs text-xs text-balance">{sub}</p>
      {!q && !view && (
        <p className="text-muted-foreground mt-2 flex items-center gap-1.5 text-xs">
          <KbdGroup size="sm" keys={['⌘', 'K']} /> for commands
        </p>
      )}
    </div>
  );
}
