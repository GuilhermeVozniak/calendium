const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUsePushRegistration = jest.fn();
jest.mock('@/hooks/use-push-registration', () => ({
  usePushRegistration: (...args: unknown[]) => mockUsePushRegistration(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

jest.mock('nativewind', () => ({
  useColorScheme: () => ({ colorScheme: 'light' }),
}));

// lib/theme (named-theme store, M2.6 Task 13) persists to AsyncStorage, whose
// native module doesn't exist under Jest — same mock as settings.test.tsx.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

// Stands in for the real bottom-tab navigator: renders one Text node per
// screen actually passed as a child, keyed by its `name`. Good enough to
// assert which tabs the layout wires up without pulling in
// @react-navigation's full tab renderer.
const mockPush = jest.fn();
jest.mock('expo-router', () => {
  const React = require('react');
  const { View, Text } = require('react-native');
  function Tabs({ children }: { children: React.ReactNode }) {
    const items: React.ReactElement<{ name: string }>[] = React.Children.toArray(children).filter(
      (child: unknown): child is React.ReactElement<{ name: string }> => React.isValidElement(child)
    );
    return (
      <View>
        {items.map((child) => (
          <Text key={child.props.name}>{child.props.name}</Text>
        ))}
      </View>
    );
  }
  Tabs.Screen = () => null;
  return {
    Tabs,
    Redirect: () => null,
    useRouter: () => ({ push: mockPush }),
  };
});

import { render, screen } from '@testing-library/react-native';
import TabsLayout from './_layout';

const AI_ENABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: true, push: false } };
const AI_DISABLED_CONFIG = { features: { billing: false, google: false, microsoft: false, ai: false, push: false } };

beforeEach(() => {
  jest.clearAllMocks();
  mockUseAuth.mockReturnValue({ user: { id: 'u1', email: 'you@example.com' }, loading: false });
  mockUseServerConfig.mockReturnValue({ config: AI_ENABLED_CONFIG });
});

/**
 * Task 17 review fix: nothing previously covered the Ask AI tab-bar button's
 * visibility toggle (apps/mobile/app/(tabs)/_layout.tsx gates the `ask-ai`
 * Tabs.Screen behind `config.features.ai`) — a server that disables AI should
 * hide the entry point entirely, not just the screen behind it.
 *
 * `render()` in this @testing-library/react-native + React 19 setup returns a
 * thenable that must be awaited before `screen` queries reflect the mount
 * (see compose.test.tsx / ask-ai.test.tsx / classifiers.test.tsx, all of
 * which `await render(...)` for the same reason).
 */
describe('TabsLayout — Ask AI tab visibility', () => {
  it('shows the Ask AI tab-bar button when the server enables AI', async () => {
    await render(<TabsLayout />);

    expect(screen.getByText('ask-ai')).toBeTruthy();
  });

  it('hides the Ask AI tab-bar button when the server disables AI', async () => {
    mockUseServerConfig.mockReturnValue({ config: AI_DISABLED_CONFIG });
    await render(<TabsLayout />);

    expect(screen.queryByText('ask-ai')).toBeNull();
    // The always-present tabs are unaffected by the AI flag.
    expect(screen.getByText('inbox')).toBeTruthy();
    expect(screen.getByText('calendar')).toBeTruthy();
    expect(screen.getByText('tasks')).toBeTruthy();
    expect(screen.getByText('settings')).toBeTruthy();
  });
});
