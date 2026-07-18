'use client';

import * as React from 'react';
import type { Thread } from '@calendium/shared';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { Skeleton } from '@/components/ui/skeleton';
import { aiErrorMessage, runInstantReplies } from '@/lib/use-mail';

/**
 * Up to three AI reply-suggestion chips shown under the conversation. Prefers
 * the cached `thread.instantReplies` the backend already computed; only calls
 * getInstantReplies() when the thread doesn't have them yet. Clicking (or
 * pressing 1/2/3 while the chip row has focus) opens the composer prefilled
 * with that reply.
 */
export function InstantReplies({
  thread,
  onPick,
}: {
  thread: Thread;
  onPick: (text: string) => void;
}) {
  const [replies, setReplies] = React.useState<string[] | null>(thread.instantReplies ?? null);
  const [loading, setLoading] = React.useState(false);
  const [failed, setFailed] = React.useState(false);

  React.useEffect(() => {
    setFailed(false);
    if (thread.instantReplies && thread.instantReplies.length > 0) {
      setReplies(thread.instantReplies);
      return;
    }
    setReplies(null);
    let cancelled = false;
    setLoading(true);
    runInstantReplies(thread.id)
      .then((res) => {
        if (!cancelled) setReplies(res.replies);
      })
      .catch((err) => {
        if (!cancelled) {
          setFailed(true);
          toast.error(aiErrorMessage(err));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // Re-run only when the thread identity changes — thread.instantReplies is
    // read once above; refetching on every thread object identity change
    // (e.g. an unrelated optimistic patch) would refire this network call.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [thread.id]);

  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (!replies) return;
    const index = ['1', '2', '3'].indexOf(event.key);
    if (index !== -1 && replies[index]) {
      event.preventDefault();
      onPick(replies[index]);
    }
  }

  if (loading) {
    return (
      <div className="flex items-center gap-1.5 px-4 pb-2" data-testid="instant-replies-loading">
        <Skeleton className="h-7 w-24 rounded-full" />
        <Skeleton className="h-7 w-28 rounded-full" />
        <Skeleton className="h-7 w-20 rounded-full" />
      </div>
    );
  }

  if (failed || !replies || replies.length === 0) return null;

  return (
    // No role/tabIndex here: the 1/2/3 shortcut fires while one of the chip
    // <Button>s below has real keyboard focus and the keydown event bubbles
    // up to this handler, so the row itself doesn't need to be a focus target.
    <div onKeyDown={handleKeyDown} className="flex flex-wrap items-center gap-1.5 px-4 pb-2">
      {replies.slice(0, 3).map((reply, index) => (
        <Button
          key={reply}
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1.5 rounded-full font-normal"
          onClick={() => onPick(reply)}
        >
          <span className="max-w-64 truncate">{reply}</span>
          <Kbd size="sm" className="opacity-60">
            {index + 1}
          </Kbd>
        </Button>
      ))}
    </div>
  );
}
