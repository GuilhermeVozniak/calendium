import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { disableWebPush, enableWebPush, isWebPushSupported } from '@/lib/web-push';

// ---------------------------------------------------------------------------
// Test doubles for the browser Push/Notification/service-worker surface. jsdom
// implements none of these, so every test installs exactly the globals it
// needs and tears them down afterward.
// ---------------------------------------------------------------------------

function definePatchable(target: object, key: string, value: unknown) {
  Object.defineProperty(target, key, { value, configurable: true, writable: true });
}

function clearPatchable(target: object, key: string) {
  if (Object.prototype.hasOwnProperty.call(target, key)) {
    Reflect.deleteProperty(target, key);
  }
}

afterEach(() => {
  clearPatchable(navigator, 'serviceWorker');
  clearPatchable(window, 'PushManager');
  clearPatchable(window, 'Notification');
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('isWebPushSupported', () => {
  it('is false in a plain jsdom environment (no Push APIs)', () => {
    expect(isWebPushSupported()).toBe(false);
  });

  it('is true once serviceWorker, PushManager, and Notification are all present', () => {
    definePatchable(navigator, 'serviceWorker', {});
    definePatchable(window, 'PushManager', class {});
    definePatchable(window, 'Notification', {});
    expect(isWebPushSupported()).toBe(true);
  });

  it('is false when only some of the required APIs are present', () => {
    definePatchable(navigator, 'serviceWorker', {});
    definePatchable(window, 'PushManager', class {});
    // Notification intentionally left undefined.
    expect(isWebPushSupported()).toBe(false);
  });
});

describe('enableWebPush / disableWebPush', () => {
  const REGISTER_DEVICE_RESPONSE = { id: 'device-123', platform: 'web', token: 'sub-json' };
  // base64url encoding of [0,1,2,3,4,5,6,7,8,9,10,255,254,253] — exercises the
  // '-'/'_' substitution and padding restoration branches of the private
  // urlBase64ToUint8Array() helper (not exported, so verified indirectly via
  // the bytes actually handed to PushManager.subscribe()).
  const VAPID_KEY = 'AAECAwQFBgcICQr__v0';
  const EXPECTED_BYTES = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 255, 254, 253];

  let fetchMock: ReturnType<typeof vi.fn>;
  let subscribeMock: ReturnType<typeof vi.fn>;
  let getSubscriptionMock: ReturnType<typeof vi.fn>;
  let unsubscribeMock: ReturnType<typeof vi.fn>;
  let getRegistrationMock: ReturnType<typeof vi.fn>;
  let registerMock: ReturnType<typeof vi.fn>;
  let requestPermissionMock: ReturnType<typeof vi.fn>;
  let mockSubscription: { endpoint: string; unsubscribe: ReturnType<typeof vi.fn> };
  let mockRegistration: { pushManager: { getSubscription: ReturnType<typeof vi.fn>; subscribe: ReturnType<typeof vi.fn> } };

  beforeEach(() => {
    unsubscribeMock = vi.fn(async () => true);
    mockSubscription = { endpoint: 'https://push.example.com/abc', unsubscribe: unsubscribeMock };

    getSubscriptionMock = vi.fn(async () => null);
    subscribeMock = vi.fn(async () => mockSubscription);
    mockRegistration = {
      pushManager: { getSubscription: getSubscriptionMock, subscribe: subscribeMock },
    };

    getRegistrationMock = vi.fn(async () => undefined);
    registerMock = vi.fn(async () => mockRegistration);
    requestPermissionMock = vi.fn(async () => 'granted');

    definePatchable(navigator, 'serviceWorker', {
      getRegistration: getRegistrationMock,
      register: registerMock,
      ready: Promise.resolve(mockRegistration),
    });
    definePatchable(window, 'PushManager', class {});
    definePatchable(window, 'Notification', {});
    vi.stubGlobal('Notification', { requestPermission: requestPermissionMock });

    fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/token') {
        return new Response(JSON.stringify({ token: 'test-jwt' }), { status: 200 });
      }
      if (url === '/v1/devices' && init?.method === 'POST') {
        return new Response(JSON.stringify(REGISTER_DEVICE_RESPONSE), { status: 200 });
      }
      if (url.startsWith('/v1/devices/') && init?.method === 'DELETE') {
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unhandled fetch in test: ${init?.method ?? 'GET'} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
  });

  describe('enableWebPush', () => {
    it('throws when push is not supported', async () => {
      clearPatchable(window, 'Notification');
      await expect(enableWebPush(VAPID_KEY)).rejects.toThrow(
        'Push notifications are not supported in this browser.',
      );
    });

    it('throws when notification permission is not granted', async () => {
      requestPermissionMock.mockResolvedValueOnce('denied');
      await expect(enableWebPush(VAPID_KEY)).rejects.toThrow(
        'Notification permission was not granted.',
      );
    });

    it('registers a new service worker when none exists yet', async () => {
      await enableWebPush(VAPID_KEY);
      expect(getRegistrationMock).toHaveBeenCalledTimes(1);
      expect(registerMock).toHaveBeenCalledWith('/sw.js');
    });

    it('reuses an existing service worker registration without re-registering', async () => {
      getRegistrationMock.mockResolvedValueOnce(mockRegistration);
      await enableWebPush(VAPID_KEY);
      expect(registerMock).not.toHaveBeenCalled();
    });

    it('subscribes with the decoded VAPID key when there is no existing subscription', async () => {
      await enableWebPush(VAPID_KEY);
      expect(subscribeMock).toHaveBeenCalledTimes(1);
      const [{ userVisibleOnly, applicationServerKey }] = subscribeMock.mock.calls[0]!;
      expect(userVisibleOnly).toBe(true);
      expect(Array.from(applicationServerKey as Uint8Array)).toEqual(EXPECTED_BYTES);
    });

    it('reuses an existing push subscription instead of subscribing again', async () => {
      getSubscriptionMock.mockResolvedValueOnce(mockSubscription);
      await enableWebPush(VAPID_KEY);
      expect(subscribeMock).not.toHaveBeenCalled();
    });

    it('registers the subscription with the backend and returns the device id', async () => {
      const deviceId = await enableWebPush(VAPID_KEY);
      expect(deviceId).toBe('device-123');
      const postCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'POST');
      expect(postCall).toBeDefined();
      const [, init] = postCall!;
      expect(String(postCall![0])).toBe('/v1/devices');
      const body = JSON.parse((init as RequestInit).body as string) as {
        platform: string;
        token: string;
      };
      expect(body.platform).toBe('web');
      expect(JSON.parse(body.token)).toEqual({ endpoint: mockSubscription.endpoint });
    });
  });

  describe('disableWebPush', () => {
    it('does nothing when push is unsupported and no deviceId is given', async () => {
      clearPatchable(window, 'Notification');
      await expect(disableWebPush()).resolves.toBeUndefined();
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it('unsubscribes locally when a push subscription exists', async () => {
      getSubscriptionMock.mockResolvedValueOnce(mockSubscription);
      await disableWebPush();
      expect(unsubscribeMock).toHaveBeenCalledTimes(1);
    });

    it('does not call unsubscribe when there is no active subscription', async () => {
      await disableWebPush();
      expect(unsubscribeMock).not.toHaveBeenCalled();
    });

    it('removes the device from the backend when a deviceId is given', async () => {
      await disableWebPush('device-123');
      const deleteCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'DELETE');
      expect(deleteCall).toBeDefined();
      expect(String(deleteCall![0])).toBe('/v1/devices/device-123');
    });

    it('does not call the backend when no deviceId is given', async () => {
      await disableWebPush(null);
      const deleteCall = fetchMock.mock.calls.find(([, init]) => init?.method === 'DELETE');
      expect(deleteCall).toBeUndefined();
    });
  });
});
