import useAuth from '@/context/auth';
import { api } from '@/lib/api';
import { useServerConfig } from '@/lib/server-config';
import AsyncStorage from '@react-native-async-storage/async-storage';
import Constants, { ExecutionEnvironment } from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { useEffect, useRef } from 'react';
import { Alert, Platform } from 'react-native';

/** AsyncStorage key holding the server-side NotificationDevice id for this install. */
const DEVICE_ID_KEY = 'calendium.pushDeviceId';

/**
 * Surfaces a clear, one-time message when a native push token can't be obtained,
 * instead of failing silently. The backend delivers to Android via FCM v1 and to
 * iOS via APNs directly (backend/internal/adapter/out/push), so we register the
 * *native* device token from getDevicePushTokenAsync — never an Expo push token,
 * which those transports can't deliver to. On Android an FCM token requires the
 * operator's own Firebase config: drop `google-services.json` into apps/mobile/
 * (auto-wired by app.config.js) and rebuild. See apps/mobile/docs/push.md (client
 * setup) and docs/self-hosting/providers.md (server-side FCM/APNs keys).
 */
function notifyPushUnavailable(): void {
  const message =
    "Push notifications couldn't be set up on this build. Android needs the " +
    "operator's Firebase config — add apps/mobile/google-services.json (auto-wired " +
    'by app.config.js) and rebuild. See apps/mobile/docs/push.md.';
  console.warn('[calendium]', message);
  if (__DEV__) {
    // Visible in dev builds so the misconfiguration is caught before shipping.
    Alert.alert('Push notifications unavailable', message);
  }
}

/**
 * Registers this device for push notifications once the user is signed in:
 * permission prompt → native APNs/FCM device token (getDevicePushTokenAsync) →
 * POST /v1/devices via ApiClient.registerDevice. The backend's push adapter
 * talks to APNs/FCM directly, so only the native token is registered — an Expo
 * push token would be undeliverable, so we surface an honest failure rather than
 * registering a device that can never receive a notification.
 *
 * Best-effort by design: silently no-ops in Expo Go (remote push unsupported
 * since SDK 53), on simulators, and on web; never blocks the UI on failure.
 */
export function usePushRegistration() {
  const { isAuthenticated } = useAuth();
  const { config } = useServerConfig();
  const pushEnabled = config?.features?.push ?? false;
  const demoMode = config?.demoMode ?? false;
  const attempted = useRef(false);

  useEffect(() => {
    const platform = Platform.OS;
    if (!isAuthenticated || attempted.current) return;
    if (platform !== 'ios' && platform !== 'android') return;
    // Nothing to register against when the server doesn't offer push, or in the
    // offline demo (no backend). Gated on the /v1/instance features flag.
    if (!pushEnabled || demoMode) return;
    attempted.current = true;

    (async () => {
      try {
        const isExpoGo = Constants.executionEnvironment === ExecutionEnvironment.StoreClient;
        if (isExpoGo || !Device.isDevice) return;

        if (platform === 'android') {
          await Notifications.setNotificationChannelAsync('default', {
            name: 'Default',
            importance: Notifications.AndroidImportance.HIGH,
          });
        }

        let { status } = await Notifications.getPermissionsAsync();
        if (status !== 'granted') {
          ({ status } = await Notifications.requestPermissionsAsync());
        }
        if (status !== 'granted') return;

        // Native device token (APNs/FCM) — exactly what backend/adapter/out/push
        // delivers to. On Android getDevicePushTokenAsync throws without the
        // operator's Firebase config (google-services.json); degrade with a clear
        // message rather than registering an undeliverable token or failing
        // silently. (An Expo push token is deliberately NOT used as a fallback:
        // the backend has no Expo push transport, so it could never deliver to one.)
        let token: string | null = null;
        try {
          const devicePushToken = await Notifications.getDevicePushTokenAsync();
          token =
            typeof devicePushToken.data === 'string'
              ? devicePushToken.data
              : JSON.stringify(devicePushToken.data);
        } catch {
          notifyPushUnavailable();
          return;
        }
        if (!token) {
          notifyPushUnavailable();
          return;
        }

        // Persist the returned device id so we can unregister it on sign-out.
        const device = await api.registerDevice(platform, token);
        await AsyncStorage.setItem(DEVICE_ID_KEY, device.id);
      } catch (error) {
        // Server-side registration failure (e.g. API error) is best-effort.
        console.warn('[calendium] push registration skipped:', error);
      }
    })();
  }, [isAuthenticated, pushEnabled, demoMode]);
}

/**
 * Removes this device's push token server-side (DELETE /v1/devices/{id}) and
 * clears the locally persisted id. Call on sign-out so revoked sessions stop
 * receiving notifications. Best-effort: never throws, so it can't block sign-out.
 */
export async function unregisterPushDevice(): Promise<void> {
  try {
    const deviceId = await AsyncStorage.getItem(DEVICE_ID_KEY);
    if (!deviceId) return;
    await api.unregisterDevice(deviceId);
    await AsyncStorage.removeItem(DEVICE_ID_KEY);
  } catch (error) {
    // Unregistration is best-effort; never block sign-out on it.
    console.warn('[calendium] push unregistration skipped:', error);
  }
}
