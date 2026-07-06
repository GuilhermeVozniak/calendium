'use client';

import { getApiClient } from '@/lib/api';

/**
 * Web Push subscription flow. Registers the service worker (public/sw.js),
 * subscribes to the browser's push service with the instance VAPID key, and
 * registers the subscription with the backend as a `web` device. The token is
 * the standard PushSubscription JSON ({ endpoint, keys: { p256dh, auth } }),
 * which is exactly what the Go webpush sender parses.
 */

export function isWebPushSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    'serviceWorker' in navigator &&
    'PushManager' in window &&
    'Notification' in window
  );
}

/** VAPID application-server keys are base64url; PushManager wants a Uint8Array. */
function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4);
  const normalized = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(normalized);
  const output = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i += 1) output[i] = raw.charCodeAt(i);
  return output;
}

async function registration(): Promise<ServiceWorkerRegistration> {
  const existing = await navigator.serviceWorker.getRegistration();
  if (existing) return existing;
  return navigator.serviceWorker.register('/sw.js');
}

export async function getPushSubscription(): Promise<PushSubscription | null> {
  if (!isWebPushSupported()) return null;
  const reg = await registration();
  return reg.pushManager.getSubscription();
}

/**
 * Requests permission, subscribes, and registers the device with the backend.
 * Returns the registered device id. Throws on permission denial or any API
 * failure so the caller can surface a real error (never a fake success).
 */
export async function enableWebPush(vapidPublicKey: string): Promise<string> {
  if (!isWebPushSupported()) {
    throw new Error('Push notifications are not supported in this browser.');
  }
  const permission = await Notification.requestPermission();
  if (permission !== 'granted') {
    throw new Error('Notification permission was not granted.');
  }
  const reg = await registration();
  await navigator.serviceWorker.ready;
  let subscription = await reg.pushManager.getSubscription();
  if (!subscription) {
    subscription = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(vapidPublicKey) as BufferSource,
    });
  }
  const device = await getApiClient().registerDevice('web', JSON.stringify(subscription));
  return device.id;
}

/** Unsubscribes locally and removes the device from the backend when known. */
export async function disableWebPush(deviceId?: string | null): Promise<void> {
  const subscription = await getPushSubscription();
  if (subscription) await subscription.unsubscribe();
  if (deviceId) await getApiClient().unregisterDevice(deviceId);
}
