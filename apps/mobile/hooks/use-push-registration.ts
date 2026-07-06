import useAuth from '@/context/auth';
import { api } from '@/lib/api';
import AsyncStorage from '@react-native-async-storage/async-storage';
import Constants, { ExecutionEnvironment } from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { useEffect, useRef } from 'react';
import { Platform } from 'react-native';

/** AsyncStorage key holding the server-side NotificationDevice id for this install. */
const DEVICE_ID_KEY = 'calendium.pushDeviceId';

/**
 * Registers this device for push notifications once the user is signed in:
 * permission prompt → native APNs/FCM device token (Expo push token as a
 * fallback) → POST /v1/devices via ApiClient.registerDevice.
 *
 * Best-effort by design: silently no-ops in Expo Go (remote push unsupported
 * since SDK 53), on simulators, and on web; never blocks the UI on failure.
 */
export function usePushRegistration() {
  const { isAuthenticated } = useAuth();
  const attempted = useRef(false);

  useEffect(() => {
    const platform = Platform.OS;
    if (!isAuthenticated || attempted.current) return;
    if (platform !== 'ios' && platform !== 'android') return;
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

        // Native device token (APNs/FCM) — what backend/adapter/out/push expects.
        let token: string | null = null;
        try {
          const devicePushToken = await Notifications.getDevicePushTokenAsync();
          token =
            typeof devicePushToken.data === 'string'
              ? devicePushToken.data
              : JSON.stringify(devicePushToken.data);
        } catch {
          const expoPushToken = await Notifications.getExpoPushTokenAsync();
          token = expoPushToken.data;
        }
        if (!token) return;

        // Persist the returned device id so we can unregister it on sign-out.
        const device = await api.registerDevice(platform, token);
        await AsyncStorage.setItem(DEVICE_ID_KEY, device.id);
      } catch (error) {
        // Push registration is best-effort; never surface this to the user.
        console.warn('[calendium] push registration skipped:', error);
      }
    })();
  }, [isAuthenticated]);
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
