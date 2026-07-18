import type { Draft } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Unlike the other desktop view test suites (ThreadPane/CalendarView), which
// always force orMock() through the *mock* branch, this suite needs the
// *real* branch exercised for the AI-edit call so we can assert exactly what
// reaches aiEditDraft (the free-text tone). isDemoMode() is separately mocked
// to false so ensureDraftId's persistDraft() also takes the real api.saveDraft
// path instead of synthesizing a local draft.
const saveDraftMock = vi.fn();
const aiEditDraftMock = vi.fn();
const listAccountsMock = vi.fn();

vi.mock('@/lib/api', () => ({
  api: {
    listAccounts: (...args: unknown[]) => listAccountsMock(...args),
    saveDraft: (...args: unknown[]) => saveDraftMock(...args),
    updateDraft: vi.fn(),
    sendDraft: vi.fn(),
    unsendDraft: vi.fn(),
    aiEditDraft: (...args: unknown[]) => aiEditDraftMock(...args),
  },
  orMock: async (real: () => unknown, _mock: () => unknown) => real(),
}));

vi.mock('@/lib/server-config', () => ({
  getActiveServerConfig: () => ({
    serverUrl: 'https://api.test',
    authBaseUrl: '',
    authProviders: [],
    mode: 'cloud',
    name: 'Test',
    features: { billing: false, google: false, microsoft: false, ai: true, push: false },
    undoSendSeconds: 15,
  }),
  isDemoMode: () => false,
}));

import { openCompose } from '@/lib/compose';

import { ComposeHost } from './ComposeView';

function makeDraft(overrides: Partial<Draft> = {}): Draft {
  return {
    id: 'draft_1',
    accountId: 'acc_1',
    threadId: null,
    to: [],
    cc: [],
    bcc: [],
    subject: '',
    bodyHtml: '<p></p>',
    scheduledAt: null,
    sendAttempts: 0,
    aiGenerated: false,
    lastError: null,
    updatedAt: new Date().toISOString(),
    ...overrides,
  };
}

function renderHost() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <ComposeHost />
    </QueryClientProvider>
  );
}

describe('ComposeHost — AI edit tone', () => {
  beforeEach(() => {
    listAccountsMock.mockReset().mockResolvedValue([
      {
        id: 'acc_1',
        provider: 'google',
        email: 'ada@calendium.app',
        status: 'active',
        scopes: [],
        vipSenders: [],
        lastSyncedAt: null,
        createdAt: new Date().toISOString(),
      },
    ]);
    saveDraftMock.mockReset().mockResolvedValue(makeDraft());
    aiEditDraftMock.mockReset().mockResolvedValue({ text: 'Rewritten body', model: 'gpt' });
  });

  it('sends the user-typed tone through to aiEditDraft when submitted with Enter', async () => {
    renderHost();
    openCompose({ kind: 'new' });

    await screen.findByRole('dialog');
    await userEvent.type(screen.getByLabelText('Body'), 'Hello there');

    await userEvent.click(screen.getByRole('button', { name: /edit with ai/i }));
    await userEvent.click(screen.getByText('Change tone…'));

    const toneInput = await screen.findByLabelText('Target tone');
    await userEvent.type(toneInput, 'more casual{enter}');

    await waitFor(() =>
      expect(aiEditDraftMock).toHaveBeenCalledWith('change_tone', expect.any(String), 'more casual')
    );
  });

  it('applies the typed tone via the Apply button too', async () => {
    renderHost();
    openCompose({ kind: 'new' });

    await screen.findByRole('dialog');
    await userEvent.type(screen.getByLabelText('Body'), 'Hello there');

    await userEvent.click(screen.getByRole('button', { name: /edit with ai/i }));
    await userEvent.click(screen.getByText('Change tone…'));

    const toneInput = await screen.findByLabelText('Target tone');
    await userEvent.type(toneInput, 'more playful');
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() =>
      expect(aiEditDraftMock).toHaveBeenCalledWith('change_tone', expect.any(String), 'more playful')
    );
  });

  it('does not offer Apply for a blank tone', async () => {
    renderHost();
    openCompose({ kind: 'new' });

    await screen.findByRole('dialog');
    await userEvent.click(screen.getByRole('button', { name: /edit with ai/i }));
    await userEvent.click(screen.getByText('Change tone…'));

    await screen.findByLabelText('Target tone');
    expect((screen.getByRole('button', { name: 'Apply' }) as HTMLButtonElement).disabled).toBe(true);
  });
});
