'use client';

import { Sparkles } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

/**
 * "AI draft" indicator for `aiGenerated` drafts (auto-reply / auto-draft),
 * shown in the drafts list and the composer header. `onDiscard` /
 * `onEditAndSend` are optional — pass them where the affordance makes sense
 * (the drafts list row); omit them for a plain badge (the composer header,
 * where you're already editing).
 */
export function AiDraftBadge({
  onDiscard,
  onEditAndSend,
}: {
  onDiscard?: () => void;
  onEditAndSend?: () => void;
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <Badge variant="secondary" className="gap-1 font-normal">
        <Sparkles className="size-3" />
        AI draft
      </Badge>
      {onEditAndSend && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-6 px-1.5 text-xs"
          onClick={(event) => {
            event.stopPropagation();
            onEditAndSend();
          }}
        >
          Edit &amp; send
        </Button>
      )}
      {onDiscard && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="text-destructive hover:text-destructive h-6 px-1.5 text-xs"
          onClick={(event) => {
            event.stopPropagation();
            onDiscard();
          }}
        >
          Discard
        </Button>
      )}
    </span>
  );
}
