import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listAccountsMock = vi.fn();
const deleteUserMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    listAccounts: (...args: unknown[]) => listAccountsMock(...args),
    deleteUser: (...args: unknown[]) => deleteUserMock(...args),
  },
}));

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: replaceMock, push: vi.fn() }),
}));

const performSignOutMock = vi.fn();
vi.mock('@/lib/sign-out', () => ({
  performSignOut: (...args: unknown[]) => performSignOutMock(...args),
}));

import { DeleteAccountDialog, describeDeleteError, normalizeEmail } from './delete-account-dialog';

const EMAIL = 'me@example.com';

function renderDialog() {
  return render(<DeleteAccountDialog open onOpenChange={() => {}} email={EMAIL} />);
}

// Better Auth's list-accounts rows carry `providerId` (see settings-account.tsx).
const credentialAccounts = { data: [{ id: 'acc1', providerId: 'credential' }], error: null };
const socialAccounts = { data: [{ id: 'acc2', providerId: 'google' }], error: null };

beforeEach(() => {
  vi.clearAllMocks();
  listAccountsMock.mockResolvedValue(credentialAccounts);
  deleteUserMock.mockResolvedValue({ data: { success: true }, error: null });
  performSignOutMock.mockResolvedValue(undefined);
});

describe('DeleteAccountDialog', () => {
  it('keeps confirm disabled until the typed email matches', async () => {
    const user = userEvent.setup();
    renderDialog();
    const confirm = await screen.findByRole('button', { name: 'Delete my account' });
    expect(confirm).toBeDisabled();
    await user.type(screen.getByLabelText(/type your email/i), 'wrong@example.com');
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(confirm).toBeDisabled();
    await user.clear(screen.getByLabelText(/type your email/i));
    await user.type(screen.getByLabelText(/type your email/i), EMAIL);
    expect(confirm).toBeEnabled();
  });

  // Review Focus 4: case and whitespace are normalized, not byte-compared.
  it('enables confirm when the typed email matches ignoring case and whitespace', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), '  Me@Example.com ');
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled();
  });

  it('keeps confirm disabled for a credential account until a password is typed', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await screen.findByLabelText('Your password');
    expect(screen.getByRole('button', { name: 'Delete my account' })).toBeDisabled();
  });

  it('shows the password field only when a credential account exists', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    expect(screen.queryByLabelText('Your password')).toBeNull();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled());
  });

  it('calls deleteUser with the password for credential accounts', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(deleteUserMock).toHaveBeenCalledWith({ password: 'hunter2' }));
  });

  it('calls deleteUser without a password for social-only accounts', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled());
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(deleteUserMock).toHaveBeenCalledWith({}));
  });

  it('renders the blocking team list on owns_teams and does not navigate', async () => {
    deleteUserMock.mockResolvedValue({
      data: null,
      error: {
        status: 409,
        code: 'owns_teams',
        message: 'x',
        details: {
          teams: [
            { id: 't1', name: 'Design' },
            { id: 't2', name: 'Ops' },
          ],
        },
      },
    });
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    expect(await screen.findByText('Design')).toBeInTheDocument();
    expect(screen.getByText('Ops')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(/transfer ownership/i);
    expect(replaceMock).not.toHaveBeenCalled();
    expect(performSignOutMock).not.toHaveBeenCalled();
  });

  it('explains a stale social session (Better Auth answers 400 SESSION_EXPIRED)', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    deleteUserMock.mockResolvedValue({
      data: null,
      error: { status: 400, code: 'SESSION_EXPIRED', message: 'Session expired. Re-authenticate to perform this action.' },
    });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled());
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/sign in again/i);
  });

  it('on success routes to /goodbye first, then signs out and clears local state', async () => {
    const user = userEvent.setup();
    renderDialog();
    await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
    await user.type(await screen.findByLabelText('Your password'), 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(performSignOutMock).toHaveBeenCalled());
    expect(replaceMock).toHaveBeenCalledWith('/goodbye');
    expect(replaceMock.mock.invocationCallOrder[0]).toBeLessThan(performSignOutMock.mock.invocationCallOrder[0]);
  });
});

describe('helpers', () => {
  it('normalizeEmail trims and lowercases', () => {
    expect(normalizeEmail('  Me@Example.com ')).toBe('me@example.com');
  });

  it('describeDeleteError maps codes to copy', () => {
    expect(describeDeleteError({ status: 400, code: 'INVALID_PASSWORD' }).message).toMatch(/password/i);
    expect(describeDeleteError({ status: 400, code: 'SESSION_EXPIRED' }).message).toMatch(/sign in again/i);
    expect(describeDeleteError({ status: 403, code: 'SESSION_NOT_FRESH' }).message).toMatch(/sign in again/i);
    expect(describeDeleteError({ status: 503 }).message).toMatch(/try again/i);
    expect(describeDeleteError({ status: 429 }).message).toMatch(/too many/i);
    expect(
      describeDeleteError({ status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'A' }] } }).teams
    ).toHaveLength(1);
  });
});
