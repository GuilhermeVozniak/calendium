import type { Snippet, Team } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock only the API boundary (@/lib/api) so the real settings-data.ts
// wrappers run for real — mirrors settings-compose.test.tsx.
const listSnippetsMock = vi.fn();
const createSnippetMock = vi.fn();
const deleteSnippetMock = vi.fn();
const listTeamsMock = vi.fn();
const getMeMock = vi.fn();
const getTeamMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listSnippets: (...args: unknown[]) => listSnippetsMock(...args),
    createSnippet: (...args: unknown[]) => createSnippetMock(...args),
    deleteSnippet: (...args: unknown[]) => deleteSnippetMock(...args),
    listTeams: (...args: unknown[]) => listTeamsMock(...args),
    getMe: (...args: unknown[]) => getMeMock(...args),
    getTeam: (...args: unknown[]) => getTeamMock(...args),
  }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
  },
}));

import { SnippetsSection } from './settings-page';

const PERSONAL: Snippet = {
  id: 's1',
  name: 'Intro',
  shortcut: ';intro',
  bodyHtml: '<p>Hi there</p>',
  usageCount: 3,
  teamId: null,
  authorId: 'user_me',
  canDelete: true,
};

// A teammate's snippet the server says a plain member may not delete (F2:
// canDelete is computed server-side with exact author/role parity).
const TEAM_SNIPPET: Snippet = {
  id: 's2',
  name: 'Team reply',
  shortcut: null,
  bodyHtml: '<p>On behalf of Acme</p>',
  usageCount: 8,
  teamId: 'team1',
  authorId: 'user_other',
  canDelete: false,
};

const TEAM: Team = {
  id: 'team1',
  name: 'Acme',
  createdBy: 'user_other',
  createdAt: '2026-07-01T00:00:00Z',
};

function renderSection() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <SnippetsSection />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listSnippetsMock.mockResolvedValue([PERSONAL, TEAM_SNIPPET]);
  listTeamsMock.mockResolvedValue([TEAM]);
  getMeMock.mockResolvedValue({
    id: 'user_me',
    email: 'me@example.com',
    name: 'Me',
    avatarUrl: null,
    createdAt: '2026-01-01T00:00:00Z',
  });
  getTeamMock.mockResolvedValue({ team: TEAM, members: [] });
});

describe('SnippetsSection — team snippets (M2.7 Task 11)', () => {
  it('badges a team snippet with the team name from server data', async () => {
    renderSection();
    expect(await screen.findByText('Team reply')).toBeInTheDocument();
    expect(await screen.findByText('Acme')).toBeInTheDocument();
  });

  it('hides delete when the server computed canDelete=false, keeps it for your own', async () => {
    renderSection();
    expect(await screen.findByText('Team reply')).toBeInTheDocument();
    expect(
      await screen.findByRole('button', { name: 'Delete snippet Intro' })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Delete snippet Team reply' })
    ).not.toBeInTheDocument();
    // The role heuristic is gone: no per-team roster fetch just to gate a button.
    expect(getTeamMock).not.toHaveBeenCalled();
  });

  it('shows delete when the server grants it (author or admin+)', async () => {
    listSnippetsMock.mockResolvedValue([PERSONAL, { ...TEAM_SNIPPET, canDelete: true }]);
    renderSection();
    expect(
      await screen.findByRole('button', { name: 'Delete snippet Team reply' })
    ).toBeInTheDocument();
  });

  it('fails open when canDelete is absent (older server) — the server still decides', async () => {
    const { canDelete: _dropped, ...legacy } = TEAM_SNIPPET;
    listSnippetsMock.mockResolvedValue([legacy]);
    renderSection();
    expect(
      await screen.findByRole('button', { name: 'Delete snippet Team reply' })
    ).toBeInTheDocument();
  });

  it('omits the team selector when the user belongs to no teams', async () => {
    listTeamsMock.mockResolvedValue([]);
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /New snippet/ }));
    await screen.findByRole('dialog');
    expect(screen.queryByText('Share with')).not.toBeInTheDocument();
  });

  it('creates a team-scoped snippet via the Share with selector', async () => {
    createSnippetMock.mockResolvedValue({ ...TEAM_SNIPPET, id: 's3', name: 'FAQ' });
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /New snippet/ }));
    await screen.findByRole('dialog');

    await user.type(screen.getByLabelText('Name'), 'FAQ');
    await user.type(screen.getByLabelText('Body'), 'Answers');

    await user.click(screen.getByRole('combobox', { name: 'Share with' }));
    await user.click(await screen.findByRole('option', { name: 'Acme' }));

    await user.click(screen.getByRole('button', { name: 'Create snippet' }));
    await waitFor(() => expect(createSnippetMock).toHaveBeenCalledTimes(1));
    expect(createSnippetMock.mock.calls[0]![0]).toMatchObject({ name: 'FAQ', teamId: 'team1' });
    // Success closes the dialog.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('creates a personal snippet by default (teamId null — no implicit team scope)', async () => {
    createSnippetMock.mockResolvedValue({ ...PERSONAL, id: 's4', name: 'Solo' });
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /New snippet/ }));
    await screen.findByRole('dialog');

    await user.type(screen.getByLabelText('Name'), 'Solo');
    await user.type(screen.getByLabelText('Body'), 'Just mine');
    await user.click(screen.getByRole('button', { name: 'Create snippet' }));
    await waitFor(() => expect(createSnippetMock).toHaveBeenCalledTimes(1));
    expect(createSnippetMock.mock.calls[0]![0]).toMatchObject({ name: 'Solo', teamId: null });
  });
});
