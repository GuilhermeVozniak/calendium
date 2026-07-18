import { useQuery } from '@tanstack/react-query';
import { format } from 'date-fns';
import { Archive, Clock, MailOpen, Reply, Star } from 'lucide-react';

import { api, orMock } from '@/lib/api';
import { openCompose } from '@/lib/compose';
import { mockThread } from '@/lib/mock';
import { Badge } from '@/ui/badge';
import { Button } from '@/ui/button';
import { Kbd } from '@/ui/kbd';
import { Tooltip } from '@/ui/tooltip';
import type { MailAction } from '@/views/InboxView';

export function ThreadPane({
  threadId,
  onAction,
}: {
  threadId: string | null;
  onAction: (action: MailAction) => void;
}) {
  const { data } = useQuery({
    queryKey: ['thread', threadId],
    enabled: threadId !== null,
    queryFn: () =>
      orMock(
        () => api.getThread(threadId!),
        () => mockThread(threadId!)
      ),
  });

  if (!threadId || !data) {
    return (
      <div className="flex min-w-0 flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
        <MailOpen className="size-8 text-muted-foreground/50" />
        <p className="text-sm font-medium">Select a conversation</p>
        <p className="flex items-center gap-1 text-xs text-muted-foreground">
          Use <Kbd>J</Kbd> and <Kbd>K</Kbd> to move, <Kbd>⌘K</Kbd> for commands
        </p>
      </div>
    );
  }

  const { thread, messages } = data;

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b px-4">
        <h2 className="min-w-0 truncate text-sm font-semibold">{thread.subject}</h2>
        <Badge variant="outline" className="capitalize">
          {thread.split}
        </Badge>
        <div className="ml-auto flex items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            className="mr-1"
            disabled={messages.length === 0}
            onClick={() =>
              openCompose({ kind: 'reply', thread, message: messages[messages.length - 1]! })
            }
          >
            <Reply /> Reply
          </Button>
          <Tooltip
            label={
              <>
                Archive <Kbd className="bg-primary-foreground/20 text-primary-foreground">E</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Archive" onClick={() => onAction('archive')}>
              <Archive />
            </Button>
          </Tooltip>
          <Tooltip
            label={
              <>
                Star <Kbd className="bg-primary-foreground/20 text-primary-foreground">S</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Star" onClick={() => onAction('star')}>
              <Star className={thread.starred ? 'fill-chart-4 text-chart-4' : ''} />
            </Button>
          </Tooltip>
          <Tooltip
            label={
              <>
                Snooze 3h <Kbd className="bg-primary-foreground/20 text-primary-foreground">H</Kbd>
              </>
            }
          >
            <Button variant="ghost" size="icon" aria-label="Snooze" onClick={() => onAction('snooze')}>
              <Clock />
            </Button>
          </Tooltip>
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-2xl flex-col gap-3 p-4">
          {messages.map((message) => (
            <article key={message.id} className="rounded-lg border bg-card p-4 shadow-sm">
              <div className="mb-2 flex items-baseline gap-2">
                <span className="text-sm font-medium">
                  {message.from.name ?? message.from.email}
                </span>
                <span className="truncate text-xs text-muted-foreground">{message.from.email}</span>
                <span className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">
                  {format(new Date(message.sentAt), 'MMM d, HH:mm')}
                </span>
              </div>
              <div className="whitespace-pre-wrap text-sm leading-relaxed">{message.bodyText}</div>
            </article>
          ))}
        </div>
      </div>
    </div>
  );
}
