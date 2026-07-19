import type { AuditEntry, Delegation } from '@calendium/shared';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listDelegationsMock = vi.fn();
const listDelegationAuditMock = vi.fn();
const createDelegationMock = vi.fn();
const acceptDelegationMock = vi.fn();
const revokeDelegationMock = vi.fn();
vi.mock('@/lib/api', () => ({
  getApiClient: () => ({
    listDelegations: (...args: unknown[]) => listDelegationsMock(...args),
    listDelegationAudit: (...args: unknown[]) => listDelegationAuditMock(...args),
    createDelegation: (...args: unknown[]) => createDelegationMock(...args),
    acceptDelegation: (...args: unknown[]) => acceptDelegationMock(...args),
    revokeDelegation: (...args: unknown[]) => revokeDelegationMock(...args),
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

import { DelegationSection } from './delegation';

const GRANTED: Delegation = {
  id: 'd1',
  principalId: 'user_me',
  assistantId: 'user_ea',
  scopes: ['mail_read', 'mail_write'],
  status: 'active',
  createdAt: '2026-07-01T10:00:00Z',
  acceptedAt: '2026-07-01T11:00:00Z',
  revokedAt: null,
};

const RECEIVED_PENDING: Delegation = {
  id: 'd2',
  principalId: 'user_boss',
  assistantId: 'user_me',
  scopes: ['calendar_read'],
  status: 'pending',
  createdAt: '2026-07-02T10:00:00Z',
  acceptedAt: null,
  revokedAt: null,
};

const AUDIT_ENTRY: AuditEntry = {
  id: 'a1',
  actorId: 'user_ea',
  principalId: 'user_me',
  action: 'POST /v1/mail/threads/{id}/actions',
  resourceType: 'mail',
  resourceId: 't1',
  metadata: { delegated: true },
  createdAt: '2026-07-10T09:00:00Z',
};

function renderSection() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <DelegationSection />
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  listDelegationsMock.mockResolvedValue({
    asPrincipal: [GRANTED],
    asAssistant: [RECEIVED_PENDING],
  });
  listDelegationAuditMock.mockResolvedValue({ entries: [AUDIT_ENTRY] });
});

describe('DelegationSection', () => {
  it('renders both grant sides with server ids, statuses, and scope labels', async () => {
    renderSection();
    expect(await screen.findByText('user_ea')).toBeInTheDocument();
    expect(screen.getByText('user_boss')).toBeInTheDocument();
    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('Pending')).toBeInTheDocument();
    expect(screen.getByText('Read mail')).toBeInTheDocument();
    expect(screen.getByText('Send & manage mail')).toBeInTheDocument();
    expect(screen.getByText('Read calendar')).toBeInTheDocument();
  });

  it('renders the audit table from server entries only', async () => {
    renderSection();
    expect(await screen.findByText('POST /v1/mail/threads/{id}/actions')).toBeInTheDocument();
    expect(screen.getByText(/by user_ea/)).toBeInTheDocument();
    expect(listDelegationAuditMock).toHaveBeenCalledWith(50);
  });

  it('shows honest empty states when there are no grants and no audit entries', async () => {
    listDelegationsMock.mockResolvedValue({ asPrincipal: [], asAssistant: [] });
    listDelegationAuditMock.mockResolvedValue({ entries: [] });
    renderSection();
    expect(
      await screen.findByText('No delegations yet. Grant an assistant scoped access to get started.')
    ).toBeInTheDocument();
    expect(await screen.findByText('No delegated activity yet.')).toBeInTheDocument();
  });

  it('accepts a pending received grant', async () => {
    acceptDelegationMock.mockResolvedValue({ ...RECEIVED_PENDING, status: 'active' });
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole('button', { name: 'Accept delegation from user_boss' })
    );
    await waitFor(() => expect(acceptDelegationMock).toHaveBeenCalledWith('d2'));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Delegation accepted'));
  });

  it('revokes a granted delegation', async () => {
    revokeDelegationMock.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole('button', { name: 'Revoke delegation for user_ea' })
    );
    await waitFor(() => expect(revokeDelegationMock).toHaveBeenCalledWith('d1'));
  });

  it('creates a grant with the picked scopes and requires email + at least one scope', async () => {
    createDelegationMock.mockResolvedValue({ ...GRANTED, id: 'd3' });
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /New grant/ }));
    await screen.findByRole('dialog');

    const submit = screen.getByRole('button', { name: 'Create grant' });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText('Assistant email'), 'ea2@example.com');
    expect(submit).toBeDisabled(); // still no scopes

    await user.click(screen.getByRole('checkbox', { name: 'Read mail' }));
    await user.click(screen.getByRole('checkbox', { name: 'Manage calendar' }));
    expect(submit).toBeEnabled();

    await user.click(submit);
    await waitFor(() =>
      expect(createDelegationMock).toHaveBeenCalledWith('ea2@example.com', [
        'mail_read',
        'calendar_write',
      ])
    );
    // Success closes the dialog.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('surfaces the server error message when creation fails', async () => {
    const { ApiRequestError } = await import('@calendium/shared');
    createDelegationMock.mockRejectedValue(
      new ApiRequestError(404, 'not_found', 'resource not found')
    );
    const user = userEvent.setup();
    renderSection();
    await user.click(await screen.findByRole('button', { name: /New grant/ }));
    await user.type(screen.getByLabelText('Assistant email'), 'ghost@example.com');
    await user.click(screen.getByRole('checkbox', { name: 'Read mail' }));
    await user.click(screen.getByRole('button', { name: 'Create grant' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('resource not found'));
  });
});
