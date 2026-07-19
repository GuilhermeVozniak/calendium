import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const updatePreferencesMock = vi.fn();
const getPreferencesMock = vi.fn();
vi.mock('@/lib/api', () => ({
  api: {
    updatePreferences: (...args: unknown[]) => updatePreferencesMock(...args),
    getPreferences: (...args: unknown[]) => getPreferencesMock(...args),
  },
}));

let demoMode = false;
vi.mock('@/lib/server-config', () => ({
  isDemoMode: () => demoMode,
}));

const toastMock = vi.fn();
vi.mock('@/lib/toast', () => ({
  toast: (...args: unknown[]) => toastMock(...args),
  errorMessage: (e: unknown) => (e instanceof Error ? e.message : 'error'),
}));

import {
  applyNamedTheme,
  getStoredNamedTheme,
  initNamedTheme,
  setNamedTheme,
  syncNamedThemeFromServer,
} from './named-theme';

beforeEach(() => {
  window.localStorage.clear();
  delete document.documentElement.dataset.theme;
  demoMode = false;
  updatePreferencesMock.mockResolvedValue({ theme: 'neutral' });
  getPreferencesMock.mockRejectedValue(new Error('offline'));
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('applyNamedTheme', () => {
  it('sets data-theme for a named palette and removes it for neutral', () => {
    applyNamedTheme('ocean');
    expect(document.documentElement.dataset.theme).toBe('ocean');
    applyNamedTheme('neutral');
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });
});

describe('getStoredNamedTheme / initNamedTheme', () => {
  it('returns neutral for absent or garbage values', () => {
    expect(getStoredNamedTheme()).toBe('neutral');
    window.localStorage.setItem('calendium-named-theme', 'purple');
    expect(getStoredNamedTheme()).toBe('neutral');
  });

  it('applies the stored theme at startup', () => {
    window.localStorage.setItem('calendium-named-theme', 'forest');
    initNamedTheme();
    expect(document.documentElement.dataset.theme).toBe('forest');
  });
});

describe('setNamedTheme', () => {
  it('applies, persists locally, and PUTs the preference to the server', () => {
    setNamedTheme('ocean');
    expect(document.documentElement.dataset.theme).toBe('ocean');
    expect(window.localStorage.getItem('calendium-named-theme')).toBe('ocean');
    expect(updatePreferencesMock).toHaveBeenCalledWith({ theme: 'ocean' });
  });

  it('keeps the local theme and toasts when the PUT fails (no fake success)', async () => {
    updatePreferencesMock.mockRejectedValue(new Error('500'));
    setNamedTheme('sunset');
    await vi.waitFor(() => expect(toastMock).toHaveBeenCalled());
    expect(toastMock).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'destructive' })
    );
    expect(document.documentElement.dataset.theme).toBe('sunset');
    expect(window.localStorage.getItem('calendium-named-theme')).toBe('sunset');
  });

  it('does not call the server in demo mode', () => {
    demoMode = true;
    setNamedTheme('forest');
    expect(updatePreferencesMock).not.toHaveBeenCalled();
    expect(document.documentElement.dataset.theme).toBe('forest');
  });
});

describe('syncNamedThemeFromServer', () => {
  it('applies and persists the server preference (server wins over localStorage)', async () => {
    window.localStorage.setItem('calendium-named-theme', 'ocean');
    getPreferencesMock.mockResolvedValue({ theme: 'sunset' });
    await expect(syncNamedThemeFromServer()).resolves.toBe('sunset');
    expect(document.documentElement.dataset.theme).toBe('sunset');
    expect(window.localStorage.getItem('calendium-named-theme')).toBe('sunset');
  });

  it('returns null and keeps the local theme when the server is unreachable', async () => {
    window.localStorage.setItem('calendium-named-theme', 'ocean');
    applyNamedTheme('ocean');
    await expect(syncNamedThemeFromServer()).resolves.toBeNull();
    expect(document.documentElement.dataset.theme).toBe('ocean');
  });

  it('returns null in demo mode without touching the server', async () => {
    demoMode = true;
    await expect(syncNamedThemeFromServer()).resolves.toBeNull();
    expect(getPreferencesMock).not.toHaveBeenCalled();
  });
});
