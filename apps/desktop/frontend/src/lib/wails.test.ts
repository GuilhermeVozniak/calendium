import { afterEach, describe, expect, it, vi } from 'vitest';

// wails.ts computes `isDesktop` / `desktop` / `wailsRuntime` once, at module
// load, from window.go / window.runtime. jsdom's default `window` has neither
// defined, so a plain static import exercises the "plain browser" fallback
// path (the default `bun run dev` / test environment). The "running inside
// the Wails WebView" path is exercised below by seeding window.go/runtime
// *before* a fresh dynamic import (vi.resetModules()), since re-assigning
// those globals after the module has already loaded wouldn't change the
// already-computed top-level consts.

describe('wails.ts — browser fallback (no window.go/window.runtime)', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('reports isDesktop as false', async () => {
    const { isDesktop } = await import('./wails');
    expect(isDesktop).toBe(false);
  });

  it('OpenExternal falls back to window.open', async () => {
    const { desktop } = await import('./wails');
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
    await desktop.OpenExternal('https://example.com');
    expect(openSpy).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });

  it('GetAppVersion resolves to the dev/browser placeholder', async () => {
    const { desktop } = await import('./wails');
    await expect(desktop.GetAppVersion()).resolves.toBe('dev (browser)');
  });

  it('wailsRuntime.WindowSetTitle sets document.title', async () => {
    const { wailsRuntime } = await import('./wails');
    wailsRuntime.WindowSetTitle('Calendium — Inbox');
    expect(document.title).toBe('Calendium — Inbox');
  });

  it('wailsRuntime.BrowserOpenURL falls back to window.open', async () => {
    const { wailsRuntime } = await import('./wails');
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
    wailsRuntime.BrowserOpenURL('https://example.com');
    expect(openSpy).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });

  it('onDeepLink no-ops gracefully: EventsOn/EventsEmit never invoke a handler', async () => {
    const { onDeepLink } = await import('./wails');
    const handler = vi.fn();
    const unsubscribe = onDeepLink(handler);
    expect(typeof unsubscribe).toBe('function');
    // Calling the no-op unsubscribe must not throw.
    expect(() => unsubscribe()).not.toThrow();
    expect(handler).not.toHaveBeenCalled();
  });

  it('onGlobalShortcut no-ops gracefully and returns an unsubscribe', async () => {
    const { onGlobalShortcut } = await import('./wails');
    const handler = vi.fn();
    const unsubscribe = onGlobalShortcut(handler);
    expect(() => unsubscribe()).not.toThrow();
    expect(handler).not.toHaveBeenCalled();
  });

  it('wailsRuntime.WindowShow is a safe no-op in the browser', async () => {
    const { wailsRuntime } = await import('./wails');
    expect(() => wailsRuntime.WindowShow()).not.toThrow();
  });

  it('setGlobalShortcutsEnabled persists the toggle and resolves', async () => {
    const { globalShortcutsEnabled, setGlobalShortcutsEnabled } = await import('./wails');
    expect(globalShortcutsEnabled()).toBe(true); // default on
    await setGlobalShortcutsEnabled(false);
    expect(globalShortcutsEnabled()).toBe(false);
    await setGlobalShortcutsEnabled(true);
    expect(globalShortcutsEnabled()).toBe(true);
    localStorage.removeItem('calendium.global-shortcuts');
  });
});

describe('wails.ts — inside the Wails WebView (window.go/window.runtime present)', () => {
  afterEach(() => {
    delete (window as { go?: unknown }).go;
    delete (window as { runtime?: unknown }).runtime;
    vi.resetModules();
    vi.restoreAllMocks();
  });

  function installBridge() {
    const OpenExternal = vi.fn().mockResolvedValue(undefined);
    const GetAppVersion = vi.fn().mockResolvedValue('1.2.3');
    const SetGlobalShortcutsEnabled = vi.fn().mockResolvedValue(undefined);
    const EventsOn = vi.fn((_eventName: string, _cb: (...data: unknown[]) => void) => vi.fn());
    const EventsEmit = vi.fn();
    const WindowSetTitle = vi.fn();
    const WindowShow = vi.fn();
    const BrowserOpenURL = vi.fn();
    (window as unknown as { go: unknown }).go = {
      main: { App: { OpenExternal, GetAppVersion, SetGlobalShortcutsEnabled } },
    };
    (window as unknown as { runtime: unknown }).runtime = {
      EventsOn,
      EventsEmit,
      WindowSetTitle,
      WindowShow,
      BrowserOpenURL,
    };
    return {
      OpenExternal,
      GetAppVersion,
      SetGlobalShortcutsEnabled,
      EventsOn,
      EventsEmit,
      WindowSetTitle,
      WindowShow,
      BrowserOpenURL,
    };
  }

  it('reports isDesktop as true and exposes the bound Go App directly', async () => {
    const bridge = installBridge();
    vi.resetModules();
    const { isDesktop, desktop } = await import('./wails');
    expect(isDesktop).toBe(true);
    expect(desktop.OpenExternal).toBe(bridge.OpenExternal);
    expect(desktop.GetAppVersion).toBe(bridge.GetAppVersion);
  });

  it('onDeepLink subscribes via wailsRuntime.EventsOn on the deep-link event name', async () => {
    const bridge = installBridge();
    vi.resetModules();
    const { onDeepLink, DEEP_LINK_EVENT } = await import('./wails');
    const handler = vi.fn();
    onDeepLink(handler);
    expect(bridge.EventsOn).toHaveBeenCalledTimes(1);
    expect(bridge.EventsOn.mock.calls[0]?.[0]).toBe(DEEP_LINK_EVENT);
    expect(DEEP_LINK_EVENT).toBe('deep-link');
  });

  it('forwards a string payload from EventsOn to the handler', async () => {
    installBridge();
    vi.resetModules();
    const { onDeepLink } = await import('./wails');
    const runtime = (window as unknown as { runtime: { EventsOn: ReturnType<typeof vi.fn> } })
      .runtime;
    const handler = vi.fn();
    onDeepLink(handler);
    const registeredCallback = runtime.EventsOn.mock.calls[0]?.[1] as (
      ...data: unknown[]
    ) => void;
    registeredCallback('calendium://auth/callback?ott=abc123');
    expect(handler).toHaveBeenCalledWith('calendium://auth/callback?ott=abc123');
  });

  it('ignores a non-string payload from EventsOn', async () => {
    installBridge();
    vi.resetModules();
    const { onDeepLink } = await import('./wails');
    const runtime = (window as unknown as { runtime: { EventsOn: ReturnType<typeof vi.fn> } })
      .runtime;
    const handler = vi.fn();
    onDeepLink(handler);
    const registeredCallback = runtime.EventsOn.mock.calls[0]?.[1] as (
      ...data: unknown[]
    ) => void;
    registeredCallback(42);
    expect(handler).not.toHaveBeenCalled();
  });

  it('returns the unsubscribe function produced by EventsOn', async () => {
    const bridge = installBridge();
    const unsub = vi.fn();
    bridge.EventsOn.mockReturnValue(unsub);
    vi.resetModules();
    const { onDeepLink } = await import('./wails');
    const returned = onDeepLink(vi.fn());
    expect(returned).toBe(unsub);
  });

  describe('global shortcuts (Task 9)', () => {
    it('onGlobalShortcut subscribes via EventsOn on the global-shortcut event name', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onGlobalShortcut, GLOBAL_SHORTCUT_EVENT } = await import('./wails');
      onGlobalShortcut(vi.fn());
      expect(bridge.EventsOn).toHaveBeenCalledTimes(1);
      expect(bridge.EventsOn.mock.calls[0]?.[0]).toBe(GLOBAL_SHORTCUT_EVENT);
      expect(GLOBAL_SHORTCUT_EVENT).toBe('global-shortcut');
    });

    it('a fake host event with action "compose" dispatches compose to the handler', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onGlobalShortcut } = await import('./wails');
      // Stands in for App.tsx's listener that calls openCompose().
      const openComposeSpy = vi.fn();
      onGlobalShortcut((action) => {
        if (action === 'compose') openComposeSpy();
      });
      const registeredCallback = bridge.EventsOn.mock.calls[0]?.[1] as (
        ...data: unknown[]
      ) => void;
      registeredCallback({ action: 'compose' });
      expect(openComposeSpy).toHaveBeenCalledTimes(1);
    });

    it('forwards the "search" action', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onGlobalShortcut } = await import('./wails');
      const handler = vi.fn();
      onGlobalShortcut(handler);
      const registeredCallback = bridge.EventsOn.mock.calls[0]?.[1] as (
        ...data: unknown[]
      ) => void;
      registeredCallback({ action: 'search' });
      expect(handler).toHaveBeenCalledWith('search');
    });

    it('ignores malformed payloads', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onGlobalShortcut } = await import('./wails');
      const handler = vi.fn();
      onGlobalShortcut(handler);
      const registeredCallback = bridge.EventsOn.mock.calls[0]?.[1] as (
        ...data: unknown[]
      ) => void;
      registeredCallback('compose'); // bare string, not the payload object
      registeredCallback({ action: 'self-destruct' });
      registeredCallback({});
      registeredCallback(null);
      registeredCallback(42);
      registeredCallback();
      expect(handler).not.toHaveBeenCalled();
    });

    it('setGlobalShortcutsEnabled persists and calls the bound host method', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { setGlobalShortcutsEnabled, globalShortcutsEnabled } = await import('./wails');
      await setGlobalShortcutsEnabled(false);
      expect(bridge.SetGlobalShortcutsEnabled).toHaveBeenCalledWith(false);
      expect(globalShortcutsEnabled()).toBe(false);
      await setGlobalShortcutsEnabled(true);
      expect(bridge.SetGlobalShortcutsEnabled).toHaveBeenCalledWith(true);
      expect(globalShortcutsEnabled()).toBe(true);
      localStorage.removeItem('calendium.global-shortcuts');
    });
  });
});
