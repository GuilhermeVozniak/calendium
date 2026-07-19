import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Message, Reaction } from '@calendium/shared';
import { describe, expect, it, vi } from 'vitest';

import { MessageReactions } from '@/components/mail/message-reactions';

function makeMessage(reactions: Reaction[]): Message {
  return {
    id: 'm1',
    threadId: 't1',
    accountId: 'acc1',
    from: { name: 'Daniel Cho', email: 'daniel@northwind.com' },
    to: [{ name: 'You', email: 'me@calendium.app' }],
    cc: [],
    bcc: [],
    subject: 'Subject',
    bodyHtml: '<p>Body</p>',
    bodyText: 'Body',
    attachments: [],
    sentAt: new Date().toISOString(),
    isDraft: false,
    openedAt: null,
    reactions,
  };
}

function reaction(emoji: string, delivery: 'local' | 'sent' = 'local'): Reaction {
  return { id: `rx-${emoji}`, messageId: 'm1', emoji, delivery, createdAt: new Date().toISOString() };
}

describe('MessageReactions', () => {
  it('renders the five quick-react emoji in the hover bar', () => {
    render(<MessageReactions message={makeMessage([])} onReact={vi.fn()} onRemove={vi.fn()} />);

    for (const emoji of ['👍', '❤️', '😂', '🎉', '✅']) {
      expect(screen.getByRole('button', { name: `React with ${emoji}` })).toBeInTheDocument();
    }
  });

  it('renders existing reactions as chips from message.reactions', () => {
    render(
      <MessageReactions
        message={makeMessage([reaction('👍')])}
        onReact={vi.fn()}
        onRemove={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Remove 👍 reaction' })).toBeInTheDocument();
    // No fabricated chips for emoji that aren't actually present.
    expect(screen.queryByRole('button', { name: 'Remove ❤️ reaction' })).not.toBeInTheDocument();
  });

  it('shows a count only when more than one reaction shares the emoji', () => {
    render(
      <MessageReactions
        message={makeMessage([reaction('👍'), reaction('👍')])}
        onReact={vi.fn()}
        onRemove={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Remove 👍 reaction' })).toHaveTextContent('2');
  });

  it('clicking a hover-bar emoji calls onReact with that emoji and does not bubble', async () => {
    const user = userEvent.setup();
    const onReact = vi.fn();
    const parentClick = vi.fn();
    render(
      // biome-ignore lint/a11y/useKeyWithClickEvents: test-only propagation probe, not a real UI element.
      // biome-ignore lint/a11y/noStaticElementInteractions: test-only propagation probe, not a real UI element.
      <div onClick={parentClick}>
        <MessageReactions message={makeMessage([])} onReact={onReact} onRemove={vi.fn()} />
      </div>
    );

    await user.click(screen.getByRole('button', { name: 'React with 👍' }));

    expect(onReact).toHaveBeenCalledWith('👍');
    expect(parentClick).not.toHaveBeenCalled();
  });

  it('clicking an existing chip calls onRemove with that emoji and does not bubble', async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    const parentClick = vi.fn();
    render(
      // biome-ignore lint/a11y/useKeyWithClickEvents: test-only propagation probe, not a real UI element.
      // biome-ignore lint/a11y/noStaticElementInteractions: test-only propagation probe, not a real UI element.
      <div onClick={parentClick}>
        <MessageReactions message={makeMessage([reaction('🎉')])} onReact={vi.fn()} onRemove={onRemove} />
      </div>
    );

    await user.click(screen.getByRole('button', { name: 'Remove 🎉 reaction' }));

    expect(onRemove).toHaveBeenCalledWith('🎉');
    expect(parentClick).not.toHaveBeenCalled();
  });
});
