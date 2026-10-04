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

/** App Store 3.1.1/3.1.3: no price, purchase CTA or purchase/billing link. */
const STORE_FORBIDDEN = /\$|price|subscribe|pricing|checkout/i;

beforeEach(() => jest.clearAllMocks());

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

  it('offers only Refresh and Sign out', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeTruthy();
  });

  it('shows no price, purchase call to action or billing link', async () => {
    await render(<PaywallScreen onRefresh={jest.fn()} />);
    expect(screen.queryAllByText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByLabelText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByHintText(STORE_FORBIDDEN)).toHaveLength(0);
    expect(screen.queryAllByRole('link')).toHaveLength(0);
    for (const b of screen.getAllByRole('button')) await fireEvent.press(b);
    expect(mockOpenBrowser).not.toHaveBeenCalled();
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
});
