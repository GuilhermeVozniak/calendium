import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const replaceMock = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ replace: replaceMock, push: vi.fn() }) }));
vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const signInEmail = vi.fn();
const signUpEmail = vi.fn();
const sendVerificationEmail = vi.fn();
const useSessionMock = vi.fn(() => ({ data: null, isPending: false }));
const invalidateAccessToken = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: {
    useSession: () => useSessionMock(),
    sendVerificationEmail: (...args: unknown[]) => sendVerificationEmail(...args),
  },
  signIn: { email: (...args: unknown[]) => signInEmail(...args), social: vi.fn() },
  signUp: { email: (...args: unknown[]) => signUpEmail(...args) },
  invalidateAccessToken: () => invalidateAccessToken(),
}));

vi.mock('@/lib/use-instance', () => ({
  useInstance: () => ({ data: { authProviders: ['email'], features: { email: true } } }),
}));

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: (...a: unknown[]) => toastSuccess(...a) } }));

import SignInPage from './page';

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>, opts: { signup?: boolean } = {}) {
  if (opts.signup) await user.click(screen.getByRole('button', { name: "Don't have an account? Sign up" }));
  await user.type(screen.getByLabelText('Email'), 'ada@example.test');
  await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
  await user.click(screen.getByRole('button', { name: opts.signup ? 'Create account' : 'Sign in' }));
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, '', '/signin');
});

afterEach(() => {
  vi.useRealTimers();
});

describe('SignInPage', () => {
  it('links to /forgot-password; only sign-up enforces the 10-character minimum', async () => {
    const user = userEvent.setup();
    render(<SignInPage />);
    expect(screen.getByRole('link', { name: 'Forgot password?' })).toHaveAttribute('href', '/forgot-password');
    // Existing accounts may have 8–9 character passwords (the old minimum):
    // sign-in must not block them client-side.
    expect(screen.getByLabelText('Password')).not.toHaveAttribute('minlength');
    await user.click(screen.getByRole('button', { name: "Don't have an account? Sign up" }));
    expect(screen.getByLabelText('Password')).toHaveAttribute('minlength', '10');
  });

  it('submits a 9-character password in sign-in mode', async () => {
    const user = userEvent.setup();
    signInEmail.mockResolvedValue({ data: { token: 'sess' }, error: null });
    render(<SignInPage />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.type(screen.getByLabelText('Password'), 'nine-char');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(screen.getByLabelText('Password')).toBeValid();
    await waitFor(() => expect(signInEmail).toHaveBeenCalledWith(expect.objectContaining({ password: 'nine-char' }), expect.anything()));
  });

  it('sign-up with token === null shows "Check your inbox" with a 60 s resend cooldown, then resends', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    signUpEmail.mockResolvedValue({ data: { token: null, user: { email: 'ada@example.test' } }, error: null });
    sendVerificationEmail.mockResolvedValue({ data: { status: true }, error: null });
    render(<SignInPage />);

    await fillAndSubmit(user, { signup: true });

    expect(signUpEmail).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'ada@example.test', password: 'correct-horse-battery', callbackURL: '/verify-email' }),
      expect.anything()
    );
    expect(await screen.findByText('Check your inbox')).toBeInTheDocument();
    expect(screen.getByText(/ada@example\.test/)).toBeInTheDocument();
    const resend = screen.getByRole('button', { name: /Resend in 60 s/ });
    expect(resend).toBeDisabled();
    expect(replaceMock).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(60_000);
    });
    const ready = await screen.findByRole('button', { name: 'Resend email' });
    expect(ready).toBeEnabled();
    await user.click(ready);
    expect(sendVerificationEmail).toHaveBeenCalledWith({ email: 'ada@example.test', callbackURL: '/verify-email' }, expect.anything());
    expect(await screen.findByRole('button', { name: /Resend in 60 s/ })).toBeDisabled();
  });

  it('sign-up that returns a session goes straight to the inbox (SMTP off)', async () => {
    const user = userEvent.setup();
    signUpEmail.mockResolvedValue({ data: { token: 'sess', user: {} }, error: null });
    render(<SignInPage />);
    await fillAndSubmit(user, { signup: true });
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/mail'));
  });

  it('sign-in 403 EMAIL_NOT_VERIFIED switches to the check-email notice with the verify-first copy', async () => {
    const user = userEvent.setup();
    signInEmail.mockResolvedValue({ data: null, error: { status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' } });
    render(<SignInPage />);
    await fillAndSubmit(user);
    expect(await screen.findByText('Verify your email first — we sent a new link.')).toBeInTheDocument();
    // The swapped-in state is announced: focus on its heading, polite status.
    expect(screen.getByRole('heading', { name: 'Check your inbox' })).toHaveFocus();
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite');
    expect(screen.getByRole('button', { name: /Resend in 60 s/ })).toBeDisabled();
    expect(replaceMock).not.toHaveBeenCalled();
  });

  it('sign-in 429 shows the retry-after copy from the header', async () => {
    const user = userEvent.setup();
    signInEmail.mockImplementation(async (_body: unknown, opts: { onError: (ctx: { response: Response }) => void }) => {
      opts.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '42' } }) });
      return { data: null, error: { status: 429, message: 'Too many requests. Please try again later.' } };
    });
    render(<SignInPage />);
    await fillAndSubmit(user);
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Too many attempts, try again in 42 s'));
  });

  it('sign-in success passes the destination as callbackURL and navigates', async () => {
    const user = userEvent.setup();
    window.history.replaceState(null, '', '/signin?next=/calendar');
    signInEmail.mockResolvedValue({ data: { token: 'sess' }, error: null });
    render(<SignInPage />);
    await fillAndSubmit(user);
    expect(signInEmail).toHaveBeenCalledWith(expect.objectContaining({ email: 'ada@example.test', callbackURL: '/calendar' }), expect.anything());
    await waitFor(() => expect(replaceMock).toHaveBeenCalledWith('/calendar'));
  });

  it('drops any cached API token after a successful sign-in, before navigating', async () => {
    const user = userEvent.setup();
    signInEmail.mockResolvedValue({ data: { token: 'sess' }, error: null });
    replaceMock.mockImplementation(() => expect(invalidateAccessToken).toHaveBeenCalledTimes(1));
    render(<SignInPage />);
    await fillAndSubmit(user);
    await waitFor(() => expect(replaceMock).toHaveBeenCalled());
    replaceMock.mockReset();
  });

  it('drops any cached API token after a sign-up that signs in (SMTP off), not after one that only sends mail', async () => {
    const user = userEvent.setup();
    signUpEmail.mockResolvedValueOnce({ data: { token: 'sess', user: {} }, error: null });
    const { unmount } = render(<SignInPage />);
    await fillAndSubmit(user, { signup: true });
    await waitFor(() => expect(invalidateAccessToken).toHaveBeenCalledTimes(1));
    unmount();

    invalidateAccessToken.mockClear();
    signUpEmail.mockResolvedValueOnce({ data: { token: null, user: {} }, error: null });
    render(<SignInPage />);
    await fillAndSubmit(user, { signup: true });
    expect(await screen.findByText('Check your inbox')).toBeInTheDocument();
    expect(invalidateAccessToken).not.toHaveBeenCalled();
  });

  it('does not touch the token cache when sign-in fails', async () => {
    const user = userEvent.setup();
    signInEmail.mockResolvedValue({ data: null, error: { status: 401, code: 'INVALID_EMAIL_OR_PASSWORD', message: 'Invalid email or password' } });
    render(<SignInPage />);
    await fillAndSubmit(user);
    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(invalidateAccessToken).not.toHaveBeenCalled();
  });
});
