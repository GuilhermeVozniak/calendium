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

const sendVerificationEmail = vi.fn();
const sessionState = vi.hoisted(() => ({ value: { data: null as unknown, isPending: false } }));
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    useSession: () => sessionState.value,
    sendVerificationEmail: (...args: unknown[]) => sendVerificationEmail(...args),
  },
}));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn() } }));

import VerifyEmailPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
  sessionState.value = { data: null, isPending: false };
});

describe('VerifyEmailPage', () => {
  it('with a session: Verified + Continue to /mail', async () => {
    window.history.replaceState(null, '', '/verify-email');
    sessionState.value = { data: { user: { id: 'u1' } }, isPending: false };
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    expect(await screen.findByText('Email verified')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(replaceMock).toHaveBeenCalledWith('/mail');
  });

  it('without a session: Verified + sign in link', async () => {
    window.history.replaceState(null, '', '/verify-email');
    render(<VerifyEmailPage />);
    expect(await screen.findByText('Email verified')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign in to continue' })).toHaveAttribute('href', '/signin');
  });

  it.each(['TOKEN_EXPIRED', 'INVALID_TOKEN'])('with ?error=%s: expired copy and a resend form that calls sendVerificationEmail', async (code) => {
    window.history.replaceState(null, '', `/verify-email?error=${code}`);
    sendVerificationEmail.mockResolvedValue({ data: { status: true }, error: null });
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    expect(await screen.findByText('This link has expired or was already used')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send a new link' }));
    expect(sendVerificationEmail).toHaveBeenCalledWith({ email: 'ada@example.test', callbackURL: '/verify-email' }, expect.anything());
    expect(await screen.findByText('If an account exists for that address, we sent a new link.')).toBeInTheDocument();
  });

  it('resend failures use the shared copy', async () => {
    window.history.replaceState(null, '', '/verify-email?error=INVALID_TOKEN');
    sendVerificationEmail.mockResolvedValue({ data: null, error: { status: 500, message: 'Internal' } });
    const user = userEvent.setup();
    render(<VerifyEmailPage />);
    await user.type(await screen.findByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send a new link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("We couldn't send the email. Try again in a minute."));
  });

  it('shows a spinner while the session is pending', () => {
    window.history.replaceState(null, '', '/verify-email');
    sessionState.value = { data: null, isPending: true };
    render(<VerifyEmailPage />);
    expect(screen.getByLabelText('Loading')).toBeInTheDocument();
  });
});
