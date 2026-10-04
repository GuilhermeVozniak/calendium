/**
 * calendium:// deep-link subscription for a single route ("auth" for the OTT
 * handoff in SignInView, "accounts" for the mailbox-connect return in
 * SettingsView).
 *
 * Warm path: the host emits the "deep-link" event. Cold path: a link that
 * opened the app arrived before any view subscribed, so the host buffers it
 * and this helper pulls it with TakePendingDeepLink right after subscribing
 * (consume-once on the host). Both paths go through deepLinkTo, the same
 * scheme/route rule, before the handler sees the link.
 */
import { deepLinkTo } from './links';
import { desktop, onDeepLink } from './wails';

export function subscribeDeepLink(route: string, handler: (link: string) => void): () => void {
  let active = true;
  const unsubscribe = onDeepLink((url) => {
    const link = deepLinkTo(url, route);
    if (link) handler(link);
  });
  desktop
    .TakePendingDeepLink(route)
    .then((url) => {
      const link = url ? deepLinkTo(url, route) : null;
      if (active && link) handler(link);
    })
    .catch(() => {
      // No binding (older host) or a host error: nothing pending to deliver.
    });
  return () => {
    active = false;
    unsubscribe();
  };
}
