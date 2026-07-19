'use client';

import * as React from 'react';
import type { EmailAddress, Message } from '@calendium/shared';
import {
  Archive,
  ArrowLeft,
  BellRing,
  CalendarPlus,
  CheckCheck,
  Clock,
  Forward,
  Loader2,
  MailX,
  MessagesSquare,
  Reply,
  ReplyAll,
  Send,
  Share2,
  Sparkles,
  Star,
  User,
  X,
} from 'lucide-react';
import { toast } from 'sonner';

import { CommentsPanel } from '@/components/app/comments-panel';
import { useCompose } from '@/components/app/compose';
import { ContactPane } from '@/components/app/contact-pane';
import { SHARE_THREAD_EVENT, ShareDialog } from '@/components/app/share-dialog';
import { TimePickerDialog } from '@/components/app/snooze-menu';
import { TeamActivityChips } from '@/components/app/team-activity-chips';
import { InstantReplies } from '@/components/mail/instant-replies';
import { MessageReactions } from '@/components/mail/message-reactions';
import { ThreadSummary } from '@/components/mail/thread-summary';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Separator } from '@/components/ui/separator';
import { Skeleton } from '@/components/ui/skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import {
  displayName,
  firstName,
  formatFullTime,
  formatListTime,
  formatOptionTime,
  initials,
  reminderOptions,
  snoozeOptions,
} from '@/lib/mail-utils';
import { useSelfEmails } from '@/lib/use-identity';
import { useChords, useShortcuts } from '@/lib/shortcuts';
import { useInstance } from '@/lib/use-instance';
import {
  runAiAsk,
  runAiSummarize,
  useMailActions,
  useReactToMessage,
  useThreadDetail,
} from '@/lib/use-mail';
import { teachShortcut } from '@/lib/shortcut-hints';
import { cn } from '@/lib/utils';

type IsMe = (addr: EmailAddress) => boolean;

/** Splits a plain-text body into visible text and collapsed quoted history. */
function splitQuoted(bodyText: string): { main: string; quoted: string | null } {
  const lines = bodyText.split('\n');
  const index = lines.findIndex(
    (line) => /^On .+ wrote:\s*$/.test(line.trim()) || line.startsWith('>')
  );
  if (index === -1) return { main: bodyText, quoted: null };
  return {
    main: lines.slice(0, index).join('\n').trimEnd(),
    quoted: lines.slice(index).join('\n').trim(),
  };
}

function recipientsLine(message: Message, isMe: IsMe): string {
  const names = message.to.map((addr) => (isMe(addr) ? 'me' : firstName(addr)));
  const cc = message.cc.map((addr) => (isMe(addr) ? 'me' : firstName(addr)));
  return `to ${names.join(', ')}${cc.length ? `, cc ${cc.join(', ')}` : ''}`;
}

// ---------------------------------------------------------------------------
// AI panel — Summarize / Ask (gated on the server's features.ai)
// ---------------------------------------------------------------------------

function ThreadAiPanel({ threadId }: { threadId: string }) {
  const [busy, setBusy] = React.useState(false);
  const [result, setResult] = React.useState<string | null>(null);
  const [question, setQuestion] = React.useState('');

  async function summarize() {
    setBusy(true);
    setResult(null);
    try {
      const res = await runAiSummarize(threadId);
      setResult(res.text);
    } catch {
      toast.error('AI is unavailable right now. Please try again.');
    } finally {
      setBusy(false);
    }
  }

  async function ask() {
    const prompt = question.trim();
    if (!prompt) return;
    setBusy(true);
    setResult(null);
    try {
      const res = await runAiAsk(threadId, prompt);
      setResult(res.text);
    } catch {
      toast.error('AI is unavailable right now. Please try again.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="bg-muted/30 shrink-0 border-b px-4 py-2">
      <div className="mx-auto flex max-w-3xl flex-col gap-2">
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={() => void summarize()}
            disabled={busy}
          >
            {busy ? <Loader2 className="animate-spin" /> : <Sparkles />}
            Summarize
          </Button>
          <div className="relative flex-1">
            <Input
              value={question}
              onChange={(event) => setQuestion(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  void ask();
                }
              }}
              placeholder="Ask AI about this thread…"
              className="h-8 pr-8"
              disabled={busy}
            />
            <button
              type="button"
              onClick={() => void ask()}
              disabled={busy || !question.trim()}
              aria-label="Ask AI"
              className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2 disabled:opacity-40"
            >
              <Send className="size-3.5" />
            </button>
          </div>
        </div>
        {result !== null && (
          <div className="bg-background flex items-start gap-2 rounded-md border p-3 text-sm">
            <Sparkles className="text-muted-foreground mt-0.5 size-3.5 shrink-0" />
            <p className="min-w-0 flex-1 whitespace-pre-wrap">{result}</p>
            <button
              type="button"
              onClick={() => setResult(null)}
              aria-label="Dismiss"
              className="text-muted-foreground hover:text-foreground shrink-0"
            >
              <X className="size-3.5" />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

interface ThreadViewProps {
  threadId: string;
  onClose: () => void;
  onArchive?: () => void;
  /**
   * When provided, the header Snooze button defers to the caller (the mail
   * page opens its own TimePickerDialog, which routes through
   * advancePastRemoved + undo like archive/trash). When absent, ThreadView
   * falls back to its own internal dialog — same contract as `onArchive`.
   */
  onSnooze?: () => void;
  /** "Create event with AI" — opens the caller's ProposeEventDialog for this thread. */
  onProposeEvent?: () => void;
}

/**
 * Superhuman-style message stack: flat stream, collapsed older messages,
 * quoted history behind a toggle, read-status line on your sent messages,
 * reply / reply-all / forward with r / a / f.
 */
export function ThreadView({ threadId, onClose, onArchive, onSnooze, onProposeEvent }: ThreadViewProps) {
  const { data, isLoading } = useThreadDetail(threadId);
  const { act, snooze, remind, markOpened, unsubscribe } = useMailActions();
  const { react: reactToMessage, removeReaction } = useReactToMessage();
  const { openCompose } = useCompose();
  const selfEmails = useSelfEmails();
  const isMe = React.useCallback<IsMe>(
    (addr) => selfEmails.has(addr.email.toLowerCase()),
    [selfEmails]
  );
  const aiEnabled = useInstance().data?.features.ai ?? false;

  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [quotedShown, setQuotedShown] = React.useState<Set<string>>(new Set());
  const [snoozeOpen, setSnoozeOpen] = React.useState(false);
  const [remindOpen, setRemindOpen] = React.useState(false);
  const [contactPaneOpen, setContactPaneOpen] = React.useState(false);
  const [shareOpen, setShareOpen] = React.useState(false);
  const [commentsOpen, setCommentsOpen] = React.useState(false);

  // ⌘K "Share conversation" targets whichever thread view is open.
  React.useEffect(() => {
    const onShare = () => setShareOpen(true);
    window.addEventListener(SHARE_THREAD_EVENT, onShare);
    return () => window.removeEventListener(SHARE_THREAD_EVENT, onShare);
  }, []);

  const thread = data?.thread ?? null;
  const messages = React.useMemo(() => data?.messages ?? [], [data]);
  const lastMessage = messages[messages.length - 1] ?? null;
  const contactEmail = thread?.participants.find((p) => !isMe(p))?.email ?? null;

  // Expand the newest message whenever the thread changes.
  React.useEffect(() => {
    setExpanded(new Set(lastMessage ? [lastMessage.id] : []));
    setQuotedShown(new Set());
  }, [threadId, lastMessage?.id]);

  // Opening a thread records a real open (POST .../open): marks it read
  // server-side and stamps openedAt. Fire once per thread open.
  React.useEffect(() => {
    if (thread?.id) void markOpened(thread.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [thread?.id]);

  const replyRecipients = React.useCallback((): { to: EmailAddress[]; cc: EmailAddress[] } => {
    if (!lastMessage) return { to: [], cc: [] };
    const counterpart = isMe(lastMessage.from) ? lastMessage.to : [lastMessage.from];
    return { to: counterpart, cc: [] };
  }, [lastMessage, isMe]);

  const openReply = React.useCallback(
    (mode: 'reply' | 'reply-all' | 'forward') => {
      if (!thread || !lastMessage) return;
      const base = replyRecipients();
      if (mode === 'forward') {
        openCompose({
          subject: thread.subject.startsWith('Fwd:') ? thread.subject : `Fwd: ${thread.subject}`,
          body: `\n\n---------- Forwarded message ----------\nFrom: ${displayName(lastMessage.from)} <${lastMessage.from.email}>\nDate: ${formatFullTime(lastMessage.sentAt)}\nSubject: ${lastMessage.subject}\n\n${lastMessage.bodyText}`,
          threadId: thread.id,
        });
        return;
      }
      const cc =
        mode === 'reply-all'
          ? [...lastMessage.to, ...lastMessage.cc].filter((a) => !isMe(a) && !base.to.some((t) => t.email === a.email))
          : [];
      openCompose({
        to: base.to,
        cc,
        subject: thread.subject.startsWith('Re:') ? thread.subject : `Re: ${thread.subject}`,
        threadId: thread.id,
      });
    },
    [thread, lastMessage, openCompose, replyRecipients, isMe]
  );

  const handleUnsubscribe = React.useCallback(async () => {
    if (!thread) return;
    try {
      const res = await unsubscribe(thread.id);
      if (res.method === 'link' && res.url) {
        window.open(res.url, '_blank', 'noopener,noreferrer');
        toast.message('Opened the unsubscribe page in a new tab');
      } else {
        toast.success('Unsubscribed — the sender has been asked to stop');
      }
    } catch {
      toast.error('Could not unsubscribe.');
    }
  }, [thread, unsubscribe]);

  useShortcuts([
    { keys: 'r', description: 'Reply', handler: () => openReply('reply') },
    { keys: 'a', description: 'Reply all', handler: () => openReply('reply-all') },
    { keys: 'f', description: 'Forward', handler: () => openReply('forward') },
  ]);

  useChords([
    {
      keys: 'o c',
      description: 'Toggle contact pane',
      enabled: !!contactEmail,
      handler: () => setContactPaneOpen((open) => !open),
    },
  ]);

  if (isLoading) {
    return (
      <div className="flex h-full flex-col gap-4 p-6">
        <Skeleton className="h-6 w-2/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  if (!thread) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-sm">
        Conversation not found.
        <Button variant="outline" size="sm" onClick={onClose}>
          Back to inbox
        </Button>
      </div>
    );
  }

  return (
    <div className="relative flex h-full min-w-0 flex-row">
    <div className="flex h-full min-w-0 flex-1 flex-col">
      {/* Header */}
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2.5">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7" onClick={onClose} aria-label="Back">
              <ArrowLeft className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            Back <Kbd size="sm">Esc</Kbd>
          </TooltipContent>
        </Tooltip>
        <h1 className="min-w-0 flex-1 truncate text-sm font-semibold">{thread.subject}</h1>
        <Badge variant="outline" className="capitalize">
          {thread.split}
        </Badge>
        <div className="flex items-center">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Archive"
                onClick={() => {
                  teachShortcut('archive', 'E', 'Archive');
                  if (onArchive) {
                    onArchive();
                  } else {
                    void act(thread.id, 'archive').then((ok) => {
                      if (ok) toast.success('Archived');
                    });
                    onClose();
                  }
                }}
              >
                <Archive className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Archive <Kbd size="sm">E</Kbd>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Snooze"
                onClick={() => {
                  teachShortcut('snooze', 'H', 'Snooze');
                  if (onSnooze) {
                    onSnooze();
                  } else {
                    setSnoozeOpen(true);
                  }
                }}
              >
                <Clock className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Snooze <Kbd size="sm">H</Kbd>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Set reminder"
                onClick={() => {
                  teachShortcut('remind', '⇧H', 'Set follow-up reminder');
                  setRemindOpen(true);
                }}
              >
                <BellRing className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Remind <KbdGroup size="sm" keys={['⇧', 'H']} />
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Star"
                onClick={() => void act(thread.id, thread.starred ? 'unstar' : 'star')}
              >
                <Star
                  className={cn(
                    'size-4',
                    thread.starred && 'fill-amber-400 text-amber-400'
                  )}
                />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Star <Kbd size="sm">S</Kbd>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Share thread"
                onClick={() => setShareOpen(true)}
              >
                <Share2 className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Share thread</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Toggle team comments"
                aria-pressed={commentsOpen}
                onClick={() => setCommentsOpen((open) => !open)}
              >
                <MessagesSquare className={cn('size-4', commentsOpen && 'text-primary')} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Team comments</TooltipContent>
          </Tooltip>
          {contactEmail && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label="Toggle contact pane"
                  aria-pressed={contactPaneOpen}
                  onClick={() => setContactPaneOpen((open) => !open)}
                >
                  <User className={cn('size-4', contactPaneOpen && 'text-primary')} />
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                Contact <KbdGroup size="sm" keys={['O', 'C']} />
              </TooltipContent>
            </Tooltip>
          )}
          {aiEnabled && onProposeEvent && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label="Create event with AI"
                  onClick={onProposeEvent}
                >
                  <CalendarPlus className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Create event with AI</TooltipContent>
            </Tooltip>
          )}
          {(thread.unsubscribeMailto || thread.unsubscribeUrl || thread.unsubscribeOneClick) && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label="Unsubscribe"
                  onClick={() => void handleUnsubscribe()}
                >
                  <MailX className="size-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Unsubscribe</TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>

      {/* AI (summarize / ask) */}
      {aiEnabled && <ThreadAiPanel threadId={thread.id} />}

      {/* AI thread summary (pre-computed by the backend's AI job queue) */}
      {aiEnabled && <ThreadSummary thread={thread} />}

      {/* Message stack */}
      <ScrollArea className="min-h-0 flex-1">
        <div className="mx-auto flex max-w-3xl flex-col px-4 py-2">
          {messages.map((message, index) => {
            const isExpanded = expanded.has(message.id) || index === messages.length - 1;
            const { main, quoted } = splitQuoted(message.bodyText);
            const mine = isMe(message.from);
            return (
              <div key={message.id} className={cn(index > 0 && 'border-t')}>
                {!isExpanded ? (
                  <button
                    type="button"
                    className="hover:bg-accent/40 flex w-full items-center gap-3 rounded-md px-2 py-2.5 text-left"
                    onClick={() =>
                      setExpanded((prev) => new Set(prev).add(message.id))
                    }
                  >
                    <Avatar className="size-7">
                      <AvatarFallback className="text-[0.625rem]">
                        {initials(message.from)}
                      </AvatarFallback>
                    </Avatar>
                    <span className="w-32 shrink-0 truncate text-sm font-medium">
                      {mine ? 'You' : displayName(message.from)}
                    </span>
                    <span className="text-muted-foreground min-w-0 flex-1 truncate text-sm">
                      {main.replace(/\s+/g, ' ').slice(0, 120)}
                    </span>
                    <span className="text-muted-foreground shrink-0 text-xs">
                      {formatListTime(message.sentAt)}
                    </span>
                  </button>
                ) : (
                  <div className="group px-2 py-4">
                    <div className="flex items-start gap-3">
                      <Avatar className="mt-0.5 size-8">
                        <AvatarFallback className="text-xs">
                          {initials(message.from)}
                        </AvatarFallback>
                      </Avatar>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-baseline justify-between gap-2">
                          <span className="truncate text-sm font-semibold">
                            {mine ? 'You' : displayName(message.from)}
                            <span className="text-muted-foreground ml-2 hidden text-xs font-normal sm:inline">
                              {message.from.email}
                            </span>
                          </span>
                          <span className="text-muted-foreground shrink-0 text-xs">
                            {formatFullTime(message.sentAt)}
                          </span>
                        </div>
                        <p className="text-muted-foreground truncate text-xs">
                          {recipientsLine(message, isMe)}
                        </p>
                      </div>
                    </div>

                    <div className="mt-3 text-sm leading-relaxed whitespace-pre-wrap">{main}</div>

                    {quoted && (
                      <div className="mt-3">
                        <button
                          type="button"
                          className="bg-muted text-muted-foreground hover:bg-accent inline-flex h-5 items-center rounded-full px-2 text-xs"
                          onClick={() =>
                            setQuotedShown((prev) => {
                              const next = new Set(prev);
                              if (next.has(message.id)) next.delete(message.id);
                              else next.add(message.id);
                              return next;
                            })
                          }
                          aria-label="Toggle quoted text"
                        >
                          •••
                        </button>
                        {quotedShown.has(message.id) && (
                          <div className="text-muted-foreground mt-2 border-l-2 pl-3 text-sm whitespace-pre-wrap">
                            {quoted}
                          </div>
                        )}
                      </div>
                    )}

                    {/* Read receipts only when the backend has real openedAt data. */}
                    {mine && message.openedAt && (
                      <p className="text-muted-foreground mt-3 flex items-center gap-1.5 text-xs">
                        <CheckCheck className="size-3.5 text-emerald-500" />
                        Seen {formatListTime(message.openedAt)}
                      </p>
                    )}

                    <MessageReactions
                      message={message}
                      onReact={(emoji) => void reactToMessage(thread.id, message.id, emoji, !mine)}
                      onRemove={(emoji) => void removeReaction(thread.id, message.id, emoji)}
                    />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </ScrollArea>

      {/* Teammate read/reply indicators (M2.7 team read statuses) */}
      <TeamActivityChips threadId={thread.id} />

      {/* AI instant reply chips */}
      {aiEnabled && (
        <InstantReplies
          thread={thread}
          onPick={(text) => {
            const base = replyRecipients();
            openCompose({
              to: base.to,
              cc: base.cc,
              subject: thread.subject.startsWith('Re:') ? thread.subject : `Re: ${thread.subject}`,
              threadId: thread.id,
              body: text,
            });
          }}
        />
      )}

      {/* Reply bar */}
      <div className="flex shrink-0 items-center gap-2 border-t px-4 py-2.5">
        <Button size="sm" variant="secondary" onClick={() => openReply('reply')}>
          <Reply />
          Reply
          <Kbd size="sm">R</Kbd>
        </Button>
        <Button size="sm" variant="ghost" onClick={() => openReply('reply-all')}>
          <ReplyAll />
          Reply all
          <Kbd size="sm">A</Kbd>
        </Button>
        <Button size="sm" variant="ghost" onClick={() => openReply('forward')}>
          <Forward />
          Forward
          <Kbd size="sm">F</Kbd>
        </Button>
        <Separator orientation="vertical" className="mx-1 h-5" />
        <span className="text-muted-foreground hidden text-xs md:block">
          <Kbd size="sm">J</Kbd> / <Kbd size="sm">K</Kbd> next · previous conversation
        </span>
      </div>

      {/* Only rendered when the caller hasn't taken over Snooze (see onSnooze
          above) — the page's own dialog routes through advancePastRemoved
          and undo, so this internal fallback is unused when onSnooze is set. */}
      {!onSnooze && (
        <TimePickerDialog
          open={snoozeOpen}
          onOpenChange={setSnoozeOpen}
          title="Snooze until…"
          options={snoozeOptions()}
          onPick={(when) => {
            onClose();
            void snooze(thread.id, when.toISOString()).then((ok) => {
              if (ok) toast.success(`Snoozed until ${formatOptionTime(when)}`);
            });
          }}
        />
      )}
      <TimePickerDialog
        open={remindOpen}
        onOpenChange={setRemindOpen}
        title="Remind me if no reply by…"
        options={reminderOptions()}
        onPick={(when) => {
          void remind(thread.id, when.toISOString());
          toast.success(`Reminder set for ${formatOptionTime(when)}`);
        }}
      />
    </div>
      {contactPaneOpen && contactEmail && (
        <ContactPane email={contactEmail} onClose={() => setContactPaneOpen(false)} />
      )}
      {commentsOpen && (
        <CommentsPanel threadId={thread.id} onClose={() => setCommentsOpen(false)} />
      )}
      <ShareDialog threadId={thread.id} open={shareOpen} onOpenChange={setShareOpen} />
    </div>
  );
}
