import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

type Connectivity = typeof import('@/lib/offline/connectivity');

/**
 * The tracker keeps module-level state (seeded from navigator.onLine at
 * import time), so every test gets a FRESH module instance via resetModules +
 * dynamic import, with navigator.onLine stubbed before the import runs.
 */
async function loadConnectivity(initialNavigatorOnline: boolean): Promise<Connectivity> {
  vi.resetModules();
  Object.defineProperty(window.navigator, 'onLine', {
    configurable: true,
    get: () => initialNavigatorOnline,
  });
  return await import('@/lib/offline/connectivity');
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('connectivity tracker', () => {
  it('seeds from navigator.onLine', async () => {
    const offlineStart = await loadConnectivity(false);
    expect(offlineStart.isOnline()).toBe(false);

    const onlineStart = await loadConnectivity(true);
    expect(onlineStart.isOnline()).toBe(true);
  });

  it("flips on window 'offline' and 'online' events", async () => {
    const conn = await loadConnectivity(true);

    window.dispatchEvent(new Event('offline'));
    expect(conn.isOnline()).toBe(false);

    window.dispatchEvent(new Event('online'));
    expect(conn.isOnline()).toBe(true);
  });

  it('reportApiReachable(false) forces offline even while navigator.onLine is true', async () => {
    const conn = await loadConnectivity(true);

    conn.reportApiReachable(false);
    expect(conn.isOnline()).toBe(false);

    conn.reportApiReachable(true);
    expect(conn.isOnline()).toBe(true);
  });

  it('browser back online while the API is down still counts as offline', async () => {
    const conn = await loadConnectivity(true);
    conn.reportApiReachable(false);
    window.dispatchEvent(new Event('offline'));
    window.dispatchEvent(new Event('online'));
    expect(conn.isOnline()).toBe(false);
  });

  it('notifies subscribers exactly once per EFFECTIVE change', async () => {
    const conn = await loadConnectivity(true);
    const seen: boolean[] = [];
    const unsubscribe = conn.subscribeOnline((online) => seen.push(online));

    window.dispatchEvent(new Event('offline'));
    window.dispatchEvent(new Event('offline')); // duplicate — no second notification
    conn.reportApiReachable(false); // already offline — effective value unchanged
    window.dispatchEvent(new Event('online')); // API still down — STILL offline, no notification
    conn.reportApiReachable(true); // now genuinely back online

    expect(seen).toEqual([false, true]);

    unsubscribe();
    window.dispatchEvent(new Event('offline'));
    expect(seen).toEqual([false, true]);
  });

  it('useOnline re-renders on connectivity changes', async () => {
    const conn = await loadConnectivity(true);
    const { result } = renderHook(() => conn.useOnline());
    expect(result.current).toBe(true);

    act(() => {
      window.dispatchEvent(new Event('offline'));
    });
    expect(result.current).toBe(false);

    act(() => {
      window.dispatchEvent(new Event('online'));
    });
    expect(result.current).toBe(true);
  });
});
