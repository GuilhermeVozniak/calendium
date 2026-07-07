// --- Mocks -------------------------------------------------------------
// This hook sits at the top of a wide native-module fan-out (auth, server
// config, AsyncStorage, expo-constants/device/notifications, RN Alert). Every
// collaborator is mocked so the hook's own branching logic is what's under
// test, not any of those integrations.
const mockUseAuth = jest.fn();
jest.mock('@/context/auth', () => ({
  __esModule: true,
  default: (...args: unknown[]) => mockUseAuth(...args),
}));

const mockUseServerConfig = jest.fn();
jest.mock('@/lib/server-config', () => ({
  useServerConfig: (...args: unknown[]) => mockUseServerConfig(...args),
}));

const mockRegisterDevice = jest.fn();
const mockUnregisterDevice = jest.fn();
jest.mock('@/lib/api', () => ({
  api: {
    registerDevice: (...args: unknown[]) => mockRegisterDevice(...args),
    unregisterDevice: (...args: unknown[]) => mockUnregisterDevice(...args),
  },
}));

jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

const EXECUTION_ENVIRONMENT = { BARE: 'bare', STANDALONE: 'standalone', STORE_CLIENT: 'storeClient' };
jest.mock('expo-constants', () => ({
  __esModule: true,
  default: { executionEnvironment: 'bare' },
  ExecutionEnvironment: { Bare: 'bare', Standalone: 'standalone', StoreClient: 'storeClient' },
}));

// Namespace imports (`import * as Device from ...`) go through Babel's
// `_interopRequireWildcard`, which — under Metro's CJS interop mode — snapshots
// plain value properties into a new object at import time instead of live-
// binding them; only accessor (getter) properties are copied as live
// descriptors. `isDevice` is defined as a getter here so mutating
// `mockDeviceState.isDevice` per test is actually observed by the hook.
const mockDeviceState = { isDevice: true };
jest.mock('expo-device', () => ({
  get isDevice() {
    return mockDeviceState.isDevice;
  },
}));

const mockSetNotificationChannelAsync = jest.fn();
const mockGetPermissionsAsync = jest.fn();
const mockRequestPermissionsAsync = jest.fn();
const mockGetDevicePushTokenAsync = jest.fn();
jest.mock('expo-notifications', () => ({
  setNotificationChannelAsync: (...args: unknown[]) => mockSetNotificationChannelAsync(...args),
  getPermissionsAsync: (...args: unknown[]) => mockGetPermissionsAsync(...args),
  requestPermissionsAsync: (...args: unknown[]) => mockRequestPermissionsAsync(...args),
  getDevicePushTokenAsync: (...args: unknown[]) => mockGetDevicePushTokenAsync(...args),
  AndroidImportance: { HIGH: 4 },
}));

import { act, renderHook } from '@testing-library/react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';
import Constants from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { Alert, Platform } from 'react-native';
import { unregisterPushDevice, usePushRegistration } from './use-push-registration';

const DEVICE_ID_KEY = 'calendium.pushDeviceId';

const baseConfig = {
  features: { billing: false, google: false, microsoft: false, ai: false, push: true },
  demoMode: false,
};

let alertSpy: jest.SpyInstance;
let consoleWarnSpy: jest.SpyInstance;
const originalPlatformOS = Platform.OS;
const mutableConstants = Constants as unknown as { executionEnvironment: string };
void Device; // imported so the mocked module is exercised; state is driven via mockDeviceState

beforeEach(() => {
  jest.clearAllMocks();
  mockUseAuth.mockReturnValue({ isAuthenticated: true });
  mockUseServerConfig.mockReturnValue({ config: baseConfig });
  mockGetPermissionsAsync.mockResolvedValue({ status: 'granted' });
  mockRequestPermissionsAsync.mockResolvedValue({ status: 'granted' });
  mockGetDevicePushTokenAsync.mockResolvedValue({ data: 'native-token-abc' });
  mockRegisterDevice.mockResolvedValue({ id: 'device-server-id-1' });
  mutableConstants.executionEnvironment = EXECUTION_ENVIRONMENT.BARE;
  mockDeviceState.isDevice = true;
  Platform.OS = 'ios';
  alertSpy = jest.spyOn(Alert, 'alert').mockImplementation(() => {});
  consoleWarnSpy = jest.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(async () => {
  Platform.OS = originalPlatformOS;
  alertSpy.mockRestore();
  consoleWarnSpy.mockRestore();
  await AsyncStorage.clear();
});

/**
 * `waitFor` from @testing-library/react-native v14 never observes updates in
 * this jest-expo + React 19 setup (it polls but the mocked async chain never
 * appears to resolve from its perspective, timing out at the default 1000ms
 * every time — reproduced in isolation, not specific to one test here).
 * Driving a real macrotask tick inside `act()` reliably flushes the pending
 * promise chain from the hook's effect, so every test below flushes this way
 * and then asserts directly instead of polling.
 */
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });
}

describe('gating — never touches Notifications/permissions APIs', () => {
  it('does nothing when not authenticated', async () => {
    mockUseAuth.mockReturnValue({ isAuthenticated: false });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });

  it('does nothing on web (platform is neither ios nor android)', async () => {
    Platform.OS = 'web';
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });

  it('does nothing when the server does not advertise push support', async () => {
    mockUseServerConfig.mockReturnValue({
      config: { ...baseConfig, features: { ...baseConfig.features, push: false } },
    });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });

  it('does nothing in demo mode, even if push is otherwise enabled', async () => {
    mockUseServerConfig.mockReturnValue({ config: { ...baseConfig, demoMode: true } });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });

  it('does nothing when there is no server config at all', async () => {
    mockUseServerConfig.mockReturnValue({ config: null });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });
});

describe('Expo Go / simulator short-circuit', () => {
  it('no-ops silently in Expo Go (StoreClient execution environment)', async () => {
    mutableConstants.executionEnvironment = EXECUTION_ENVIRONMENT.STORE_CLIENT;
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
    expect(mockRegisterDevice).not.toHaveBeenCalled();
  });

  it('no-ops on a simulator (Device.isDevice === false)', async () => {
    mockDeviceState.isDevice = false;
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();
  });
});

describe('permission flow', () => {
  it('skips requestPermissionsAsync when already granted', async () => {
    mockGetPermissionsAsync.mockResolvedValue({ status: 'granted' });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockRegisterDevice).toHaveBeenCalled();
    expect(mockRequestPermissionsAsync).not.toHaveBeenCalled();
  });

  it('requests permission when not yet granted, and proceeds once the user grants it', async () => {
    mockGetPermissionsAsync.mockResolvedValue({ status: 'undetermined' });
    mockRequestPermissionsAsync.mockResolvedValue({ status: 'granted' });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockRegisterDevice).toHaveBeenCalled();
    expect(mockRequestPermissionsAsync).toHaveBeenCalledTimes(1);
  });

  it('stops (no token fetch, no registration) when the user denies the permission request', async () => {
    mockGetPermissionsAsync.mockResolvedValue({ status: 'undetermined' });
    mockRequestPermissionsAsync.mockResolvedValue({ status: 'denied' });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetDevicePushTokenAsync).not.toHaveBeenCalled();
    expect(mockRegisterDevice).not.toHaveBeenCalled();
  });
});

describe('Android channel setup', () => {
  it('creates the default notification channel before requesting permission', async () => {
    Platform.OS = 'android';
    const order: string[] = [];
    mockSetNotificationChannelAsync.mockImplementation(async () => {
      order.push('channel');
    });
    mockGetPermissionsAsync.mockImplementation(async () => {
      order.push('permissions');
      return { status: 'granted' };
    });

    await renderHook(() => usePushRegistration());
    await flush();

    expect(mockSetNotificationChannelAsync).toHaveBeenCalledWith(
      'default',
      expect.objectContaining({ name: 'Default', importance: Notifications.AndroidImportance.HIGH })
    );
    expect(order).toEqual(['channel', 'permissions']);
    expect(mockRegisterDevice).toHaveBeenCalledWith('android', 'native-token-abc');
  });

  it('does not set up a channel on iOS', async () => {
    Platform.OS = 'ios';
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockRegisterDevice).toHaveBeenCalled();
    expect(mockSetNotificationChannelAsync).not.toHaveBeenCalled();
  });
});

describe('successful registration', () => {
  it('registers the native device token and persists the returned device id', async () => {
    mockGetDevicePushTokenAsync.mockResolvedValue({ data: 'ios-token-xyz' });
    mockRegisterDevice.mockResolvedValue({ id: 'srv-device-42' });

    await renderHook(() => usePushRegistration());
    await flush();

    expect(mockRegisterDevice).toHaveBeenCalledWith('ios', 'ios-token-xyz');
    expect(await AsyncStorage.getItem(DEVICE_ID_KEY)).toBe('srv-device-42');
  });

  it('JSON-stringifies a non-string device token payload', async () => {
    mockGetDevicePushTokenAsync.mockResolvedValue({ data: { raw: 'android-fcm-blob' } });
    await renderHook(() => usePushRegistration());
    await flush();
    expect(mockRegisterDevice).toHaveBeenCalledWith(
      'ios',
      JSON.stringify({ raw: 'android-fcm-blob' })
    );
  });

  it('only ever attempts registration once, even across re-renders with the same deps', async () => {
    const { rerender } = await renderHook(() => usePushRegistration());
    await flush();
    expect(mockRegisterDevice).toHaveBeenCalledTimes(1);

    await rerender(undefined);
    await rerender(undefined);
    await flush();

    expect(mockGetPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(mockRegisterDevice).toHaveBeenCalledTimes(1);
  });

  it('re-attempts once push becomes enabled after an initial gated render', async () => {
    mockUseServerConfig.mockReturnValue({
      config: { ...baseConfig, features: { ...baseConfig.features, push: false } },
    });
    const { rerender } = await renderHook(() => usePushRegistration());
    await flush();
    expect(mockGetPermissionsAsync).not.toHaveBeenCalled();

    mockUseServerConfig.mockReturnValue({ config: baseConfig });
    await rerender(undefined);
    await flush();

    expect(mockRegisterDevice).toHaveBeenCalledTimes(1);
  });
});

describe('honest failure when no usable token can be obtained', () => {
  it('warns the user (dev alert) when getDevicePushTokenAsync throws', async () => {
    mockGetDevicePushTokenAsync.mockRejectedValue(new Error('no Firebase config'));
    await renderHook(() => usePushRegistration());
    await flush();

    expect(alertSpy).toHaveBeenCalledWith(
      'Push notifications unavailable',
      expect.stringContaining("couldn't be set up")
    );
    expect(mockRegisterDevice).not.toHaveBeenCalled();
  });

  it('warns the user when the resolved token is falsy (empty string)', async () => {
    mockGetDevicePushTokenAsync.mockResolvedValue({ data: '' });
    await renderHook(() => usePushRegistration());
    await flush();

    expect(alertSpy).toHaveBeenCalled();
    expect(mockRegisterDevice).not.toHaveBeenCalled();
  });
});

describe('best-effort server registration', () => {
  it('never throws out of the effect when registerDevice rejects', async () => {
    mockRegisterDevice.mockRejectedValue(new Error('API unreachable'));
    await renderHook(() => usePushRegistration());
    await flush();

    expect(consoleWarnSpy).toHaveBeenCalled();
    expect(consoleWarnSpy.mock.calls[0][0]).toContain('push registration skipped:');
    expect(await AsyncStorage.getItem(DEVICE_ID_KEY)).toBeNull();
  });
});

describe('unregisterPushDevice', () => {
  it('no-ops when no device id was ever persisted', async () => {
    await unregisterPushDevice();
    expect(mockUnregisterDevice).not.toHaveBeenCalled();
  });

  it('deletes the device server-side and clears the local id', async () => {
    await AsyncStorage.setItem(DEVICE_ID_KEY, 'srv-device-99');
    mockUnregisterDevice.mockResolvedValue(undefined);

    await unregisterPushDevice();

    expect(mockUnregisterDevice).toHaveBeenCalledWith('srv-device-99');
    expect(await AsyncStorage.getItem(DEVICE_ID_KEY)).toBeNull();
  });

  it('is best-effort: swallows errors from the server call without throwing', async () => {
    await AsyncStorage.setItem(DEVICE_ID_KEY, 'srv-device-99');
    mockUnregisterDevice.mockRejectedValue(new Error('network down'));

    await expect(unregisterPushDevice()).resolves.toBeUndefined();
    expect(consoleWarnSpy).toHaveBeenCalled();
    // Best-effort: the (still server-registered) id is left in place on failure
    // rather than being optimistically cleared.
    expect(await AsyncStorage.getItem(DEVICE_ID_KEY)).toBe('srv-device-99');
  });
});
