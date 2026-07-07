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
  transformIgnorePatterns: [
    '/node_modules/(?!(\\.bun|.pnpm|(jest-)?react-native|@react-native|@react-native-community|expo[^/]*|@expo[^/]*|@expo-google-fonts|react-navigation|@react-navigation|@sentry/react-native|native-base|@calendium|nativewind|react-native-css-interop|@better-auth|better-auth)/)',
    '/node_modules/react-native-reanimated/plugin/',
  ],
  setupFiles: ['<rootDir>/jest.setup.js'],
  testPathIgnorePatterns: ['/node_modules/', '<rootDir>/.expo/', '<rootDir>/docs/'],
  collectCoverageFrom: ['lib/**/*.ts', 'hooks/**/*.ts'],
  moduleNameMapper: {
    // See test/mocks/react-native-css-interop.js for why this is stubbed.
    '^react-native-css-interop$': '<rootDir>/test/mocks/react-native-css-interop.js',
  },
};
