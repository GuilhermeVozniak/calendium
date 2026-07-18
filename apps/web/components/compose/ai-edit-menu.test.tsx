import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const runAiEditDraftMock = vi.fn();
const aiErrorMessageMock = vi.fn((..._args: unknown[]) => 'AI is unavailable right now. Please try again.');
vi.mock('@/lib/use-mail', () => ({
  runAiEditDraft: (...args: unknown[]) => runAiEditDraftMock(...args),
  aiErrorMessage: (...args: unknown[]) => aiErrorMessageMock(...args),
}));

const toastError = vi.fn();
const toastInfo = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastError(...args),
    info: (...args: unknown[]) => toastInfo(...args),
  },
}));

import { AiEditMenu, dispatchAiEditCommand } from './ai-edit-menu';

describe('AiEditMenu', () => {
  beforeEach(() => {
    runAiEditDraftMock.mockReset();
    toastError.mockReset();
    toastInfo.mockReset();
  });

  it('ensures a draft id, calls aiEditDraft, and replaces the body on Improve', async () => {
    const ensureDraftId = vi.fn().mockResolvedValue('draft_1');
    const onApplied = vi.fn();
    runAiEditDraftMock.mockResolvedValue({ text: 'Improved body', model: 'demo/model', source: 'api' });

    render(<AiEditMenu ensureDraftId={ensureDraftId} onApplied={onApplied} />);
    await userEvent.click(screen.getByText('Edit with AI'));
    await userEvent.click(await screen.findByText('Improve'));

    await waitFor(() => expect(ensureDraftId).toHaveBeenCalled());
    expect(runAiEditDraftMock).toHaveBeenCalledWith('improve', 'draft_1', undefined);
    await waitFor(() => expect(onApplied).toHaveBeenCalledWith('Improved body'));
  });

  it('applies a custom tone via the Change tone popover', async () => {
    const ensureDraftId = vi.fn().mockResolvedValue('draft_2');
    const onApplied = vi.fn();
    runAiEditDraftMock.mockResolvedValue({ text: 'Formal body', model: 'demo/model', source: 'api' });

    render(<AiEditMenu ensureDraftId={ensureDraftId} onApplied={onApplied} />);
    await userEvent.click(screen.getByText('Edit with AI'));
    await userEvent.click(await screen.findByText('Change tone…'));
    await userEvent.type(await screen.findByPlaceholderText('e.g. more formal'), 'more formal');
    await userEvent.click(screen.getByText('Apply'));

    await waitFor(() =>
      expect(runAiEditDraftMock).toHaveBeenCalledWith('change_tone', 'draft_2', 'more formal')
    );
    await waitFor(() => expect(onApplied).toHaveBeenCalledWith('Formal body'));
  });

  it('shows a friendly error toast when the edit fails', async () => {
    const ensureDraftId = vi.fn().mockResolvedValue('draft_3');
    const onApplied = vi.fn();
    runAiEditDraftMock.mockRejectedValue(new Error('boom'));

    render(<AiEditMenu ensureDraftId={ensureDraftId} onApplied={onApplied} />);
    await userEvent.click(screen.getByText('Edit with AI'));
    await userEvent.click(await screen.findByText('Shorten'));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('AI is unavailable right now. Please try again.')
    );
    expect(onApplied).not.toHaveBeenCalled();
  });

  it('runs the requested action when dispatched from the command palette', async () => {
    const ensureDraftId = vi.fn().mockResolvedValue('draft_4');
    const onApplied = vi.fn();
    runAiEditDraftMock.mockResolvedValue({ text: 'Simplified body', model: 'demo/model', source: 'api' });

    render(<AiEditMenu ensureDraftId={ensureDraftId} onApplied={onApplied} />);
    dispatchAiEditCommand('simplify');

    await waitFor(() => expect(runAiEditDraftMock).toHaveBeenCalledWith('simplify', 'draft_4', undefined));
    await waitFor(() => expect(onApplied).toHaveBeenCalledWith('Simplified body'));
  });
});
