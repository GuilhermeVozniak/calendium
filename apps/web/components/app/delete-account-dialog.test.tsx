import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const listAccountsMock = vi.fn();
const deleteUserMock = vi.fn();
const signInSocialMock = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    listAccounts: (...args: unknown[]) => listAccountsMock(...args),
    deleteUser: (...args: unknown[]) => deleteUserMock(...args),
    signIn: { social: (...args: unknown[]) => signInSocialMock(...args) },
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

const beginMock = vi.fn();
const endMock = vi.fn();
const scrubMock = vi.fn();
const leaveMock = vi.fn();
vi.mock('@/lib/account-deletion', () => ({
  beginAccountDeletion: () => beginMock(),
  endAccountDeletion: () => endMock(),
  scrubLocalStateAfterDeletion: (...args: unknown[]) => scrubMock(...args),
  leaveAfterAccountDeletion: () => leaveMock(),
}));

import { DeleteAccountDialog, describeDeleteError, normalizeEmail } from './delete-account-dialog';

const EMAIL = 'me@example.com';

let queryClient: QueryClient;

function renderDialog() {
  queryClient = new QueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <DeleteAccountDialog open onOpenChange={() => {}} email={EMAIL} />
    </QueryClientProvider>
  );
}

async function confirmAs(user: ReturnType<typeof userEvent.setup>, password?: string) {
  await user.type(await screen.findByLabelText(/type your email/i), EMAIL);
  if (password !== undefined) await user.type(await screen.findByLabelText('Your password'), password);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Delete my account' })).toBeEnabled());
  await user.click(screen.getByRole('button', { name: 'Delete my account' }));
}

// Better Auth's list-accounts rows carry `providerId` (see settings-account.tsx).
const credentialAccounts = { data: [{ id: 'acc1', providerId: 'credential' }], error: null };
const socialAccounts = { data: [{ id: 'acc2', providerId: 'google' }], error: null };

beforeEach(() => {
  vi.clearAllMocks();
  listAccountsMock.mockResolvedValue(credentialAccounts);
  deleteUserMock.mockResolvedValue({ data: { success: true }, error: null });
  performSignOutMock.mockResolvedValue(undefined);
  scrubMock.mockResolvedValue(undefined);
  signInSocialMock.mockResolvedValue({ data: {}, error: null });
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

  it('a stale social session (400 SESSION_EXPIRED) offers provider re-auth, never "incorrect password"', async () => {
    listAccountsMock.mockResolvedValue(socialAccounts);
    deleteUserMock.mockResolvedValue({
      data: null,
      error: { status: 400, code: 'SESSION_EXPIRED', message: 'Session expired. Re-authenticate to perform this action.' },
    });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() => expect(listAccountsMock).toHaveBeenCalled());
    await confirmAs(user);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/confirm it.s you with Google/i);
    expect(alert).not.toHaveTextContent(/incorrect/i);
    await user.click(screen.getByRole('button', { name: 'Continue with Google' }));
    expect(signInSocialMock).toHaveBeenCalledWith({ provider: 'google', callbackURL: '/settings?tab=account' });
    expect(endMock).toHaveBeenCalled();
    expect(scrubMock).not.toHaveBeenCalled();
    expect(leaveMock).not.toHaveBeenCalled();
  });

  it('a stale session on a password account asks for the password and retries with it', async () => {
    deleteUserMock.mockResolvedValueOnce({
      data: null,
      error: { status: 400, code: 'SESSION_EXPIRED', message: 'Session expired.' },
    });
    const user = userEvent.setup();
    renderDialog();
    await confirmAs(user, 'hunter2');
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/enter your password/i);
    expect(alert).not.toHaveTextContent(/incorrect/i);
    expect(screen.queryByRole('button', { name: /continue with/i })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Delete my account' }));
    await waitFor(() => expect(deleteUserMock).toHaveBeenCalledTimes(2));
    expect(deleteUserMock).toHaveBeenLastCalledWith({ password: 'hunter2' });
    await waitFor(() => expect(leaveMock).toHaveBeenCalled());
  });

  it('a 401 (session already gone) says sign in again, not "incorrect password"', async () => {
    deleteUserMock.mockResolvedValue({ data: null, error: { status: 401, code: 'UNAUTHORIZED' } });
    const user = userEvent.setup();
    renderDialog();
    await confirmAs(user, 'hunter2');
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/sign in again/i);
    expect(alert).not.toHaveTextContent(/incorrect/i);
  });

  it('when the sign-in method lookup fails it says so and keeps confirm disabled', async () => {
    listAccountsMock.mockRejectedValue(new Error('network'));
    const user = userEvent.setup();
    renderDialog();
    expect(await screen.findByRole('alert')).toHaveTextContent(/could not check how you sign in/i);
    await user.type(screen.getByLabelText(/type your email/i), EMAIL);
    expect(screen.getByRole('button', { name: 'Delete my account' })).toBeDisabled();
  });

  it('on success flags the deletion before the call, scrubs local state, then hard-navigates to /goodbye', async () => {
    const order: string[] = [];
    beginMock.mockImplementation(() => order.push('begin'));
    deleteUserMock.mockImplementation(async () => {
      order.push('deleteUser');
      return { data: { success: true }, error: null };
    });
    scrubMock.mockImplementation(async () => {
      order.push('scrub');
    });
    leaveMock.mockImplementation(() => order.push('leave'));
    const user = userEvent.setup();
    renderDialog();
    await confirmAs(user, 'hunter2');
    await waitFor(() => expect(leaveMock).toHaveBeenCalled());
    expect(order).toEqual(['begin', 'deleteUser', 'scrub', 'leave']);
    expect(scrubMock).toHaveBeenCalledWith(queryClient);
    expect(endMock).not.toHaveBeenCalled();
    // No soft navigation and no sign-out round trip after the account is gone.
    expect(replaceMock).not.toHaveBeenCalled();
    expect(performSignOutMock).not.toHaveBeenCalled();
  });

  it('still leaves for /goodbye when the local scrub throws', async () => {
    scrubMock.mockRejectedValue(new Error('boom'));
    const user = userEvent.setup();
    renderDialog();
    await confirmAs(user, 'hunter2');
    await waitFor(() => expect(leaveMock).toHaveBeenCalled());
  });

  it('lowers the deletion flag when the call throws', async () => {
    deleteUserMock.mockRejectedValue(new Error('network'));
    const user = userEvent.setup();
    renderDialog();
    await confirmAs(user, 'hunter2');
    expect(await screen.findByRole('alert')).toHaveTextContent(/could not delete/i);
    expect(endMock).toHaveBeenCalled();
    expect(leaveMock).not.toHaveBeenCalled();
  });
});

describe('helpers', () => {
  it('normalizeEmail trims and lowercases', () => {
    expect(normalizeEmail('  Me@Example.com ')).toBe('me@example.com');
  });

  it('describeDeleteError maps codes to copy', () => {
    expect(describeDeleteError({ status: 400, code: 'INVALID_PASSWORD' }).message).toMatch(/incorrect/i);
    expect(describeDeleteError({ status: 503 }).message).toMatch(/try again/i);
    expect(describeDeleteError({ status: 429 }).message).toMatch(/too many/i);
    expect(describeDeleteError({ status: 401 }).message).toMatch(/sign in again/i);
    expect(
      describeDeleteError({ status: 409, code: 'owns_teams', details: { teams: [{ id: 't1', name: 'A' }] } }).teams
    ).toHaveLength(1);
  });

  // Track D I1: Better Auth answers a stale session with 400 SESSION_EXPIRED.
  it('describeDeleteError routes SESSION_EXPIRED / SESSION_NOT_FRESH to re-authentication, never "incorrect"', () => {
    for (const error of [
      { status: 400, code: 'SESSION_EXPIRED' },
      { status: 403, code: 'SESSION_NOT_FRESH' },
      { status: 403 },
    ]) {
      const password = describeDeleteError(error, { hasCredential: true, socialProvider: null });
      expect(password.reauth).toBe('password');
      expect(password.message).toMatch(/enter your password/i);
      const social = describeDeleteError(error, { hasCredential: false, socialProvider: 'microsoft' });
      expect(social.reauth).toBe('social');
      expect(social.message).toMatch(/Microsoft/);
      const unknown = describeDeleteError(error);
      expect(unknown.reauth).toBeNull();
      expect(unknown.message).toMatch(/sign in again/i);
      for (const d of [password, social, unknown]) expect(d.message).not.toMatch(/incorrect/i);
    }
  });
});
