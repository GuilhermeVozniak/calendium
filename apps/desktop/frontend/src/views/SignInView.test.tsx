import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const signInEmail = vi.fn();
const signUpEmail = vi.fn();
vi.mock('@/lib/auth', () => ({
  signInEmail: (...a: unknown[]) => signInEmail(...a),
  signUpEmail: (...a: unknown[]) => signUpEmail(...a),
  verifyOtt: vi.fn(),
}));

const openExternal = vi.fn();
vi.mock('@/lib/wails', () => ({
  desktop: { OpenExternal: (...a: unknown[]) => openExternal(...a) },
  onDeepLink: () => () => {},
}));

vi.mock('@/lib/server-config', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/server-config')>();
  return {
    ...actual,
    useServerConfig: () => ({
      config: {
        serverUrl: 'https://api.test',
        authBaseUrl: 'https://mail.example.com/api/auth',
        authProviders: ['email'],
        mode: 'self_host',
        name: 'Test',
        features: { billing: false, google: false, microsoft: false, ai: false, push: false, email: true },
        undoSendSeconds: 15,
        webUrl: 'https://app.example.com',
      },
    }),
  };
});

import { SignInView } from './SignInView';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SignInView (piece 2)', () => {
  it('"Forgot password?" opens <webUrl>/forgot-password in the system browser', async () => {
    const user = userEvent.setup();
    render(<SignInView />);
    await user.click(screen.getByRole('button', { name: 'Forgot password?' }));
    expect(openExternal).toHaveBeenCalledWith('https://app.example.com/forgot-password');
  });

  it('a verification-required sign-up switches to sign-in mode with the check-inbox notice', async () => {
    signUpEmail.mockResolvedValue({ ok: true, verificationRequired: true });
    const user = userEvent.setup();
    render(<SignInView />);
    await user.click(screen.getByRole('button', { name: 'Create one' }));
    await user.type(screen.getByLabelText('Name'), 'Ada');
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
    await user.click(screen.getByRole('button', { name: 'Create account' }));
    await waitFor(() => expect(screen.getByText('Check your inbox — we sent a verification link to ada@example.test.')).toBeTruthy());
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeTruthy();
  });

  it('surfaces the verify-first error from sign-in', async () => {
    signInEmail.mockResolvedValue({ ok: false, error: 'Verify your email first — we sent a new link.' });
    const user = userEvent.setup();
    render(<SignInView />);
    await user.type(screen.getByLabelText('Email'), 'ada@example.test');
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(screen.getByText('Verify your email first — we sent a new link.')).toBeTruthy());
  });
});
