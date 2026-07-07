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
    const EventsOn = vi.fn((_eventName: string, _cb: (...data: unknown[]) => void) => vi.fn());
    const EventsEmit = vi.fn();
    const WindowSetTitle = vi.fn();
    const BrowserOpenURL = vi.fn();
    (window as unknown as { go: unknown }).go = { main: { App: { OpenExternal, GetAppVersion } } };
    (window as unknown as { runtime: unknown }).runtime = {
      EventsOn,
      EventsEmit,
      WindowSetTitle,
      BrowserOpenURL,
    };
    return { OpenExternal, GetAppVersion, EventsOn, EventsEmit, WindowSetTitle, BrowserOpenURL };
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
});
