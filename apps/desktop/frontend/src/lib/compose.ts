import type { Message, Thread } from '@calendium/shared';

/**
 * How the composer opens: a blank new message, or a reply seeded from a thread
 * and the message being replied to.
 */
export type ComposeIntent =
  | { kind: 'new' }
  | { kind: 'reply'; thread: Thread; message: Message };

const COMPOSE_EVENT = 'calendium:compose';

/** Open the composer from anywhere (command palette, ThreadPane reply, etc.). */
export function openCompose(intent: ComposeIntent = { kind: 'new' }): void {
  window.dispatchEvent(new CustomEvent<ComposeIntent>(COMPOSE_EVENT, { detail: intent }));
}

/** Subscribe to compose-open requests; returns an unsubscribe function. */
export function onOpenCompose(handler: (intent: ComposeIntent) => void): () => void {
  const fn = (e: Event) => handler((e as CustomEvent<ComposeIntent>).detail);
  window.addEventListener(COMPOSE_EVENT, fn);
  return () => window.removeEventListener(COMPOSE_EVENT, fn);
}
