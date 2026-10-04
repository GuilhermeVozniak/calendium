import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('next/link', () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

const requestPasswordReset = vi.fn();
vi.mock('@/lib/auth-client', () => ({
  authClient: { requestPasswordReset: (...args: unknown[]) => requestPasswordReset(...args) },
}));

const instanceState = vi.hoisted(() => ({ value: { data: undefined as unknown, isError: false } }));
vi.mock('@/lib/use-instance', () => ({ useInstance: () => instanceState.value }));

const toastError = vi.fn();
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => toastError(...a), success: vi.fn() } }));

import ForgotPasswordPage from './page';

beforeEach(() => {
  vi.clearAllMocks();
  instanceState.value = { data: { features: { email: true } }, isError: false };
});

describe('ForgotPasswordPage', () => {
  it('submits the email with redirectTo=/reset-password and shows the generic confirmation', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockResolvedValue({ data: { status: true }, error: null });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    expect(requestPasswordReset).toHaveBeenCalledWith({ email: 'ada@example.test', redirectTo: '/reset-password' }, expect.anything());
    expect(await screen.findByText('If an account exists for that address, we sent a link.')).toBeInTheDocument();
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument();
  });

  it('shows the same confirmation whatever the server answered for an unknown address (no enumeration)', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockResolvedValue({ data: { status: true, message: 'If this email exists in our system, check your email for the reset link' }, error: null });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'nobody@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    expect(await screen.findByText('If an account exists for that address, we sent a link.')).toBeInTheDocument();
  });

  it('renders the administrator instructions when features.email === false', () => {
    instanceState.value = { data: { features: { email: false } }, isError: false };
    render(<ForgotPasswordPage />);
    expect(screen.getByText("Password reset by email isn't available on this server")).toBeInTheDocument();
    expect(screen.getByText(/reset-password\.mjs/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument();
  });

  it('renders the form when the instance is unavailable or does not report features.email', () => {
    instanceState.value = { data: undefined, isError: true };
    const { unmount } = render(<ForgotPasswordPage />);
    expect(screen.getByLabelText('Email')).toBeInTheDocument();
    unmount();
    instanceState.value = { data: { features: {} }, isError: false };
    render(<ForgotPasswordPage />);
    expect(screen.getByLabelText('Email')).toBeInTheDocument();
  });

  it('maps 429 and 500 to the shared copy', async () => {
    const user = userEvent.setup();
    requestPasswordReset.mockImplementationOnce(async (_b: unknown, o: { onError: (c: { response: Response }) => void }) => {
      o.onError({ response: new Response(null, { status: 429, headers: { 'X-Retry-After': '9' } }) });
      return { data: null, error: { status: 429 } };
    });
    render(<ForgotPasswordPage />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Too many attempts, try again in 9 s'));

    requestPasswordReset.mockResolvedValueOnce({ data: null, error: { status: 500, message: 'Internal Server Error' } });
    await user.click(screen.getByRole('button', { name: 'Send reset link' }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("We couldn't send the email. Try again in a minute."));
  });
});
