'use client';

import type { Thread } from '@calendium/shared';
import { Sparkles } from 'lucide-react';

import { Skeleton } from '@/components/ui/skeleton';

/**
 * One-line AI thread summary, shown above the message list. `thread.summary`
 * is computed asynchronously by the backend's AI job queue
 * (backend/internal/service/ai_jobs.go) and cached on the thread — this
 * component is purely presentational: it never calls the AI itself, only
 * renders whatever the thread payload already carries. While a summary
 * hasn't been generated yet (or the server has no AI enabled) it shows a
 * subtle skeleton shimmer rather than an empty gap.
 */
export function ThreadSummary({ thread }: { thread: Thread }) {
  if (!thread.summary) {
    return (
      <div className="flex items-center gap-2 px-4 py-2" data-testid="thread-summary-skeleton">
        <Sparkles className="text-muted-foreground/50 size-3.5 shrink-0" />
        <Skeleton className="h-3.5 w-2/3" />
      </div>
    );
  }

  return (
    <div className="text-muted-foreground flex items-start gap-2 px-4 py-2 text-xs">
      <Sparkles className="mt-0.5 size-3.5 shrink-0 opacity-70" />
      <p className="min-w-0 flex-1 leading-relaxed">{thread.summary}</p>
    </div>
  );
}
