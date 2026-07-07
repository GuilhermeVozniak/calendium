// Manual Jest mock for `react-native-css-interop` (nativewind's runtime).
//
// babel.config.js sets `jsxImportSource: 'nativewind'`, so babel-preset-expo
// rewrites *every* `React.createElement` call (not just literal JSX) into
// `_ReactNativeCSSInterop.createInteropElement`, injecting an unconditional
// `require("react-native-css-interop")` at the top of any transformed file
// that renders anything — including plain `.ts` modules like
// lib/server-config.ts, which uses `React.createElement` directly (see its
// "JSX-free" comment) even though it has no JSX syntax.
//
// The real module eagerly imports React Native's `Appearance` API at module
// load time to wire up native color-scheme observables, which needs a fuller
// native bootstrap than jest-expo's default mocks provide and isn't needed by
// any of the lib/hook unit tests in this project (none render actual styled
// views). This thin createElement-passthrough stub lets those modules load
// under Jest without pulling in nativewind's real native styling runtime.
const React = require('react');

module.exports = {
  createElement: React.createElement,
  createInteropElement: (type, props, ...children) =>
    React.createElement(type, props, ...children),
  cssInterop: () => {},
  remapProps: () => {},
  colorScheme: {
    get: () => 'light',
    set: () => {},
    addEventListener: () => ({ remove() {} }),
  },
  useColorScheme: () => ({
    colorScheme: 'light',
    setColorScheme: () => {},
    toggleColorScheme: () => {},
  }),
  useSafeAreaEnv: () => null,
  useUnstableNativeVariable: () => undefined,
  vars: (v) => v,
  rem: { get: () => 16, set: () => {} },
  StyleSheet: {
    create: (styles) => styles,
    register: () => {},
    registerCompiled: () => {},
    getGlobalStyle: () => undefined,
  },
  wrapJSX: (jsx) => jsx,
};
