jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock')
);

import AsyncStorage from '@react-native-async-storage/async-storage';
import {
  applyNamedTheme,
  getNamedTheme,
  loadStoredNamedTheme,
  navThemeFor,
  subscribeNamedTheme,
  THEME,
  THEMES,
} from './theme';

beforeEach(async () => {
  await AsyncStorage.clear();
  applyNamedTheme('neutral');
  jest.clearAllMocks();
});

describe('THEMES palette maps', () => {
  it('neutral is the base token set', () => {
    expect(THEMES.neutral).toBe(THEME);
  });

  it('named palettes carry the canonical primary tokens (same values as the CSS blocks)', () => {
    expect(THEMES.ocean.light.primary).toBe('hsl(217 72% 46%)');
    expect(THEMES.ocean.dark.primary).toBe('hsl(213 80% 66%)');
    expect(THEMES.forest.light.primary).toBe('hsl(158 55% 34%)');
    expect(THEMES.forest.dark.primary).toBe('hsl(152 45% 60%)');
    expect(THEMES.sunset.light.primary).toBe('hsl(24 82% 48%)');
    expect(THEMES.sunset.dark.primary).toBe('hsl(27 90% 62%)');
  });

  it('tokens not overridden by a named theme fall back to the neutral base', () => {
    expect(THEMES.ocean.light.card).toBe(THEME.light.card);
    expect(THEMES.forest.dark.foreground).toBe(THEME.dark.foreground);
    expect(THEMES.sunset.light.destructive).toBe(THEME.light.destructive);
  });
});

describe('named-theme store', () => {
  it('applyNamedTheme updates the store, notifies subscribers, and persists', async () => {
    const seen: string[] = [];
    const unsubscribe = subscribeNamedTheme(() => seen.push(getNamedTheme()));

    applyNamedTheme('ocean');
    expect(getNamedTheme()).toBe('ocean');
    expect(seen).toContain('ocean');
    await expect(AsyncStorage.getItem('calendium.namedTheme')).resolves.toBe('ocean');
    unsubscribe();
  });

  it('ignores unknown theme names', () => {
    applyNamedTheme('ocean');
    applyNamedTheme('purple' as never);
    expect(getNamedTheme()).toBe('ocean');
  });

  it('loadStoredNamedTheme restores the persisted value (offline fallback)', async () => {
    await AsyncStorage.setItem('calendium.namedTheme', 'forest');
    await expect(loadStoredNamedTheme()).resolves.toBe('forest');
    expect(getNamedTheme()).toBe('forest');
  });

  it('loadStoredNamedTheme ignores garbage and stays on the current theme', async () => {
    await AsyncStorage.setItem('calendium.namedTheme', 'bogus');
    await expect(loadStoredNamedTheme()).resolves.toBe('neutral');
  });
});

describe('navThemeFor', () => {
  it('derives the navigation theme from the named palette', () => {
    const nav = navThemeFor('sunset', 'dark');
    expect(nav.dark).toBe(true);
    expect(nav.colors.primary).toBe(THEMES.sunset.dark.primary);
    expect(nav.colors.background).toBe(THEMES.sunset.dark.background);
    expect(nav.colors.text).toBe(THEMES.sunset.dark.foreground);
  });

  it('neutral light matches the existing NAV_THEME values', () => {
    const nav = navThemeFor('neutral', 'light');
    expect(nav.colors.primary).toBe(THEME.light.primary);
    expect(nav.colors.background).toBe(THEME.light.background);
  });
});
