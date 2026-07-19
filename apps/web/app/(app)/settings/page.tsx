// Thin route entry: the implementation lives in settings-page.tsx so it can
// legally export section components (e.g. AccountsSection) for unit tests —
// Next.js rejects extra named exports from page files at build time.
export { default } from './settings-page';
