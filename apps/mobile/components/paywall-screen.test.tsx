const mockSignOut = jest.fn();
const mockSignOutLocally = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: () => ({ signOut: mockSignOut, signOutLocally: mockSignOutLocally, signInWithOAuth: jest.fn() }),
}));

const mockSuspendApi = jest.fn();
jest.mock('@/lib/api', () => ({ suspendApi: () => mockSuspendApi(), resumeApi: jest.fn() }));
jest.mock('@/hooks/use-push-registration', () => ({ unregisterPushDevice: jest.fn(async () => {}) }));
jest.mock('@/lib/query-client', () => ({ queryClient: { cancelQueries: jest.fn(async () => {}) } }));
const mockShareDataExport = jest.fn();
jest.mock('@/lib/data-export', () => ({
  shareDataExport: (...args: unknown[]) => mockShareDataExport(...args),
  describeExportError: () => 'Try again.',
}));

// Account & data (account lifecycle): export link-out + in-app deletion must
// stay reachable while paywalled.
const mockListAuthAccounts = jest.fn();
const mockDeleteUser = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: () => ({
    config: { mode: 'cloud', webUrl: 'https://web.example', features: { billing: true } },
    authClient: {
      listAccounts: (...args: unknown[]) => mockListAuthAccounts(...args),
      deleteUser: (...args: unknown[]) => mockDeleteUser(...args),
    },
  }),
  webOrigin: () => 'https://web.example',
}));

const mockOpenURL = jest.fn();
jest.mock('expo-linking', () => ({
  openURL: (...args: unknown[]) => mockOpenURL(...args),
}));

const mockReplace = jest.fn();
jest.mock('expo-router', () => ({
  useRouter: () => ({ replace: mockReplace, push: jest.fn() }),
}));

const mockOpenBrowser = jest.fn();
jest.mock('expo-web-browser', () => ({
  openBrowserAsync: (...args: unknown[]) => mockOpenBrowser(...args),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { act, fireEvent, render, screen } from '@testing-library/react-native';
import { Alert } from 'react-native';
import { PaywallScreen } from './paywall-screen';

/** App Store 3.1.1/3.1.3: no price, purchase CTA or purchase/billing link. */
const STORE_FORBIDDEN = /\$|price|subscribe|pricing|checkout/i;

beforeEach(() => {
  jest.clearAllMocks();
  jest.spyOn(Alert, 'alert').mockImplementation(() => undefined);
  mockListAuthAccounts.mockResolvedValue({ data: [{ id: 'acc1', providerId: 'credential' }], error: null });
  mockDeleteUser.mockResolvedValue({ data: { success: true }, error: null });
  mockSignOut.mockResolvedValue(undefined);
  mockSignOutLocally.mockResolvedValue(undefined);
  mockShareDataExport.mockResolvedValue(undefined);
});

async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
}

describe('PaywallScreen (mobile, store-compliant)', () => {
  it('renders the neutral copy with a header-role title', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    expect(
      screen.getByRole('header', { name: "This account doesn't have an active subscription." })
    ).toBeTruthy();
    expect(
      screen.getByText(
        'Your mail and calendar keep syncing. Sign in with an account that has an active subscription to continue.'
      )
    ).toBeTruthy();
  });

  it('offers only Refresh, Sign out and Account & data', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(3);
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Account & data' })).toBeTruthy();
  });

  it('shows no price, purchase call to action or billing link', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    expect(screen.queryAllByText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByLabelText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByHintText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByRole('link')).toHaveLength(0);
    for (const b of screen.getAllByRole('button')) await fireEvent.press(b);
    expect(mockOpenBrowser).not.toHaveBeenCalled();
    // The expanded Account & data panel adds no price or purchase wording either.
    expect(screen.getByText('Download my data')).toBeTruthy();
    expect(screen.queryAllByText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });

  it('Refresh re-checks the subscription', async () => {
    const onRefresh = jest.fn();
    await render(<PaywallScreen onRefresh={onRefresh} />);
    await fireEvent.press(screen.getByText('Refresh'));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('signs out', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    await fireEvent.press(screen.getByText('Sign out'));
    expect(mockSignOut).toHaveBeenCalled();
  });

  it('keeps export and in-app deletion reachable while paywalled', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    await fireEvent.press(screen.getByText('Account & data'));
    await fireEvent.press(screen.getByText('Download my data'));
    await flush();
    // App Store ruling: the export arrives via the API and the share sheet,
    // never by linking out to the web app.
    expect(mockShareDataExport).toHaveBeenCalledTimes(1);
    expect(mockOpenURL).not.toHaveBeenCalled();

    await fireEvent.press(screen.getByText('Delete account'));
    await flush();
    await fireEvent.changeText(screen.getByPlaceholderText('Your password'), 'hunter2');
    await fireEvent.press(screen.getByText('Delete my account'));
    const calls = (Alert.alert as jest.Mock).mock.calls;
    const buttons = calls[calls.length - 1]?.[2] as { style?: string; onPress?: () => void }[];
    await act(async () => {
      await buttons.find((b) => b.style === 'destructive')?.onPress?.();
    });
    await flush();
    expect(mockSuspendApi).toHaveBeenCalled();
    expect(mockDeleteUser).toHaveBeenCalledWith({ password: 'hunter2' });
    expect(mockSignOutLocally).toHaveBeenCalled();
    expect(mockSignOut).not.toHaveBeenCalled();
    expect(mockReplace).toHaveBeenCalledWith('/');
  });
});
