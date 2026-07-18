'use client';

import * as React from 'react';
import type { Message } from '@calendium/shared';

/** Quick-react palette shown on hover, Superhuman-style. */
const REACTION_EMOJIS = ['👍', '❤️', '😂', '🎉', '✅'] as const;

interface MessageReactionsProps {
  message: Message;
  onReact: (emoji: string) => void;
  onRemove: (emoji: string) => void;
}

/**
 * Existing-reaction count chips plus a hover-revealed bar of five quick-react
 * emoji. Chips render strictly from `message.reactions` (the real data /
 * mutation response) — never a fabricated optimistic guess that could survive
 * a failed request. This mailbox is single-user, so every stored reaction is
 * the account owner's own: clicking a chip removes it (toggle-off).
 *
 * Buttons call `stopPropagation` because the expanded message body they live
 * inside sits in a hoverable row alongside other click targets (M2.2 keyboard
 * / click-target safety lesson) — a reaction click must never bubble into an
 * ancestor row action.
 */
export function MessageReactions({ message, onReact, onRemove }: MessageReactionsProps) {
  const counts = React.useMemo(() => {
    const map = new Map<string, number>();
    for (const r of message.reactions) map.set(r.emoji, (map.get(r.emoji) ?? 0) + 1);
    return map;
  }, [message.reactions]);

  return (
    <div className="group/reactions mt-2 flex flex-wrap items-center gap-1.5">
      {[...counts.entries()].map(([emoji, count]) => (
        <button
          key={emoji}
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            onRemove(emoji);
          }}
          aria-label={`Remove ${emoji} reaction`}
          className="bg-accent hover:bg-accent/70 inline-flex h-6 items-center gap-1 rounded-full px-2 text-xs"
        >
          <span>{emoji}</span>
          {count > 1 && <span className="text-muted-foreground">{count}</span>}
        </button>
      ))}
      <div
        data-testid="reaction-hover-bar"
        className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100"
      >
        {REACTION_EMOJIS.map((emoji) => (
          <button
            key={emoji}
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onReact(emoji);
            }}
            aria-label={`React with ${emoji}`}
            className="hover:bg-accent inline-flex size-6 items-center justify-center rounded-full text-sm"
          >
            {emoji}
          </button>
        ))}
      </div>
    </div>
  );
}
