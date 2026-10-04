import * as React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));
vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const resetPassword = vi.fn();
vi.mock('@/lib/auth-client', () => ({ authClient: { resetPassword: (...args: unknown[]) => resetPassword(...args) } }));

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: (...a: unknown[]) => toastSuccess(...a) } }));

import ResetPasswordPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
});

async function fill(user: ReturnType<typeof userEvent.setup>, next: string, confirm = next) {
  await user.type(await screen.findByLabelText('New password'), next);
  await user.type(screen.getByLabelText('Confirm new password'), confirm);
  await user.click(screen.getByRole('button', { name: 'Update password' }));
}

describe('ResetPasswordPage', () => {
  it('with ?token= resets, toasts and goes to /signin', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-123');
    resetPassword.mockResolvedValue({ data: { status: true }, error: null });
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery');
    expect(resetPassword).toHaveBeenCalledWith({ newPassword: 'correct-horse-battery', token: 'tok-123' }, expect.anything());
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Password updated. Sign in with your new password.'));
    expect(replaceMock).toHaveBeenCalledWith('/signin');
  });

  it('rejects a mismatch and a policy violation client-side without calling the server', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-123');
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery', 'correct-horse-batterx');
    expect(await screen.findByText("Passwords don't match.")).toBeInTheDocument();
    expect(screen.getByLabelText('New password')).toHaveAccessibleDescription("Passwords don't match.");
    await user.clear(screen.getByLabelText('New password'));
    await user.clear(screen.getByLabelText('Confirm new password'));
    await fill(user, 'short');
    expect(await screen.findByText('Password must be at least 10 characters.')).toBeInTheDocument();
    expect(resetPassword).not.toHaveBeenCalled();
  });

  it('with ?error=INVALID_TOKEN shows the recovery path instead of the form', async () => {
    window.history.replaceState(null, '', '/reset-password?error=INVALID_TOKEN');
    render(<ResetPasswordPage />);
    expect(await screen.findByText('This link is invalid or has expired')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Request a new link' })).toHaveAttribute('href', '/forgot-password');
    expect(screen.queryByLabelText('New password')).not.toBeInTheDocument();
  });

  it('without a token behaves like an invalid link', async () => {
    window.history.replaceState(null, '', '/reset-password');
    render(<ResetPasswordPage />);
    expect(await screen.findByText('This link is invalid or has expired')).toBeInTheDocument();
  });

  it('a reused token (server INVALID_TOKEN) toasts and shows the recovery path', async () => {
    window.history.replaceState(null, '', '/reset-password?token=used');
    resetPassword.mockResolvedValue({ data: null, error: { status: 400, code: 'INVALID_TOKEN', message: 'Invalid token' } });
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    await fill(user, 'correct-horse-battery');
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('This link is invalid or has expired. Request a new one.'));
    expect(await screen.findByRole('link', { name: 'Request a new link' })).toBeInTheDocument();
    // The new state is announced: focus moves to its heading.
    expect(screen.getByRole('heading', { name: 'This link is invalid or has expired' })).toHaveFocus();
  });

  it('removes the token from the address bar right after reading it, keeping other params', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-123&utm=mail#top');
    resetPassword.mockResolvedValue({ data: { status: true }, error: null });
    const user = userEvent.setup();
    render(<ResetPasswordPage />);
    expect(await screen.findByLabelText('New password')).toBeInTheDocument();
    expect(window.location.pathname).toBe('/reset-password');
    expect(window.location.search).toBe('?utm=mail');
    expect(window.location.hash).toBe('#top');
    // The token still drives the submission.
    await fill(user, 'correct-horse-battery');
    expect(resetPassword).toHaveBeenCalledWith({ newPassword: 'correct-horse-battery', token: 'tok-123' }, expect.anything());
  });

  it('keeps the token when React re-runs the mount effect (StrictMode)', async () => {
    window.history.replaceState(null, '', '/reset-password?token=tok-strict');
    render(
      <React.StrictMode>
        <ResetPasswordPage />
      </React.StrictMode>
    );
    expect(await screen.findByLabelText('New password')).toBeInTheDocument();
    expect(window.location.search).toBe('');
  });
});
