// Dynamic Expo config layered on top of the static app.json (Expo reads app.json
// first and passes it in as `config`).
//
// Android push needs the operator's own Firebase config, which is specific to
// their Firebase project and therefore NOT committed to this open-source tree
// (see docs/push.md). Hardcoding `android.googleServicesFile` would make
// `expo prebuild` / `eas build` FAIL whenever the file is absent, so instead we
// wire it only when the operator has actually supplied it:
//
//   * drop `google-services.json` into apps/mobile/ (the default path), or point
//     GOOGLE_SERVICES_JSON at it (e.g. an EAS secret file), and
//   * set EAS_PROJECT_ID so `eas build` links to the operator's Expo project.
//
// With none of these set the committed default still builds and push degrades
// honestly at runtime (getDevicePushTokenAsync throws -> notifyPushUnavailable in
// hooks/use-push-registration.ts), instead of advertising a dead feature.
const fs = require('node:fs');
const path = require('node:path');

const googleServicesPath = process.env.GOOGLE_SERVICES_JSON || './google-services.json';
const hasGoogleServices = fs.existsSync(path.resolve(__dirname, googleServicesPath));
const easProjectId = process.env.EAS_PROJECT_ID;

module.exports = ({ config }) => ({
  ...config,
  android: {
    ...config.android,
    // FCM registration tokens (getDevicePushTokenAsync on Android) require this
    // Firebase config at build time; omitted when the operator hasn't supplied it.
    ...(hasGoogleServices ? { googleServicesFile: googleServicesPath } : {}),
  },
  extra: {
    ...config.extra,
    // `eas build` links a build to an Expo project via this id; kept out of the
    // committed config so self-hosters build against their own Expo project.
    ...(easProjectId ? { eas: { ...config.extra?.eas, projectId: easProjectId } } : {}),
  },
});
