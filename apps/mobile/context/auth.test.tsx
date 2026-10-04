import type { ReactNode } from 'react';

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  verifyEmailCallbackUrl: () => 'https://app.example.com/verify-email',
}));

const mockInvalidate = jest.fn();
const mockResumeApi = jest.fn();
jest.mock('@/lib/api', () => ({
  api: { invalidateAccessToken: () => mockInvalidate() },
  resumeApi: () => mockResumeApi(),
}));
const mockUnregisterPush = jest.fn(async () => {});
const mockForgetPush = jest.fn(async () => {});
jest.mock('@/hooks/use-push-registration', () => ({
  unregisterPushDevice: () => mockUnregisterPush(),
  forgetPushDevice: () => mockForgetPush(),
}));
const mockClearOffline = jest.fn(async () => {});
jest.mock('@/lib/offline', () => ({ clearOfflineState: () => mockClearOffline() }));
const mockQueryClear = jest.fn();
jest.mock('@/lib/query-client', () => ({ queryClient: { clear: () => mockQueryClear() } }));
const mockClearLocalAuth = jest.fn(async () => {});
jest.mock('@/lib/auth-client', () => ({ clearLocalAuthSession: () => mockClearLocalAuth() }));

import { act, renderHook } from '@testing-library/react-native';
import { Alert } from 'react-native';
import useAuth, { AuthProvider } from './auth';

const signUp = { email: jest.fn() };
const signIn = { email: jest.fn(), social: jest.fn() };
const getSession = jest.fn();
const signOutMock = jest.fn();
const authClient = { signUp, signIn, getSession, signOut: signOutMock };

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>;
}

/**
 * Mounts the provider and lets its initial getSession() settle. RNTL 14's
 * renderHook is async, and `waitFor` is unreliable in this jest-expo +
 * React 19 setup (see hooks/use-push-registration.test.ts), so a real tick
 * inside act() flushes the effect instead.
 */
async function mountAuth() {
  const hook = await renderHook(() => useAuth(), { wrapper });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  return hook;
}

/** The slice of a Better Auth onError context the retry-after capture reads. */
function retryAfterContext(seconds: string) {
  return {
    response: { headers: { get: (name: string) => (name.toLowerCase() === 'x-retry-after' ? seconds : null) } },
  };
}

beforeEach(() => {
  jest.clearAllMocks();
  jest.spyOn(Alert, 'alert').mockImplementation(() => {});
  getSession.mockResolvedValue({ data: null });
  mockUseServerConfig.mockReturnValue({
    authClient,
    config: { authBaseUrl: 'https://mail.example.com/api/auth', webUrl: 'https://app.example.com', demoMode: false },
    clear: jest.fn(),
  });
});

afterEach(() => {
  jest.restoreAllMocks();
});

describe('AuthProvider (piece 2)', () => {
  it('signUpWithEmail reports verificationRequired for token === null, sends the absolute callbackURL, and does not refresh', async () => {
    signUp.email.mockResolvedValue({ data: { token: null, user: { id: 'u1' } }, error: null });
    const { result } = await mountAuth();
    getSession.mockClear();
    let outcome: { verificationRequired: boolean } | undefined;
    await act(async () => {
      outcome = await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(outcome).toEqual({ verificationRequired: true });
    expect(signUp.email).toHaveBeenCalledWith(
      {
        name: 'Ada',
        email: 'ada@example.test',
        password: 'correct-horse-battery',
        callbackURL: 'https://app.example.com/verify-email',
      },
      expect.objectContaining({ onError: expect.any(Function) })
    );
    expect(getSession).not.toHaveBeenCalled();
    expect(result.current.user).toBeNull();
  });

  it('signUpWithEmail with a session refreshes and reports verificationRequired=false', async () => {
    signUp.email.mockResolvedValue({ data: { token: 'sess', user: { id: 'u1' } }, error: null });
    const { result } = await mountAuth();
    getSession.mockResolvedValue({ data: { user: { id: 'u1', email: 'ada@example.test', name: 'Ada', image: null } } });
    let outcome: { verificationRequired: boolean } | undefined;
    await act(async () => {
      outcome = await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(outcome).toEqual({ verificationRequired: false });
    expect(result.current.user?.email).toBe('ada@example.test');
  });

  it('signInWithEmail maps EMAIL_NOT_VERIFIED to the verify-first alert', async () => {
    signIn.email.mockResolvedValue({
      data: null,
      error: { status: 403, code: 'EMAIL_NOT_VERIFIED', message: 'Email not verified' },
    });
    const { result } = await mountAuth();
    await expect(result.current.signInWithEmail('ada@example.test', 'correct-horse-battery')).rejects.toThrow(
      'Verify your email first — we sent a new link.'
    );
    expect(Alert.alert).toHaveBeenCalledWith('Verify your email', 'Verify your email first — we sent a new link.');
  });

  it('signInWithEmail maps 429 to the X-Retry-After copy', async () => {
    signIn.email.mockImplementation(async (_body: unknown, fetchOptions: { onError: (ctx: unknown) => void }) => {
      fetchOptions.onError(retryAfterContext('42'));
      return { data: null, error: { status: 429, message: 'Too many requests. Please try again later.' } };
    });
    const { result } = await mountAuth();
    await expect(result.current.signInWithEmail('ada@example.test', 'correct-horse-battery')).rejects.toThrow(
      'Too many attempts, try again in 42 s'
    );
    expect(Alert.alert).toHaveBeenCalledWith('Error', 'Too many attempts, try again in 42 s');
  });

  it('signInWithEmail invalidates the cached API JWT before re-reading the session', async () => {
    signIn.email.mockResolvedValue({ data: { token: 'sess' }, error: null });
    const { result } = await mountAuth();
    getSession.mockClear();
    await act(async () => {
      await result.current.signInWithEmail('ada@example.test', 'correct-horse-battery');
    });
    expect(mockInvalidate).toHaveBeenCalledTimes(1);
    expect(mockInvalidate.mock.invocationCallOrder[0]).toBeLessThan(getSession.mock.invocationCallOrder[0]);
  });

  it('a failed signInWithEmail leaves the cached API JWT alone', async () => {
    signIn.email.mockResolvedValue({ data: null, error: { status: 401, message: 'Invalid email or password' } });
    const { result } = await mountAuth();
    await expect(result.current.signInWithEmail('ada@example.test', 'wrong')).rejects.toThrow();
    expect(mockInvalidate).not.toHaveBeenCalled();
  });

  it('signUpWithEmail with a session invalidates the cached API JWT; verification-required does not', async () => {
    signUp.email.mockResolvedValueOnce({ data: { token: null, user: { id: 'u1' } }, error: null });
    signUp.email.mockResolvedValueOnce({ data: { token: 'sess', user: { id: 'u1' } }, error: null });
    const { result } = await mountAuth();
    await act(async () => {
      await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(mockInvalidate).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.signUpWithEmail('Ada', 'ada@example.test', 'correct-horse-battery');
    });
    expect(mockInvalidate).toHaveBeenCalledTimes(1);
  });

  it('signInWithOAuth invalidates the cached API JWT on success', async () => {
    signIn.social.mockResolvedValue({ data: {}, error: null });
    const { result } = await mountAuth();
    await act(async () => {
      await result.current.signInWithOAuth('google');
    });
    expect(mockInvalidate).toHaveBeenCalledTimes(1);
  });

  it('signOut invalidates the cached API JWT', async () => {
    signOutMock.mockResolvedValue({});
    const { result } = await mountAuth();
    await act(async () => {
      await result.current.signOut();
    });
    expect(mockInvalidate).toHaveBeenCalledTimes(1);
  });

  it('every successful sign-in resumes an API suspended by account deletion', async () => {
    signIn.email.mockResolvedValue({ data: { token: 'sess' }, error: null });
    signIn.social.mockResolvedValue({ data: {}, error: null });
    const { result } = await mountAuth();
    await act(async () => {
      await result.current.signInWithEmail('ada@example.test', 'correct-horse-battery');
      await result.current.signInWithOAuth('google');
    });
    expect(mockResumeApi).toHaveBeenCalledTimes(2);
  });

  it('signOutLocally (after account deletion) clears every local cache with no request', async () => {
    getSession.mockResolvedValue({ data: { user: { id: 'u1', email: 'ada@example.test', name: 'Ada' } } });
    const { result } = await mountAuth();
    expect(result.current.user?.id).toBe('u1');
    getSession.mockClear();
    await act(async () => {
      await result.current.signOutLocally();
    });
    expect(result.current.user).toBeNull();
    expect(mockInvalidate).toHaveBeenCalled();
    expect(mockQueryClear).toHaveBeenCalled();
    expect(mockClearOffline).toHaveBeenCalled();
    expect(mockClearLocalAuth).toHaveBeenCalled();
    expect(mockForgetPush).toHaveBeenCalled();
    // No Better Auth or Go API call: the account no longer exists.
    expect(signOutMock).not.toHaveBeenCalled();
    expect(getSession).not.toHaveBeenCalled();
    expect(mockUnregisterPush).not.toHaveBeenCalled();
  });
});
