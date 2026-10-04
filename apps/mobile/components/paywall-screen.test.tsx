const mockSignOut = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: () => ({ signOut: mockSignOut }),
}));

const mockOpenBrowser = jest.fn();
jest.mock('expo-web-browser', () => ({
  openBrowserAsync: (...args: unknown[]) => mockOpenBrowser(...args),
}));

jest.mock('react-native-safe-area-context', () => ({
  useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }),
}));

import { fireEvent, render, screen } from '@testing-library/react-native';
import { PaywallScreen } from './paywall-screen';

beforeEach(() => jest.clearAllMocks());

describe('PaywallScreen (mobile, read-only)', () => {
  it.each([
    ['trial_ended', 'Your free trial has ended'],
    ['none', 'Subscribe to keep using Calendium'],
    ['canceled', 'Your subscription has ended'],
    ['past_due', 'Payment failed'],
    ['paused', 'Your subscription is paused'],
  ] as const)('renders %s copy with no purchase button', async (reason, title) => {
    await render(<PaywallScreen reason={reason} webUrl="https://web.example" />);
    expect(screen.getByText(title)).toBeTruthy();
    // The copy may *say* "Subscribe" (it points at the web), but the app never
    // sells: the only pressables are the web link and sign-out.
    expect(screen.queryByRole('button', { name: /subscribe/i })).toBeNull();
    expect(screen.getAllByRole('button')).toHaveLength(2);
    expect(screen.getByText('Manage on the web')).toBeTruthy();
  });

  it('opens <webUrl>/pricing in the browser', async () => {
    await render(<PaywallScreen reason="trial_ended" webUrl="https://web.example" />);
    await fireEvent.press(screen.getByText('Manage on the web'));
    expect(mockOpenBrowser).toHaveBeenCalledWith('https://web.example/pricing');
  });

  it('hides the web link when the server advertises no webUrl', async () => {
    await render(<PaywallScreen reason="none" webUrl={null} />);
    expect(screen.queryByText('Manage on the web')).toBeNull();
  });

  it('signs out', async () => {
    await render(<PaywallScreen reason="canceled" webUrl={null} />);
    await fireEvent.press(screen.getByText('Sign out'));
    expect(mockSignOut).toHaveBeenCalled();
  });
});
