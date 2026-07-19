import type { Delegation } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const listDelegationsMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listDelegations: (...args: unknown[]) => listDelegationsMock(...args),
  }),
}));

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { clearActingAs, getActingAs, setActingAs } from '@/lib/act-as';

import { ActAsMenuItems, ActingBanner } from './acting-as';

const ACTIVE_GRANT: Delegation = {
  id: 'd1',
  principalId: 'user_boss',
  assistantId: 'user_me',
  scopes: ['mail_read'],
  status: 'active',
  createdAt: '2026-07-01T10:00:00Z',
  acceptedAt: '2026-07-01T11:00:00Z',
  revokedAt: null,
};

const PENDING_GRANT: Delegation = {
  ...ACTIVE_GRANT,
  id: 'd2',
  principalId: 'user_pending',
  status: 'pending',
  acceptedAt: null,
};

function renderWithClient(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

function renderMenuItems() {
  return renderWithClient(
    <DropdownMenu open>
      <DropdownMenuTrigger asChild>
        <button type="button">menu</button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <ActAsMenuItems />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listDelegationsMock.mockResolvedValue({
    asPrincipal: [],
    asAssistant: [ACTIVE_GRANT, PENDING_GRANT],
  });
});

afterEach(() => {
  clearActingAs();
});

describe('ActingBanner', () => {
  it('renders nothing while not acting — the act-as state is never implicit', () => {
    renderWithClient(<ActingBanner />);
    expect(screen.queryByText(/Acting for/)).not.toBeInTheDocument();
  });

  it('badges the UI with the principal id while acting, and Stop acting clears it', async () => {
    setActingAs('user_boss');
    const user = userEvent.setup();
    renderWithClient(<ActingBanner />);
    expect(screen.getByText(/Acting for user_boss/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /Stop acting/ }));
    expect(getActingAs()).toBeNull();
    expect(screen.queryByText(/Acting for/)).not.toBeInTheDocument();
  });
});

describe('ActAsMenuItems', () => {
  it('lists only ACTIVE grants where the user is the assistant', async () => {
    renderMenuItems();
    expect(await screen.findByText('user_boss')).toBeInTheDocument();
    expect(screen.queryByText('user_pending')).not.toBeInTheDocument();
  });

  it('selecting a principal starts acting for them', async () => {
    const user = userEvent.setup();
    renderMenuItems();
    await user.click(await screen.findByText('user_boss'));
    expect(getActingAs()).toBe('user_boss');
  });

  it('renders nothing with no active grants and no acting state', async () => {
    listDelegationsMock.mockResolvedValue({ asPrincipal: [], asAssistant: [PENDING_GRANT] });
    renderMenuItems();
    await vi.waitFor(() => expect(listDelegationsMock).toHaveBeenCalled());
    expect(screen.queryByText('Act as')).not.toBeInTheDocument();
  });

  it('offers Stop acting while acting', async () => {
    setActingAs('user_boss');
    const user = userEvent.setup();
    renderMenuItems();
    await user.click(await screen.findByText('Stop acting'));
    expect(getActingAs()).toBeNull();
  });
});
