'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { formatDistanceToNow } from 'date-fns';
import { Loader2, Lock, MailOpen, X } from 'lucide-react';

import type { OpenEvent } from '@calendium/shared';
import { ApiRequestError } from '@calendium/shared';

import { useCheckoutMutation } from '@/components/app/paywall';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Skeleton } from '@/components/ui/skeleton';
import { displayName, initials } from '@/lib/mail-utils';
import { useOpensFeed } from '@/lib/use-mail';

export interface OpensFeedProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function OpenRow({ event, onSelect }: { event: OpenEvent; onSelect: () => void }) {
  const recipient = event.recipients[0];
  const extraCount = Math.max(0, event.recipients.length - 1);
  const recipientLabel = recipient ? displayName(recipient) : 'Someone';
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        className="hover:bg-accent/50 flex w-full items-start gap-3 rounded-md px-3 py-2.5 text-left"
      >
        <Avatar className="mt-0.5 size-8 shrink-0">
          <AvatarFallback className="text-xs">
            {recipient ? initials(recipient) : '?'}
          </AvatarFallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm">
            <span className="font-medium">
              {recipientLabel}
              {extraCount > 0 && ` +${extraCount}`}
            </span>{' '}
            opened &ldquo;{event.subject}&rdquo; ·{' '}
            <span className="text-muted-foreground">
              {formatDistanceToNow(new Date(event.openedAt), { addSuffix: true })}
            </span>
          </p>
        </div>
      </button>
    </li>
  );
}

/**
 * ~320px right-side panel for the mail route (M2.5, task 15): the Recent
 * Opens feed — sent messages a recipient has opened, newest first, paged via
 * the server's keyset cursor (lib/use-mail's `useOpensFeed`). Mirrors
 * CalendarPeek's controlled `open`/`onOpenChange` shape so the mail page can
 * toggle it the same way. Every row rendered here came from a real API page;
 * "Load more" fetches the next cursor page rather than refetching from the
 * top, so load-more never re-renders fabricated state.
 */
export function OpensFeed({ open, onOpenChange }: OpensFeedProps) {
  const router = useRouter();
  const query = useOpensFeed();
  const checkout = useCheckoutMutation();

  const events = React.useMemo(
    () => query.data?.pages.flatMap((page) => page.items) ?? [],
    [query.data]
  );

  const paymentRequired = query.error instanceof ApiRequestError && query.error.status === 402;

  if (!open) return null;

  return (
    <aside
      data-testid="opens-feed"
      className="hidden h-full w-80 shrink-0 flex-col border-l xl:flex"
    >
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <MailOpen className="text-muted-foreground size-4" />
        <h2 className="text-sm font-semibold">Recent opens</h2>
        <Button
          variant="ghost"
          size="icon"
          className="ml-auto size-7"
          onClick={() => onOpenChange(false)}
          aria-label="Close recent opens"
        >
          <X className="size-4" />
        </Button>
      </div>

      {paymentRequired ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
          <div className="bg-background flex size-9 items-center justify-center rounded-md border text-foreground">
            <Lock className="size-4" />
          </div>
          <div>
            <p className="text-sm font-medium">Recent Opens is a paid feature</p>
            <p className="text-muted-foreground text-sm">
              Subscribe to see when your sent messages are opened.
            </p>
          </div>
          <Button size="sm" onClick={() => checkout.mutate()} disabled={checkout.isPending}>
            {checkout.isPending && <Loader2 className="animate-spin" />}
            Subscribe - $50/year
          </Button>
        </div>
      ) : query.isLoading ? (
        <div className="flex flex-col gap-2 p-3">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : query.isError ? (
        <div className="text-muted-foreground flex flex-1 items-center justify-center p-6 text-center text-sm">
          Could not load recent opens.
        </div>
      ) : events.length === 0 ? (
        <div className="text-muted-foreground flex flex-1 items-center justify-center p-6 text-center text-sm">
          No opens yet — read statuses appear as recipients open your mail.
        </div>
      ) : (
        <ScrollArea className="min-h-0 flex-1">
          <ul className="flex flex-col gap-0.5 p-2">
            {events.map((event) => (
              <OpenRow
                key={event.messageId}
                event={event}
                onSelect={() => router.push(`/mail/${event.threadId}`)}
              />
            ))}
          </ul>
          {query.hasNextPage && (
            <div className="p-2">
              <Button
                variant="outline"
                size="sm"
                className="w-full"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {query.isFetchingNextPage && <Loader2 className="animate-spin" />}
                Load more
              </Button>
            </div>
          )}
        </ScrollArea>
      )}
    </aside>
  );
}
