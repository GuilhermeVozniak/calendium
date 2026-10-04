/**
 * Typed wrapper around the Wails v2 JS bridge (window.go / window.runtime).
 *
 * In `wails dev` / a packaged build the bridge is injected by the Go host; in
 * a plain browser (`bun run dev` via vite) every call no-ops (or degrades to a
 * sensible web behavior) so the frontend runs standalone.
 */

/**
 * Result of the host's daily GitHub release check (apps/desktop/update.go):
 * the update-available event payload and the GetUpdateStatus return value.
 */
export interface UpdateInfo {
  available: boolean;
  current: string;
  latest: string;
  url: string;
}

/** Bound methods of the Go `App` struct (apps/desktop/app.go). */
export interface DesktopBindings {
  /** Opens a URL in the system default browser (web billing et al.). */
  OpenExternal(url: string): Promise<void>;
  GetAppVersion(): Promise<string>;
  /** Last update-check result; safe to call before any check ran. */
  GetUpdateStatus(): Promise<UpdateInfo>;
  /**
   * Resumes (true) or pauses (false) the host's update check. The host starts
   * paused because it cannot read the demo flag; App.tsx passes !demoMode.
   */
  SetUpdateChecksEnabled(enabled: boolean): Promise<void>;
  /**
   * Returns and clears the calendium://<route> link that cold-started the app
   * (buffered before any view subscribed), or ''. See lib/deep-link.ts.
   */
  TakePendingDeepLink(route: string): Promise<string>;
  /** Registers/unregisters the system-wide hotkeys in the Go host (Task 9). */
  SetGlobalShortcutsEnabled(enabled: boolean): Promise<void>;
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
  WindowShow(): void;
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
  async GetUpdateStatus() {
    return { available: false, current: 'dev (browser)', latest: '', url: '' };
  },
  async SetUpdateChecksEnabled() {
    // No host update check in a plain browser.
  },
  async TakePendingDeepLink() {
    return ''; // No OS deep links reach a plain browser.
  },
  async SetGlobalShortcutsEnabled() {
    // No host to register system-wide hotkeys in a plain browser.
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
  WindowShow() {
    // A browser tab is already "shown"; nothing to do.
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

/** Event the Go host emits (apps/desktop/update.go) when a newer release exists. */
export const UPDATE_EVENT = 'update-available';

/**
 * Subscribe to newer-release notifications. Only well-formed, available
 * payloads reach the handler. Returns an unsubscribe; no-ops in a browser.
 */
export function onUpdateAvailable(handler: (info: UpdateInfo) => void): () => void {
  return wailsRuntime.EventsOn(UPDATE_EVENT, (...data: unknown[]) => {
    const payload = data[0];
    if (typeof payload !== 'object' || payload === null) return;
    const info = payload as Partial<UpdateInfo>;
    if (info.available !== true || typeof info.latest !== 'string' || typeof info.url !== 'string') {
      return;
    }
    handler({
      available: true,
      current: typeof info.current === 'string' ? info.current : '',
      latest: info.latest,
      url: info.url,
    });
  });
}

// --- Task 9: system-wide global shortcuts ----------------------------------

/**
 * Event the Go host emits (apps/desktop/hotkeys.go) when a system-wide hotkey
 * fires. Payload: `{"action":"compose"|"search"}`.
 */
export const GLOBAL_SHORTCUT_EVENT = 'global-shortcut';

export type GlobalShortcutAction = 'compose' | 'search';

const GLOBAL_SHORTCUTS_STORAGE_KEY = 'calendium.global-shortcuts';

/** Persisted Settings toggle for the system-wide shortcuts (default on). */
export function globalShortcutsEnabled(): boolean {
  try {
    const stored = localStorage.getItem(GLOBAL_SHORTCUTS_STORAGE_KEY);
    return stored == null ? true : stored === 'true';
  } catch {
    return true;
  }
}

/**
 * Persists the toggle and tells the Go host to (un)register the hotkeys. The
 * host cannot read localStorage, so App.tsx calls this once on boot with the
 * stored value — that is how "enabled at startup" reaches the host.
 */
export async function setGlobalShortcutsEnabled(enabled: boolean): Promise<void> {
  try {
    localStorage.setItem(GLOBAL_SHORTCUTS_STORAGE_KEY, String(enabled));
  } catch {
    // Persistence is best-effort; still flip the host registration.
  }
  await desktop.SetGlobalShortcutsEnabled(enabled);
}

/**
 * Subscribe to system-wide shortcuts forwarded by the host. Returns an
 * unsubscribe function; no-ops in a plain browser.
 */
export function onGlobalShortcut(handler: (action: GlobalShortcutAction) => void): () => void {
  return wailsRuntime.EventsOn(GLOBAL_SHORTCUT_EVENT, (...data: unknown[]) => {
    const payload = data[0];
    if (typeof payload !== 'object' || payload === null) return;
    const action = (payload as { action?: unknown }).action;
    if (action === 'compose' || action === 'search') handler(action);
  });
}
