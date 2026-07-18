import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { AiDraftBadge } from './ai-draft-badge';

describe('AiDraftBadge', () => {
  it('always shows the "AI draft" badge', () => {
    render(<AiDraftBadge />);
    expect(screen.getByText('AI draft')).toBeInTheDocument();
  });

  it('omits Discard/Edit & send affordances when no callbacks are given', () => {
    render(<AiDraftBadge />);
    expect(screen.queryByText('Discard')).not.toBeInTheDocument();
    expect(screen.queryByText('Edit & send')).not.toBeInTheDocument();
  });

  it('calls onDiscard when Discard is clicked, without triggering a parent click', async () => {
    const onDiscard = vi.fn();
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <AiDraftBadge onDiscard={onDiscard} />
      </div>
    );
    await userEvent.click(screen.getByText('Discard'));
    expect(onDiscard).toHaveBeenCalledTimes(1);
    expect(onParentClick).not.toHaveBeenCalled();
  });

  it('calls onEditAndSend when "Edit & send" is clicked', async () => {
    const onEditAndSend = vi.fn();
    render(<AiDraftBadge onEditAndSend={onEditAndSend} />);
    await userEvent.click(screen.getByText('Edit & send'));
    expect(onEditAndSend).toHaveBeenCalledTimes(1);
  });
});
