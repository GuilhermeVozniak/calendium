const path = require('node:path');

// react-native, react-test-renderer, and @testing-library/react-native are
// only ever hoisted to the workspace root (no nested copy), but apps/mobile
// pins an EXACT `react` version to match its Expo SDK build (required for
// real react-native compatibility — do not loosen that pin). When some other
// workspace package's looser `react` range resolves to a newer version, bun
// gives apps/mobile its own nested node_modules/react so the app keeps using
// the exact pin. That's correct for the shipped app, but under Jest it means
// react-native's hooks/contexts (root's react copy) and this package's own
// components (nested copy) are two different React instances — components
// using context (e.g. components/ui/text.tsx's TextClassContext) then crash.
// Force every `react` import inside Jest to the same, root-hoisted copy that
// react-native/react-test-renderer already use; this only affects the test
// run, not Metro's bundling of the real app.
const ROOT_NODE_MODULES = path.join(__dirname, '..', '..', 'node_modules');

/** @type {import('jest').Config} */
module.exports = {
  preset: 'jest-expo',
  // jest-expo's own transformIgnorePatterns allowlist assumes a classic flat
  // `node_modules/<pkg>` layout and only allows the *literal* package names
  // "expo"/"@expo" (not "expo-notifications", "expo-modules-core", etc.), which
  // breaks even under plain hoisted installs once jest-expo's own setup code
  // requires raw-source submodules like expo-modules-core/src/polyfill/*.ts.
  // Two adjustments on top of the upstream pattern:
  //  - `\.bun` is allowed so bun's content-addressable store dir
  //    (node_modules/.bun/<pkg>@<version>/node_modules/<pkg>/...) doesn't trip
  //    the ignore check before the regex ever reaches the real package name.
  //  - `expo`/`@expo` are turned into prefix matches (`expo[^/]*`) so every
  //    expo-* package (expo-router, expo-notifications, expo-modules-core, ...)
  //    is covered, not just the literal "expo" package. Over-matching here is
  //    safe — it just means a few extra files get run through babel-jest.
  //  - `@rn-primitives` is allowed so its ESM output (used by components/ui,
  //    e.g. text.tsx -> @rn-primitives/slot) gets transformed too; nothing
  //    exercised these components/ui modules through Jest before Task 17's
  //    component tests, so this gap was previously latent.
  transformIgnorePatterns: [
    '/node_modules/(?!(\\.bun|.pnpm|(jest-)?react-native|@react-native|@react-native-community|expo[^/]*|@expo[^/]*|@expo-google-fonts|react-navigation|@react-navigation|@sentry/react-native|native-base|@calendium|nativewind|react-native-css-interop|@better-auth|better-auth|@rn-primitives)/)',
    '/node_modules/react-native-reanimated/plugin/',
  ],
  setupFiles: ['<rootDir>/jest.setup.js'],
  testPathIgnorePatterns: ['/node_modules/', '<rootDir>/.expo/', '<rootDir>/docs/'],
  collectCoverageFrom: ['lib/**/*.ts', 'hooks/**/*.ts'],
  moduleNameMapper: {
    // See test/mocks/react-native-css-interop.js for why this is stubbed.
    '^react-native-css-interop$': '<rootDir>/test/mocks/react-native-css-interop.js',
    // See the ROOT_NODE_MODULES comment above: dedupes `react` to the copy
    // react-native/@testing-library already use, so contexts/hooks match.
    '^react$': path.join(ROOT_NODE_MODULES, 'react'),
    '^react/jsx-runtime$': path.join(ROOT_NODE_MODULES, 'react', 'jsx-runtime'),
    '^react/jsx-dev-runtime$': path.join(ROOT_NODE_MODULES, 'react', 'jsx-dev-runtime'),
  },
};
