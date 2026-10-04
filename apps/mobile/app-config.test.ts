import fs from 'node:fs';
import path from 'node:path';

import type { ExpoConfig } from 'expo/config';

// Loads app.config.js over the static app.json exactly as `expo config` does:
// Expo reads app.json first and passes it to the dynamic config as `config`.
const staticConfig = JSON.parse(fs.readFileSync(path.join(__dirname, 'app.json'), 'utf8'))
  .expo as ExpoConfig;
const dynamicConfig = require('./app.config.js') as (arg: { config: ExpoConfig }) => ExpoConfig;
const config = dynamicConfig({ config: staticConfig });

function pluginOptions(name: string): Record<string, unknown> {
  const entry = (config.plugins ?? []).find((p) => Array.isArray(p) && p[0] === name);
  if (!Array.isArray(entry) || entry.length < 2) throw new Error(`plugin ${name} is not configured`);
  return entry[1] as Record<string, unknown>;
}

/** Width/height straight from the PNG IHDR chunk (no image library needed). */
function pngSize(relative: string): { width: number; height: number } {
  const buf = fs.readFileSync(path.join(__dirname, relative));
  expect(buf.subarray(1, 4).toString('ascii')).toBe('PNG');
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
}

describe('app.json identity', () => {
  it('carries the product name, slug, scheme and bundle ids', () => {
    expect(config.name).toBe('Calendium');
    expect(config.slug).toBe('calendium');
    expect(config.scheme).toBe('calendium');
    expect(config.ios?.bundleIdentifier).toBe('app.calendium.mobile');
    expect(config.android?.package).toBe('app.calendium.mobile');
  });

  it('has a plain X.Y.Z version (set-version.mjs stamps it; stores reject anything else)', () => {
    expect(config.version).toMatch(/^\d+\.\d+\.\d+$/);
  });
});

describe('iOS Info.plist and privacy manifest', () => {
  it('declares non-exempt encryption false and nothing else', () => {
    expect(config.ios?.infoPlist).toEqual({ ITSAppUsesNonExemptEncryption: false });
  });

  it('declares no usage strings (the app imports no camera/photos/calendar/contacts/location module)', () => {
    const keys = Object.keys(config.ios?.infoPlist ?? {});
    expect(keys.filter((k) => k.endsWith('UsageDescription'))).toEqual([]);
  });

  it('ships the SDK 54 default required-reason API set and no tracking', () => {
    const manifest = config.ios?.privacyManifests as {
      NSPrivacyTracking: boolean;
      NSPrivacyAccessedAPITypes: { NSPrivacyAccessedAPIType: string; NSPrivacyAccessedAPITypeReasons: string[] }[];
    };
    expect(manifest.NSPrivacyTracking).toBe(false);
    const reasons = Object.fromEntries(
      manifest.NSPrivacyAccessedAPITypes.map((t) => [t.NSPrivacyAccessedAPIType, t.NSPrivacyAccessedAPITypeReasons])
    );
    expect(reasons).toEqual({
      NSPrivacyAccessedAPICategoryUserDefaults: ['CA92.1'],
      NSPrivacyAccessedAPICategoryFileTimestamp: ['C617.1'],
      NSPrivacyAccessedAPICategorySystemBootTime: ['35F9.1'],
      NSPrivacyAccessedAPICategoryDiskSpace: ['E174.1'],
    });
  });

  // An email/calendar client collects account and content data to function, so the
  // manifest must declare it (an empty array would contradict the App Store privacy
  // label). Every type is linked to the user, never used for tracking, and collected
  // only for app functionality.
  it('declares the data an email/calendar client collects, linked, untracked, for app functionality', () => {
    const manifest = config.ios?.privacyManifests as {
      NSPrivacyCollectedDataTypes: {
        NSPrivacyCollectedDataType: string;
        NSPrivacyCollectedDataTypeLinked: boolean;
        NSPrivacyCollectedDataTypeTracking: boolean;
        NSPrivacyCollectedDataTypePurposes: string[];
      }[];
    };
    const types = manifest.NSPrivacyCollectedDataTypes;
    expect(types.map((t) => t.NSPrivacyCollectedDataType)).toEqual([
      'NSPrivacyCollectedDataTypeEmailAddress',
      'NSPrivacyCollectedDataTypeName',
      'NSPrivacyCollectedDataTypeEmailsOrTextMessages',
      'NSPrivacyCollectedDataTypeOtherUserContent',
      'NSPrivacyCollectedDataTypeDeviceID',
    ]);
    for (const t of types) {
      expect(t).toEqual({
        NSPrivacyCollectedDataType: t.NSPrivacyCollectedDataType,
        NSPrivacyCollectedDataTypeLinked: true,
        NSPrivacyCollectedDataTypeTracking: false,
        NSPrivacyCollectedDataTypePurposes: ['NSPrivacyCollectedDataTypePurposeAppFunctionality'],
      });
    }
  });
});

describe('Android permissions', () => {
  it('requests only POST_NOTIFICATIONS and blocks the storage/overlay/alarm defaults', () => {
    expect(config.android?.permissions).toEqual(['android.permission.POST_NOTIFICATIONS']);
    expect(config.android?.blockedPermissions).toEqual([
      'android.permission.READ_EXTERNAL_STORAGE',
      'android.permission.WRITE_EXTERNAL_STORAGE',
      'android.permission.SYSTEM_ALERT_WINDOW',
      'android.permission.SCHEDULE_EXACT_ALARM',
    ]);
    expect(config.android?.adaptiveIcon?.backgroundColor).toBe('#0a0a0a');
  });
});

describe('assets', () => {
  it('uses the expo-splash-screen plugin instead of the legacy top-level splash block', () => {
    expect((config as { splash?: unknown }).splash).toBeUndefined();
    expect(pluginOptions('expo-splash-screen')).toEqual({
      image: './assets/images/splash-icon.png',
      imageWidth: 200,
      resizeMode: 'contain',
      backgroundColor: '#ffffff',
      dark: { image: './assets/images/splash-icon-dark.png', backgroundColor: '#0a0a0a' },
    });
    expect(pluginOptions('expo-notifications')).toEqual({
      icon: './assets/images/notification-icon.png',
      color: '#0a0a0a',
    });
  });

  it('every referenced asset exists at its expected pixel size', () => {
    const splash = pluginOptions('expo-splash-screen');
    const expected: [string, number][] = [
      [config.icon as string, 1024],
      [config.android?.adaptiveIcon?.foregroundImage as string, 1024],
      [splash.image as string, 1024],
      [(splash.dark as { image: string }).image, 1024],
      [pluginOptions('expo-notifications').icon as string, 96],
      [config.web?.favicon as string, 48],
    ];
    for (const [file, size] of expected) {
      expect([file, pngSize(file)]).toEqual([file, { width: size, height: size }]);
    }
  });

  it('ships no template art', () => {
    const files = fs.readdirSync(path.join(__dirname, 'assets/images'));
    expect(files.filter((f) => f.startsWith('react-native-reusables'))).toEqual([]);
    expect(files).not.toContain('splash.png');
  });
});
