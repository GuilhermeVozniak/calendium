import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const pushMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushMock }),
}));

const useInstanceMock = vi.fn();
vi.mock('@/lib/use-instance', () => ({
  useInstance: () => useInstanceMock(),
}));

const runAiAskCitedMock = vi.fn();
const aiErrorMessageMock = vi.fn((..._args: unknown[]) => 'AI is unavailable right now. Please try again.');
vi.mock('@/lib/use-mail', () => ({
  runAiAskCited: (...args: unknown[]) => runAiAskCitedMock(...args),
  aiErrorMessage: (...args: unknown[]) => aiErrorMessageMock(...args),
}));

import { AskSidebarPanel, AskSidebarProvider, useAskSidebar } from './ask-sidebar';

function Toggle() {
  const { toggle } = useAskSidebar();
  return (
    <button type="button" onClick={toggle}>
      Toggle
    </button>
  );
}

function renderSidebar() {
  return render(
    <AskSidebarProvider>
      <Toggle />
      <AskSidebarPanel />
    </AskSidebarProvider>
  );
}

describe('AskSidebar', () => {
  beforeEach(() => {
    pushMock.mockReset();
    runAiAskCitedMock.mockReset();
    useInstanceMock.mockReturnValue({ data: { features: { ai: true } } });
  });

  it('renders nothing when the server has AI disabled', () => {
    useInstanceMock.mockReturnValue({ data: { features: { ai: false } } });
    const { container } = renderSidebar();
    // Only the Toggle button should be present; the panel itself opts out entirely.
    expect(container.querySelector('[data-testid="ask-sidebar"]')).not.toBeInTheDocument();
  });

  it('is closed by default and opens via the toggle', async () => {
    renderSidebar();
    expect(screen.getByTestId('ask-sidebar')).toHaveAttribute('data-state', 'closed');
    await userEvent.click(screen.getByText('Toggle'));
    expect(screen.getByTestId('ask-sidebar')).toHaveAttribute('data-state', 'open');
  });

  it('posts the question to aiAskCited and renders the answer with a clickable source', async () => {
    runAiAskCitedMock.mockResolvedValue({
      answer: 'Priya said the incident is resolved.',
      model: 'demo/model',
      source: 'api',
      sources: [
        { threadId: 'thr_1', subject: 'Postmortem', snippet: 'Root cause found.' },
      ],
    });
    renderSidebar();
    await userEvent.click(screen.getByText('Toggle'));

    await userEvent.type(screen.getByLabelText('Ask a question'), 'What did Priya say?');
    await userEvent.click(screen.getByLabelText('Ask'));

    expect(runAiAskCitedMock).toHaveBeenCalledWith('What did Priya say?');
    expect(await screen.findByText('Priya said the incident is resolved.')).toBeInTheDocument();

    await userEvent.click(screen.getByText('Postmortem'));
    expect(pushMock).toHaveBeenCalledWith('/mail?t=thr_1');
  });

  it('shows a friendly message on failure instead of fabricating an answer', async () => {
    runAiAskCitedMock.mockRejectedValue(new Error('rate limited'));
    renderSidebar();
    await userEvent.click(screen.getByText('Toggle'));
    await userEvent.type(screen.getByLabelText('Ask a question'), 'Anything?');
    await userEvent.click(screen.getByLabelText('Ask'));

    await waitFor(() =>
      expect(screen.getByText('AI is unavailable right now. Please try again.')).toBeInTheDocument()
    );
  });
});
