'use client';

import * as React from 'react';
import type { EmailAddress, Message } from '@calendium/shared';
import {
  Archive,
  ArrowLeft,
  BellRing,
  CheckCheck,
  Clock,
  Forward,
  Reply,
  ReplyAll,
  Star,
} from 'lucide-react';
import { toast } from 'sonner';

import { useCompose } from '@/components/app/compose';
import { TimePickerDialog } from '@/components/app/snooze-menu';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Separator } from '@/components/ui/separator';
import { Skeleton } from '@/components/ui/skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { MOCK_ME } from '@/lib/mail-mock';
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
import { useShortcuts } from '@/lib/shortcuts';
import { useMailActions, useThreadDetail } from '@/lib/use-mail';
import { cn } from '@/lib/utils';

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

function isMe(addr: EmailAddress): boolean {
  return addr.email === MOCK_ME.email;
}

function recipientsLine(message: Message): string {
  const names = message.to.map((addr) => (isMe(addr) ? 'me' : firstName(addr)));
  const cc = message.cc.map((addr) => (isMe(addr) ? 'me' : firstName(addr)));
  return `to ${names.join(', ')}${cc.length ? `, cc ${cc.join(', ')}` : ''}`;
}

interface ThreadViewProps {
  threadId: string;
  onClose: () => void;
}

/**
 * Superhuman-style message stack: flat stream, collapsed older messages,
 * quoted history behind a toggle, read-status line on your sent messages,
 * reply / reply-all / forward with r / a / f.
 */
export function ThreadView({ threadId, onClose }: ThreadViewProps) {
  const { data, isLoading } = useThreadDetail(threadId);
  const { act, snooze, remind } = useMailActions();
  const { openCompose } = useCompose();

  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [quotedShown, setQuotedShown] = React.useState<Set<string>>(new Set());
  const [snoozeOpen, setSnoozeOpen] = React.useState(false);
  const [remindOpen, setRemindOpen] = React.useState(false);

  const thread = data?.thread ?? null;
  const messages = React.useMemo(() => data?.messages ?? [], [data]);
  const lastMessage = messages[messages.length - 1] ?? null;

  // Expand the newest message whenever the thread changes.
  React.useEffect(() => {
    setExpanded(new Set(lastMessage ? [lastMessage.id] : []));
    setQuotedShown(new Set());
  }, [threadId, lastMessage?.id]);

  // Opening a thread marks it read (Superhuman behavior).
  React.useEffect(() => {
    if (thread?.unread) void act(thread.id, 'read');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [thread?.id, thread?.unread]);

  const replyRecipients = React.useCallback((): { to: EmailAddress[]; cc: EmailAddress[] } => {
    if (!lastMessage) return { to: [], cc: [] };
    const counterpart = isMe(lastMessage.from) ? lastMessage.to : [lastMessage.from];
    return { to: counterpart, cc: [] };
  }, [lastMessage]);

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
    [thread, lastMessage, openCompose, replyRecipients]
  );

  useShortcuts([
    { keys: 'r', description: 'Reply', handler: () => openReply('reply') },
    { keys: 'a', description: 'Reply all', handler: () => openReply('reply-all') },
    { keys: 'f', description: 'Forward', handler: () => openReply('forward') },
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
    <div className="flex h-full min-w-0 flex-col">
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
                  void act(thread.id, 'archive');
                  toast.success('Archived');
                  onClose();
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
                onClick={() => setSnoozeOpen(true)}
              >
                <Clock className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Snooze <Kbd size="sm">Z</Kbd>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="Set reminder"
                onClick={() => setRemindOpen(true)}
              >
                <BellRing className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Remind <Kbd size="sm">H</Kbd>
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
        </div>
      </div>

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
                  <div className="px-2 py-4">
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
                          {recipientsLine(message)}
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

                    {mine && (
                      <p className="text-muted-foreground mt-3 flex items-center gap-1.5 text-xs">
                        <CheckCheck
                          className={cn('size-3.5', message.openedAt && 'text-emerald-500')}
                        />
                        {message.openedAt
                          ? `Seen ${formatListTime(message.openedAt)}`
                          : 'Delivered · not yet seen'}
                      </p>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </ScrollArea>

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

      <TimePickerDialog
        open={snoozeOpen}
        onOpenChange={setSnoozeOpen}
        title="Snooze until…"
        options={snoozeOptions()}
        onPick={(when) => {
          void snooze(thread.id, when.toISOString());
          toast.success(`Snoozed until ${formatOptionTime(when)}`);
          onClose();
        }}
      />
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
  );
}
