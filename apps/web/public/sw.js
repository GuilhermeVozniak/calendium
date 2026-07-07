/* Calendium web push service worker.
 *
 * The Go webpush sender (backend/internal/adapter/out/push/webpush.go) encrypts
 * a JSON payload of the shape { title, body, data } as the aes128gcm body, so
 * `event.data.json()` yields exactly that here. */

self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
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
