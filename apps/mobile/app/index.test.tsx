jest.mock('@/assets/icons/apple.svg', () => ({ __esModule: true, default: () => null }));
jest.mock('@/assets/icons/google.svg', () => ({ __esModule: true, default: () => null }));

const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
  forgotPasswordUrl: (config: { webUrl?: string } | null) => (config?.webUrl ? `${config.webUrl}/forgot-password` : null),
}));

const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({ openURL: (...args: unknown[]) => mockOpenURL(...args) }));

jest.mock('expo-router', () => ({ Redirect: () => null, Stack: { Screen: () => null } }));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import SignInScreen from './index';

// Same tick-in-act() flush as compose.test.tsx / settings.test.tsx.
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

function authValue(overrides: Record<string, unknown> = {}) {
  return {
    user: null,
    loading: false,
    signInWithOAuth: jest.fn(),
    signInWithEmail: jest.fn(async () => {}),
    signUpWithEmail: jest.fn(async () => ({ verificationRequired: false })),
    ...overrides,
  };
}

beforeEach(() => {
  jest.clearAllMocks();
  mockOpenURL.mockResolvedValue(true);
  mockUseServerConfig.mockReturnValue({
    isConfigured: true,
    isLoading: false,
    config: { authProviders: ['email'], webUrl: 'https://app.example.com' },
  });
  mockUseAuth.mockReturnValue(authValue());
});

describe('SignInScreen (piece 2)', () => {
  it('"Forgot password?" opens <webUrl>/forgot-password in the system browser', async () => {
    await render(<SignInScreen />);
    await fireEvent.press(screen.getByText('Forgot password?'));
    expect(mockOpenURL).toHaveBeenCalledWith('https://app.example.com/forgot-password');
  });

  it('a verification-required sign-up flips to sign-in mode with the check-inbox notice', async () => {
    const signUpWithEmail = jest.fn(async () => ({ verificationRequired: true }));
    mockUseAuth.mockReturnValue(authValue({ signUpWithEmail }));
    await render(<SignInScreen />);
    await fireEvent.press(screen.getByText("Don't have an account? Create one"));
    expect(screen.queryByText('Forgot password?')).toBeNull();
    await fireEvent.changeText(screen.getByPlaceholderText('Name'), 'Ada');
    await fireEvent.changeText(screen.getByPlaceholderText('Email'), 'ada@example.test');
    await fireEvent.changeText(screen.getByPlaceholderText('Password'), 'correct-horse-battery');
    await fireEvent.press(screen.getByText('Create account'));
    await flush();
    expect(signUpWithEmail).toHaveBeenCalledWith('Ada', 'ada@example.test', 'correct-horse-battery');
    expect(screen.getByText('Check your inbox — we sent a verification link to ada@example.test.')).toBeTruthy();
    expect(screen.getByText('Sign in')).toBeTruthy();
    expect(screen.getByText('Forgot password?')).toBeTruthy();
  });
});
