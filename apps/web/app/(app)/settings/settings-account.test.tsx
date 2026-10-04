import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listAccounts = vi.fn();
const changePassword = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    listAccounts: (...args: unknown[]) => listAccounts(...args),
    changePassword: (...args: unknown[]) => changePassword(...args),
  },
  useSession: () => ({ data: { user: { id: 'u1', email: 'ada@example.test' } }, isPending: false }),
}));

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) } }));

import { AccountSection } from './settings-account';

const CREDENTIAL = { id: 'acc-1', providerId: 'credential', accountId: 'u1', scopes: [], createdAt: new Date(), updatedAt: new Date() };
const GOOGLE = { id: 'acc-2', providerId: 'google', accountId: '123', scopes: [], createdAt: new Date(), updatedAt: new Date() };

function renderSection() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountSection />
    </QueryClientProvider>
  );
}

async function fill(user: ReturnType<typeof userEvent.setup>, current: string, next: string, confirm = next) {
  await user.type(await screen.findByLabelText('Current password'), current);
  await user.type(screen.getByLabelText('New password'), next);
  await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Update password' }));
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('AccountSection', () => {
  it('shows the email and the change-password form for a credential account, and submits with revokeOtherSessions', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL, GOOGLE], error: null });
    changePassword.mockResolvedValue({ data: { token: null, user: {} }, error: null });
    const user = userEvent.setup();
    renderSection();
    expect(await screen.findByText('ada@example.test')).toBeInTheDocument();
    await fill(user, 'old-password-1', 'correct-horse-battery');
    expect(changePassword).toHaveBeenCalledWith(
      { currentPassword: 'old-password-1', newPassword: 'correct-horse-battery', revokeOtherSessions: true },
      expect.anything()
    );
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Password updated. Other devices were signed out.'));
    expect(screen.getByLabelText('New password')).toHaveValue('');
  });

  it('rejects a mismatch and a policy violation client-side', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL], error: null });
    const user = userEvent.setup();
    renderSection();
    await fill(user, 'old-password-1', 'correct-horse-battery', 'correct-horse-batterx');
    expect(await screen.findByText("Passwords don't match.")).toBeInTheDocument();
    for (const label of ['Current password', 'New password', 'Confirm new password']) await user.clear(screen.getByLabelText(label));
    await fill(user, 'old-password-1', 'ada@example-2026');
    expect(await screen.findByText('Password must not contain your email address.')).toBeInTheDocument();
    expect(changePassword).not.toHaveBeenCalled();
  });

  it('maps INVALID_PASSWORD to "Current password is incorrect."', async () => {
    listAccounts.mockResolvedValue({ data: [CREDENTIAL], error: null });
    changePassword.mockResolvedValue({ data: null, error: { status: 400, code: 'INVALID_PASSWORD', message: 'Invalid password' } });
    const user = userEvent.setup();
    renderSection();
    await fill(user, 'wrong-password', 'correct-horse-battery');
    expect(await screen.findByText('Current password is incorrect.')).toBeInTheDocument();
  });

  it('explains the sign-in method for a social-only account', async () => {
    listAccounts.mockResolvedValue({ data: [GOOGLE], error: null });
    renderSection();
    expect(await screen.findByText(/You sign in with Google\./)).toBeInTheDocument();
    expect(screen.queryByLabelText('Current password')).not.toBeInTheDocument();
  });
});
