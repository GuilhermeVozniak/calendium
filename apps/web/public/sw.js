/* Calendium service worker: web push + static-asset caching.
 *
 * Push: the Go webpush sender (backend/internal/adapter/out/push/webpush.go)
 * encrypts a JSON payload of the shape { title, body, data } as the aes128gcm
 * body, so `event.data.json()` yields exactly that here.
 *
 * Caching: static assets and the offline shell ONLY. API data lives in the
 * TanStack persisted cache — /v1 responses are NEVER cached here. The
 * classification logic below mirrors lib/sw-caching.ts (unit-tested there);
 * keep both in sync. sw.js stays dependency-free. */

const SW_CACHE = 'calendium-static-v1';
const OFFLINE_URL = '/offline';
const PRECACHE = [OFFLINE_URL, '/icon.svg', '/icon.png', '/manifest.webmanifest'];

/** Mirror of lib/sw-caching.ts#classifyRequest — keep in sync. */
function classifyRequest(url, method, hasAuth) {
  if (method.toUpperCase() !== 'GET' || hasAuth) return 'network-only';
  const path = url.pathname;
  if (path === '/v1' || path.startsWith('/v1/') || path.startsWith('/api/auth/')) {
    return 'network-only';
  }
  if (
    path.startsWith('/_next/static/') ||
    path.startsWith('/fonts/') ||
    /\.(png|svg|ico)$/.test(path)
  ) {
    return 'cache-first';
  }
  return 'navigation';
}

/** Mirror of lib/sw-caching.ts#isStaleCache — keep in sync. */
function isStaleCache(name) {
  return name.startsWith('calendium-static-') && name !== SW_CACHE;
}

async function cacheFirst(request) {
  const cached = await caches.match(request, { cacheName: SW_CACHE });
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok) {
    const cache = await caches.open(SW_CACHE);
    await cache.put(request, response.clone());
  }
  return response;
}

async function navigationNetworkFirst(request) {
  try {
    return await fetch(request);
  } catch {
    const offline = await caches.match(OFFLINE_URL, { cacheName: SW_CACHE });
    return offline || Response.error();
  }
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SW_CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const names = await caches.keys();
      await Promise.all(names.filter(isStaleCache).map((name) => caches.delete(name)));
      await self.clients.claim();
    })()
  );
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);
  // Never touch cross-origin requests (including the /v1 API host).
  if (url.origin !== self.location.origin) return;
  const strategy = classifyRequest(url, request.method, request.headers.has('authorization'));
  if (strategy === 'cache-first') {
    event.respondWith(cacheFirst(request));
  } else if (strategy === 'navigation' && request.mode === 'navigate') {
    event.respondWith(navigationNetworkFirst(request));
  }
  // network-only (and non-navigation leftovers): no respondWith — the browser
  // performs the request itself, so auth flows and test-level network
  // interception are untouched.
});

self.addEventListener('push', (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = { title: 'Calendium', body: event.data ? event.data.text() : '' };
  }
  const title = payload.title || 'Calendium';
  const options = {
    body: payload.body || '',
    icon: '/icon.png',
    badge: '/icon.png',
    tag: payload.data?.threadId || undefined,
    data: payload.data || {},
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const data = event.notification.data || {};
  const target = data.threadId ? `/mail?t=${data.threadId}` : '/mail';
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if ('focus' in client) {
          client.navigate(target);
          return client.focus();
        }
      }
      return self.clients.openWindow(target);
    })
  );
});
