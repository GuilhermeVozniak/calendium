import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const signInEmail = vi.fn();
const signUpEmail = vi.fn();
vi.mock('@/lib/auth', () => ({
  signInEmail: (...a: unknown[]) => signInEmail(...a),
  signUpEmail: (...a: unknown[]) => signUpEmail(...a),
  verifyOtt: (...a: unknown[]) => verifyOtt(...a),
}));
const verifyOtt = vi.fn();

const openExternal = vi.fn();
const takePendingDeepLink = vi.fn<(route: string) => Promise<string>>();
let deepLinkHandlers: Array<(url: string) => void> = [];
vi.mock('@/lib/wails', () => ({
  desktop: {
    OpenExternal: (...a: unknown[]) => openExternal(...a),
    TakePendingDeepLink: (route: string) => takePendingDeepLink(route),
  },
  onDeepLink: (handler: (url: string) => void) => {
    deepLinkHandlers.push(handler);
    return () => {
      deepLinkHandlers = deepLinkHandlers.filter((h) => h !== handler);
    };
  },
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
  deepLinkHandlers = [];
  takePendingDeepLink.mockResolvedValue('');
  verifyOtt.mockResolvedValue({ ok: false, error: 'That code expired.' });
});

describe('SignInView deep-link handoff', () => {
  it('redeems a cold-start OTT the host buffered before the view mounted', async () => {
    takePendingDeepLink.mockResolvedValue('calendium://auth/callback?ott=Cold42');
    render(<SignInView />);
    await waitFor(() => expect(verifyOtt).toHaveBeenCalledWith('Cold42'));
    expect(takePendingDeepLink).toHaveBeenCalledWith('auth');
  });

  it('redeems the OTT from a calendium://auth link in any scheme case', async () => {
    render(<SignInView />);
    for (const h of deepLinkHandlers) h('CALENDIUM://auth/callback?ott=AbC123');
    await waitFor(() => expect(verifyOtt).toHaveBeenCalledWith('AbC123'));
  });

  it.each(['https://evil.example/auth/callback?ott=x', 'calendiumx://auth/callback?ott=x', 'calendium://accounts/connected?ott=x'])(
    'ignores %j',
    async (url) => {
      render(<SignInView />);
      for (const h of deepLinkHandlers) h(url);
      await new Promise((r) => setTimeout(r, 0));
      expect(verifyOtt).not.toHaveBeenCalled();
    }
  );
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
