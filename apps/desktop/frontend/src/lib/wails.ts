/**
 * Typed wrapper around the Wails v2 JS bridge (window.go / window.runtime).
 *
 * In `wails dev` / a packaged build the bridge is injected by the Go host; in
 * a plain browser (`bun run dev` via vite) every call no-ops (or degrades to a
 * sensible web behavior) so the frontend runs standalone.
 */

/** Bound methods of the Go `App` struct (apps/desktop/app.go). */
export interface DesktopBindings {
  /** Opens a URL in the system default browser (Stripe checkout et al.). */
  OpenExternal(url: string): Promise<void>;
  GetAppVersion(): Promise<string>;
  /** Pushes the upcoming-events tray feed (JSON TrayEvent[]; lib/tray.ts). */
  SetUpcomingEvents(eventsJson: string): Promise<void>;
  /** Pushes the user's auto-join setting (Task 11; lib/tray.ts). */
  SetAutoJoin(enabled: boolean, leadSeconds: number): Promise<void>;
}

/** Subset of the Wails runtime API the app uses. */
export interface DesktopRuntime {
  EventsOn(eventName: string, callback: (...data: unknown[]) => void): () => void;
  EventsEmit(eventName: string, ...data: unknown[]): void;
  WindowSetTitle(title: string): void;
  BrowserOpenURL(url: string): void;
}

declare global {
  interface Window {
    go?: { main: { App: DesktopBindings } };
    runtime?: DesktopRuntime;
  }
}

/** True when running inside the Wails WebView (bridge present). */
export const isDesktop: boolean = typeof window !== 'undefined' && !!window.go?.main?.App;

const browserFallback: DesktopBindings = {
  async OpenExternal(url: string) {
    window.open(url, '_blank', 'noopener,noreferrer');
  },
  async GetAppVersion() {
    return 'dev (browser)';
  },
  // Tray + auto-join only exist in the Wails host; browser no-ops.
  async SetUpcomingEvents() {},
  async SetAutoJoin() {},
};

const runtimeFallback: DesktopRuntime = {
  EventsOn() {
    return () => {};
  },
  EventsEmit() {},
  WindowSetTitle(title: string) {
    document.title = title;
  },
  BrowserOpenURL(url: string) {
    window.open(url, '_blank', 'noopener,noreferrer');
  },
};

/** The bound Go App — always safe to call. */
export const desktop: DesktopBindings = isDesktop ? window.go!.main.App : browserFallback;

/** The Wails runtime — always safe to call. */
export const wailsRuntime: DesktopRuntime = window.runtime ?? runtimeFallback;

/** Event the Go host emits (runtime.EventsOn) on a calendium:// deep link. */
export const DEEP_LINK_EVENT = 'deep-link';

/**
 * Subscribe to calendium:// deep links forwarded by the host (OAuth OTT handoff,
 * mailbox-connect return). Returns an unsubscribe function; no-ops in a browser.
 */
export function onDeepLink(handler: (url: string) => void): () => void {
  return wailsRuntime.EventsOn(DEEP_LINK_EVENT, (...data: unknown[]) => {
    const url = data[0];
    if (typeof url === 'string') handler(url);
  });
}
