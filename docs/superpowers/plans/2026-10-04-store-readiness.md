# Store and Release Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `vX.Y.Z` tag produces store-submittable mobile builds and version-stamped, deep-link-capable desktop installers carrying Calendium's real identity, with CI refusing to ship what `expo-doctor` or the macOS signing gate would reject.

**Architecture:** Mobile identity lives in `apps/mobile/app.json` + `eas.json` with raster assets generated from the single vector source `apps/web/public/icon.svg` by `scripts/gen-mobile-assets.mjs` (outputs committed, drift-tested). The tag is the single version source: `scripts/set-version.mjs` stamps `app.json`/`wails.json`, `-ldflags` stamp `main.version` (desktop) and `httpapi.Version` (API); a stdlib-only `apps/desktop/update.go` polls GitHub releases and the frontend shows a one-line banner. `release.yml` gains a `preflight` job that validates the version, refuses unsigned tags, and builds NSIS/Linux-desktop-entry artifacts. Identity strings (repo URL, support email, cloud hosts) become env-driven with today's values as defaults.

**Tech Stack:** Expo SDK 54 / EAS, bun workspaces (`overrides`), `sharp` 0.34.5 (node), Vitest 4 (`scripts/`), jest-expo, Wails v2.12.0 (Go 1.26 stdlib), NSIS (stock Wails template), GitHub Actions, Next.js 15 (`NEXT_PUBLIC_*` inlining), Go `-ldflags -X`.

**Spec:** docs/superpowers/specs/2026-10-04-store-readiness-design.md

## Global Constraints

- No database migration in this piece (no `backend/migrations` number consumed).
- Identifiers: mobile bundle id / package `app.calendium.mobile` (unchanged); desktop `CFBundleIdentifier` `app.calendium.desktop`; `CFBundleURLName` `app.calendium.desktop.{{.Scheme}}`; `SingleInstanceLock.UniqueId` `app.calendium.desktop`; URL scheme `calendium` everywhere.
- APNs default topic `app.calendium.mobile` (`backend/internal/adapter/out/push/apns.go`); `APNS_TOPIC` env override stays.
- The tag `vX.Y.Z` is the single source of truth. Desktop: `-ldflags "-X main.version=X.Y.Z"` into `var version = "dev"`; API: `-ldflags "-X calendium/backend/internal/adapter/in/httpapi.Version=${VERSION}"` into `var Version = "dev"` (Dockerfile `ARG VERSION=dev`; compose `VERSION: ${VERSION:-dev}`).
- `node scripts/set-version.mjs X.Y.Z` writes `apps/mobile/app.json` `expo.version` and `apps/desktop/wails.json` `info.productVersion`; `--check` exits 1 on drift; accepted input is exactly `^\d+\.\d+\.\d+$` (no `v`, no prerelease); formatting and key order of both files are preserved byte-for-byte except the stamped line.
- First tag after this piece: `v1.0.0` (mobile is already `1.0.0`; store versions cannot go backwards).
- EAS (`apps/mobile/eas.json`): `cli.version ">= 16.0.0"`, `cli.appVersionSource "remote"`; build profiles `development` (`developmentClient: true`, `distribution: internal`, `channel: development`), `preview` (`distribution: internal`, `channel: preview`), `production` (`distribution: store`, `channel: production`, `autoIncrement: true`); `submit.production.ios.appleTeamId "CT22R575UG"`, `submit.production.android` `{track: "internal", releaseStatus: "draft"}`; no credentials, no `serviceAccountKeyPath`, no `ascAppId` committed.
- `app.json`: `name "Calendium"`, `slug "calendium"`, `scheme "calendium"`; `ios.infoPlist` = `{ITSAppUsesNonExemptEncryption: false}` only (no `*UsageDescription`); `ios.privacyManifests` = `NSPrivacyTracking false`, `NSPrivacyCollectedDataTypes []`, API types UserDefaults `CA92.1`, FileTimestamp `C617.1`, SystemBootTime `35F9.1`, DiskSpace `E174.1`; `android.permissions ["android.permission.POST_NOTIFICATIONS"]`; `android.blockedPermissions` READ_EXTERNAL_STORAGE, WRITE_EXTERNAL_STORAGE, SYSTEM_ALERT_WINDOW, SCHEDULE_EXACT_ALARM; `adaptiveIcon.backgroundColor "#0a0a0a"`; top-level `splash` replaced by the `expo-splash-screen` plugin `{image: ./assets/images/splash-icon.png, imageWidth: 200, resizeMode: contain, backgroundColor: "#ffffff", dark: {image: ./assets/images/splash-icon-dark.png, backgroundColor: "#0a0a0a"}}`; `expo-notifications` plugin `{icon: ./assets/images/notification-icon.png, color: "#0a0a0a"}`; `app.config.js` unchanged.
- Assets: source `apps/web/public/icon.svg`; `sharp` is a root `devDependency` run with `node`; PNGs written with `png({compressionLevel: 9, adaptiveFiltering: false})`; sizes: `icon.png` 1024 opaque square, `adaptive-icon.png` 1024 glyph at 60% on transparent, `splash-icon.png` 1024 `#0a0a0a` glyph, `splash-icon-dark.png` 1024 `#fafafa` glyph, `notification-icon.png` 96 `#ffffff` glyph, `favicon.png` 48 original, `apps/desktop/build/appicon.png` 1024 original; `splash.png`, `react-native-reusables-{dark,light}.png` deleted; outputs committed and drift-tested (byte-identical regeneration).
- Dedupe: root `overrides` `{"react": "19.1.0", "react-dom": "19.1.0", "@types/react": "~19.1.10"}`; bumps `expo ~54.0.37`, `expo-constants ~18.0.14`, `jest-expo ~54.0.18`; `bun.lock` refreshed; `bunx expo-doctor` 18/18; CI job `mobile-doctor` (`bunx expo-doctor`, `bunx expo install --check`, `bunx expo config --type public > /dev/null`).
- Desktop update check (`apps/desktop/update.go`, stdlib only): default URL `https://api.github.com/repos/GuilhermeVozniak/calendium/releases/latest`; `CALENDIUM_UPDATE_URL` overrides, `off` disables; disabled unless `version` is plain `X.Y.Z`; `GET` with `Accept: application/vnd.github+json`, `User-Agent: Calendium-Desktop/<version>`, `If-None-Match` from cache `os.UserConfigDir()/Calendium/update-check.json` (`{etag, tagName, htmlUrl, checkedAt}`); 5 s timeout; `304` → cached result; `200` → `tag_name`, `html_url`, `draft`, `prerelease` (drafts/prereleases ignored); no query params; first run 10 s after startup, then every 24 h, stopped on shutdown; event `update-available` with `UpdateInfo{Available, Current, Latest, URL}`; binding `GetUpdateStatus() UpdateInfo`.
- Desktop banner: text `Calendium X.Y.Z is available`, **Download** → `desktop.OpenExternal(url)`, **Later** → localStorage `calendium.update.dismissed=<version>` (a newer version re-shows); no modal, no toast; mounted in `App.tsx` between titlebar and body.
- Deep links on every platform: `SingleInstanceLock{UniqueId: "app.calendium.desktop", OnSecondInstanceLaunch: app.onSecondInstance}`; `app.consumeArgs(os.Args[1:])` before `wails.Run`; macOS keeps `OnUrlOpen`; Windows: `wails build -nsis` with the stock template (no custom NSIS file committed); Linux: `build/linux/calendium.desktop` (`Exec=Calendium %u`, `MimeType=x-scheme-handler/calendium;`, `Icon=calendium`, `StartupWMClass=Calendium`) + `build/linux/README.txt` + 512 px `calendium.png` in the tarball.
- `wails.json` gains `info.companyName "Calendium"` and `info.copyright "© 2026 Calendium"`.
- `release.yml`: `workflow_dispatch.inputs` `allow_unsigned` (boolean, default false) and `version` (string, `^\d+\.\d+\.\d+$` or empty); job `preflight` (`needs: test`) computes `version` (tag → `X.Y.Z`; dispatch → input or `0.0.0`) and `build_version` (`X.Y.Z` or `0.0.0-dev.<sha7>`), runs `node scripts/set-version.mjs --check <version>` on tags, fails when `secrets.MACOS_CERT_P12 == ''` unless dispatch with `allow_unsigned=true`; `build-desktop` stamps with `set-version.mjs <version>` (uncommitted), builds with `-ldflags "-X main.version=<build_version>"`, `-nsis` on Windows (`choco install nsis -y` guard), renames `build/bin/Calendium-amd64-installer.exe` → `calendium_<v>_windows_amd64-setup.exe`, keeps the portable zip, adds `.desktop`/icon/README to the Linux tarball; assertions: `plutil -extract CFBundleShortVersionString raw` equals `<version>`, `(Get-Item setup.exe).VersionInfo.ProductVersion` starts with `<version>`; `softprops/action-gh-release@v2` with `generate_release_notes: true`; dispatch runs only upload artifacts.
- Identity: `GITHUB_URL = 'https://github.com/GuilhermeVozniak/calendium'`; `SUPPORT_EMAIL = env.supportEmail` from `NEXT_PUBLIC_SUPPORT_EMAIL` (default `support@calendium.app`); footer `© {new Date().getFullYear()} Calendium`; mobile `CLOUD_PRESET.serverUrl` from `EXPO_PUBLIC_CLOUD_API_URL` (default `https://api.calendium.app`), `DEMO_CONFIG` from `EXPO_PUBLIC_DEMO_SERVER_URL` (default `https://demo.calendium.app`); desktop `CLOUD_PRESET.serverUrl` from `VITE_CLOUD_API_URL` (default `https://api.calendium.app`); after discovery clients prefer `InstanceInfo.webUrl` (added by piece 1; typed optional — consume, never define).
- Root `package.json` scripts: `version:set` → `node scripts/set-version.mjs`, `assets:gen` → `node scripts/gen-mobile-assets.mjs`, `test:scripts` → `vitest run --root scripts` (added to `test` and lefthook `pre-push`); `biome.json` `files.includes` gains `**/*.mjs`.
- Hexagonal rule: the backend change is a `var` in the inbound `httpapi` adapter and a constant in the outbound `push` adapter; no new ports, no frameworks, stdlib only (desktop `update.go` uses only `net/http`, `encoding/json`, `os`, `time`).
- Demo mode and e2e: `DEMO_CONFIG` defaults are unchanged, demo never dials out, web marketing copy changes are not asserted by any Playwright spec (`apps/web/e2e` has no `Inc.`/support-email/repo-URL assertions); `bun run test:e2e` must stay green.
- Non-goals stay out: no certificate procurement, no in-place updater, no store submission, no account deletion, no mobile builds in GitHub Actions.

## Review Focus

1. **`v`-prefixed version passed to `set-version.mjs`** (`bun run version:set v1.2.3` — humans copy tag names): must exit 1 with a message naming the `X.Y.Z` shape, never write `"version": "v1.2.3"` into `app.json` (the store would reject it). → test added in Task 12.
2. **GitHub returns `200` with a non-semver `tag_name` (e.g. `nightly`) or a non-JSON body**: `check` returns an error, emits nothing, and does not overwrite the cache with the bad release. → test added in Task 7.
3. **Corrupt `update-check.json` on disk**: treated as "no cache" — the request goes out without `If-None-Match`, the result is applied normally, and the cache is rewritten as valid JSON. → test added in Task 7.
4. **Blank or whitespace `NEXT_PUBLIC_SUPPORT_EMAIL`** (`NEXT_PUBLIC_SUPPORT_EMAIL=` left empty in `.env`): `env.supportEmail` must fall back to `support@calendium.app`, and a padded value must be trimmed, so no `mailto: ` link is ever rendered. → test added in Task 15.
5. **`icon.svg` edited so the background `<rect>` is no longer first / no longer `#0a0a0a`**: the generator must throw instead of silently emitting glyph-less or wrongly-colored icons. → test added in Task 2.

## Execution tracks

Four parallel tracks with strict file ownership. A file is edited by exactly one track; other tracks consume the **Produces** interfaces listed in its tasks.

| Track | Tasks | Owns (nobody else edits these) |
|---|---|---|
| **A — mobile config, assets, dedupe, root tooling, CI test.yml** | 1 → 2 → 3 → 4 → 5 (sequential) | `package.json` (root), `bun.lock`, `biome.json`, `lefthook.yml`, `.github/workflows/test.yml`, `apps/mobile/package.json`, `apps/mobile/jest.config.js`, `apps/mobile/app.json`, `apps/mobile/eas.json`, `apps/mobile/app-config.test.ts`, `apps/mobile/assets/images/*`, `apps/desktop/build/appicon.png`, `scripts/gen-mobile-assets.mjs`, `scripts/gen-mobile-assets.test.mjs` |
| **B — desktop host + frontend** | 6 → 7 → 8 → 9 → 10 → 11 (sequential) | `apps/desktop/app.go`, `app_test.go`, `main.go`, `tray.go` (shutdown hook only), `update.go`, `update_test.go`, `identity_test.go`, `wails.json`, `build/darwin/Info.plist`, `build/linux/calendium.desktop`, `build/linux/README.txt`, `frontend/src/lib/wails.ts`, `wails.test.ts`, `frontend/src/views/UpdateBanner.tsx`, `UpdateBanner.test.tsx`, `frontend/src/App.tsx` |
| **C — versioning script, backend version, release workflow** | 12 → 13 → 14 (sequential) | `scripts/set-version.mjs`, `scripts/set-version.test.mjs`, `backend/internal/adapter/in/httpapi/instance.go`, `instance_test.go`, `backend/Dockerfile`, `.github/workflows/release.yml` |
| **D — identity, env-driven hosts, APNs default, docs** | 15 → 16 → 17 → 18 (sequential) | `apps/web/lib/env.ts`, `env.test.ts`, `apps/web/components/marketing/links.ts`, `links.test.ts`, `site-footer.tsx`, `site-footer.test.ts`, `apps/web/Dockerfile`, `docker-compose.yml`, `.env.example`, `backend/internal/adapter/out/push/apns.go`, `apns_test.go`, `apps/mobile/lib/server-config.ts`, `server-config.test.ts`, `apps/mobile/.env.example`, `apps/desktop/frontend/src/lib/server-config.ts`, `server-config.test.ts`, `apps/desktop/frontend/.env.example`, `docs/self-hosting/clients.md`, `configuration.md`, `providers.md`, `upgrades.md`, `docs/release/store-readiness.md`, `apps/mobile/docs/apple/secret-gem.rb`, `apps/mobile/docs/apple/README.md` |

Cross-track rules:

- Track B never touches `apps/desktop/build/appicon.png` (A) or `apps/desktop/frontend/src/lib/server-config.ts` (D). Track D never touches `apps/desktop/frontend/src/lib/wails.ts` (B).
- Track A Task 5 adds the `go build -ldflags "-X …httpapi.Version=ci"` CI step naming the symbol Track C Task 13 defines. The Go linker ignores an unknown `-X` symbol, so merging A before C is harmless.
- Track D Task 16 passes `VERSION: ${VERSION:-dev}` in `docker-compose.yml` to the `ARG` Track C Task 13 adds; Docker only warns about an unconsumed build arg, so merge order is free.
- Track C Task 14 (`release.yml`) references `scripts/set-version.mjs` (C Task 12), `main.version` (B Task 6) and `build/linux/*` (B Task 9). It only runs in CI after merge; the dry run in Task 20 requires A+B+C merged.
- Track D Task 17 consumes `InstanceInfo.webUrl?: string` from `packages/shared/src/types.ts` (piece 1). Piece 1 must be merged into the branch before Task 17 starts; Task 17's first step verifies it.
- `bun install` (lockfile refresh) happens only in Track A Task 1; no other track adds a dependency.
- Tasks 19 (full-suite gate) and 20 (verification runbook) run once after all four tracks merge.

### Task 1: Dependency dedupe, patch bumps and root scripts (Track A)

**Files:**
- Modify: `package.json` (root) — `scripts`, `devDependencies`, `overrides`
- Modify: `apps/mobile/package.json:28-29,61` — `expo`, `expo-constants`, `jest-expo`
- Modify: `apps/mobile/jest.config.js:3-16` — comment shrink
- Modify: `biome.json` — `files.includes`
- Modify: `bun.lock` — regenerated by `bun install` (expected; commit it)

**Interfaces:**
- Consumes: nothing.
- Produces: root scripts `version:set`, `assets:gen`; `sharp@^0.34.5` resolvable from `scripts/`; a single hoisted `react@19.1.0` (no `apps/mobile/node_modules/react`).

- [ ] **Step 1: Record the current failure (expo-doctor 16/18)**

Run: `cd apps/mobile && bunx expo-doctor; cd ../..`

Expected: `2 checks failed` — duplicate `react`/`react-dom` (`19.1.0` nested at `node_modules/react`, `19.2.7` hoisted) and patch mismatches `expo 54.0.35→~54.0.37`, `expo-constants 18.0.13→~18.0.14`, `jest-expo 54.0.17→~54.0.18`.

- [ ] **Step 2: Edit the root `package.json`**

Replace the `scripts` additions, `devDependencies` and `overrides` so the file reads (only the shown keys change; keep every other script as-is):

```json
  "scripts": {
    "dev:mobile": "bun run --cwd apps/mobile dev",
    "dev:web": "bun run --cwd apps/web dev",
    "dev:desktop": "cd apps/desktop && wails dev",
    "dev:api": "cd backend && go run ./cmd/api",
    "build:web": "bun run --cwd apps/web build",
    "build:desktop": "cd apps/desktop && wails build",
    "build:api": "cd backend && go build ./...",
    "test": "bun run test:api && bun run test:shared && bun run test:web && bun run test:mobile && bun run test:desktop",
    "test:api": "cd backend && go test ./...",
    "test:shared": "bun run --cwd packages/shared test",
    "test:web": "bun run --cwd apps/web test",
    "test:mobile": "bun run --cwd apps/mobile test",
    "test:desktop": "bun run --cwd apps/desktop/frontend test",
    "test:e2e": "bun run --cwd apps/web e2e",
    "typecheck": "bun run --cwd apps/web typecheck && bun run --cwd packages/shared typecheck",
    "format": "prettier --write \"**/*.{ts,tsx,js,jsx,json,md,css}\"",
    "lint": "bun run lint:js && bun run lint:go",
    "lint:js": "biome check .",
    "lint:go": "cd backend && golangci-lint run ./... && cd ../apps/desktop && golangci-lint run ./...",
    "version:set": "node scripts/set-version.mjs",
    "assets:gen": "node scripts/gen-mobile-assets.mjs",
    "prepare": "lefthook install"
  },
  "devDependencies": {
    "@biomejs/biome": "^2.5.2",
    "lefthook": "^2.1.9",
    "prettier": "^3.6.2",
    "sharp": "^0.34.5",
    "vitest": "^4.1.10"
  },
  "overrides": {
    "react": "19.1.0",
    "react-dom": "19.1.0",
    "@types/react": "~19.1.10"
  }
```

(`test:scripts` is added in Task 2 together with the first test under `scripts/`, so `vitest run --root scripts` never runs against an empty directory.)

- [ ] **Step 3: Apply the patch bumps in `apps/mobile/package.json`**

Change exactly these three lines:

```json
    "expo": "~54.0.37",
    "expo-constants": "~18.0.14",
```

and in `devDependencies`:

```json
    "jest-expo": "~54.0.18",
```

- [ ] **Step 4: Let Biome see `.mjs`**

In `biome.json` `files.includes`, insert after `"**/*.jsx",`:

```json
      "**/*.mjs",
```

- [ ] **Step 5: Refresh the lockfile**

Run: `bun install`

Expected: `bun.lock` changes (this is expected and must be committed): `sharp@0.34.5` moves to the root devDependencies, the `@calendium/mobile/react` and `@calendium/mobile/react-dom` nested entries disappear, `expo`, `expo-constants`, `jest-expo` move to the new patch versions. Verify:

```
ls apps/mobile/node_modules/react 2>&1
```
Expected: `No such file or directory`.
```
node -e "console.log(require('./node_modules/react/package.json').version, require('./node_modules/sharp/package.json').version)"
```
Expected: `19.1.0 0.34.5`.

- [ ] **Step 6: Shrink the obsolete nested-react comment in `apps/mobile/jest.config.js`**

Replace lines 3-16 (the paragraph starting `// react-native, react-test-renderer, and @testing-library/react-native are` and ending `// run, not Metro's bundling of the real app.`) with:

```js
// Belt-and-braces: the root package.json `overrides` pin react/react-dom to
// 19.1.0 so there is exactly one hoisted copy (expo-doctor's duplicate check
// passes). The moduleNameMapper entries below still force every `react`
// import to that copy, so a future looser range elsewhere in the workspace
// cannot reintroduce two React instances under Jest.
```

- [ ] **Step 7: Verify expo-doctor and every TS suite**

Run: `cd apps/mobile && bunx expo-doctor && bunx expo install --check; cd ../..`

Expected: `18/18 checks passed. No issues detected!` and `Dependencies are up to date`.

Run: `bun run test:mobile && bun run test:web && bun run test:desktop && bun run test:shared && bunx biome check .`

Expected: all PASS (the hoisted `react@19.1.0` satisfies `apps/web` `^19.1.0`, `apps/desktop/frontend` `^19.1.0`, Next 15.5).

- [ ] **Step 8: Commit**

```
git add package.json bun.lock biome.json apps/mobile/package.json apps/mobile/jest.config.js
git commit -m "chore(deps): pin one react 19.1.0 across the workspace, apply SDK 54 patch bumps, add sharp + version/asset scripts" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 2: Asset generator `scripts/gen-mobile-assets.mjs` with drift test (Track A)

**Files:**
- Create: `scripts/gen-mobile-assets.mjs`
- Create: `scripts/gen-mobile-assets.test.mjs`
- Create (generated, committed): `apps/mobile/assets/images/icon.png`, `adaptive-icon.png`, `splash-icon.png`, `splash-icon-dark.png`, `notification-icon.png`, `favicon.png` (overwrites the template PNGs), `apps/desktop/build/appicon.png` (overwrites the Wails "W")
- Delete: `apps/mobile/assets/images/react-native-reusables-dark.png`, `react-native-reusables-light.png` (unreferenced — verified by `grep -rn react-native-reusables apps/mobile --include=*.ts --include=*.tsx --include=*.js --include=*.json` returning nothing)
- Modify: `package.json` (root) — add `test:scripts`, chain it into `test`; `lefthook.yml` — add `test:scripts` job

**Interfaces:**
- Consumes: `sharp` (Task 1).
- Produces: `export const ROOT: string`, `export const SOURCE_SVG = 'apps/web/public/icon.svg'`, `export const OUTPUTS: {file: string; size: number; variant: 'original'|'square'|'glyph'; color?: string; scale?: number}[]`, `export function deriveVariants(svg: string): {original: string; square: string; glyph: (color: string) => string}`, `export async function renderOutput(variants, spec): Promise<Buffer>`, `export async function renderAll({sourceSvg?, outDir?} = {}): Promise<string[]>`; the seven committed PNGs at the sizes in Global Constraints; root script `test:scripts`.

- [ ] **Step 1: Write the failing test `scripts/gen-mobile-assets.test.mjs`**

```js
import { mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';
import { describe, expect, it } from 'vitest';

import { OUTPUTS, ROOT, SOURCE_SVG, deriveVariants, renderAll } from './gen-mobile-assets.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SLOW = { timeout: 60_000 };

async function rgba(buf, x, y) {
  const { data, info } = await sharp(buf).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
  const i = (y * info.width + x) * info.channels;
  return [data[i], data[i + 1], data[i + 2], data[i + 3]];
}

function byName(name) {
  const spec = OUTPUTS.find((o) => path.basename(o.file) === name);
  if (!spec) throw new Error(`no output named ${name}`);
  return spec;
}

describe('gen-mobile-assets', () => {
  it('ROOT is the repo root and the source SVG exists there', async () => {
    expect(path.resolve(HERE, '..')).toBe(ROOT);
    const svg = await readFile(path.join(ROOT, SOURCE_SVG), 'utf8');
    expect(svg).toContain('rx="96" fill="#0a0a0a"');
  });

  it('deriveVariants squares the background and strips it for the glyph', async () => {
    const svg = await readFile(path.join(ROOT, SOURCE_SVG), 'utf8');
    const v = deriveVariants(svg);
    expect(v.original).toBe(svg);
    expect(v.square).toContain('rx="0" fill="#0a0a0a"');
    expect(v.square).not.toContain('rx="96"');
    expect(v.glyph('#ffffff')).not.toContain('#0a0a0a');
    expect(v.glyph('#ffffff')).not.toContain('#fafafa');
    expect(v.glyph('#ffffff')).toContain('stroke="#ffffff"');
  });

  // Review Focus 5: a reshaped source must fail loudly, not emit blank icons.
  it('deriveVariants throws when the leading background rect is missing or recolored', () => {
    const noRect = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><circle r="10" fill="#fafafa"/></svg>';
    expect(() => deriveVariants(noRect)).toThrow(/expected a leading #0a0a0a background <rect/);
    const wrongFill = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#ffffff"/></svg>';
    expect(() => deriveVariants(wrongFill)).toThrow(/expected a leading #0a0a0a background <rect/);
  });

  it('renders every output at its documented size with the right alpha', async () => {
    const outDir = await mkdtemp(path.join(os.tmpdir(), 'calendium-assets-'));
    const written = await renderAll({ outDir });
    expect(written).toEqual(OUTPUTS.map((o) => o.file));

    for (const spec of OUTPUTS) {
      const meta = await sharp(path.join(outDir, spec.file)).metadata();
      expect([spec.file, meta.width, meta.height]).toEqual([spec.file, spec.size, spec.size]);
    }

    const icon = await readFile(path.join(outDir, byName('icon.png').file));
    expect((await sharp(icon).metadata()).hasAlpha).toBe(false); // App Store rejects alpha
    expect(await rgba(icon, 2, 2)).toEqual([10, 10, 10, 255]); // square corner is background

    const adaptive = await readFile(path.join(outDir, byName('adaptive-icon.png').file));
    expect((await rgba(adaptive, 2, 2))[3]).toBe(0); // transparent outside the glyph
    expect(await rgba(adaptive, 482, 556)).toEqual([250, 250, 250, 255]); // glyph bar, 60% scale

    const splash = await readFile(path.join(outDir, byName('splash-icon.png').file));
    expect((await rgba(splash, 2, 2))[3]).toBe(0);
    expect(await rgba(splash, 462, 586)).toEqual([10, 10, 10, 255]);

    const splashDark = await readFile(path.join(outDir, byName('splash-icon-dark.png').file));
    expect(await rgba(splashDark, 462, 586)).toEqual([250, 250, 250, 255]);

    const notif = await readFile(path.join(outDir, byName('notification-icon.png').file));
    const { data, info } = await sharp(notif).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
    expect(info.width).toBe(96);
    let visible = 0;
    for (let i = 0; i < data.length; i += info.channels) {
      if (data[i + 3] === 0) continue;
      visible += 1;
      // Android tints the notification glyph: every visible pixel must be white.
      expect(data[i]).toBeGreaterThanOrEqual(250);
      expect(data[i + 1]).toBeGreaterThanOrEqual(250);
      expect(data[i + 2]).toBeGreaterThanOrEqual(250);
    }
    expect(visible).toBeGreaterThan(200);

    const appicon = await readFile(path.join(outDir, byName('appicon.png').file));
    expect((await rgba(appicon, 0, 0))[3]).toBe(0); // rounded corners stay transparent
    expect(await rgba(appicon, 512, 512)).toEqual([10, 10, 10, 255]);
  }, SLOW);

  it('regenerating over the committed assets is byte-identical (no drift)', async () => {
    const outDir = await mkdtemp(path.join(os.tmpdir(), 'calendium-assets-'));
    await renderAll({ outDir });
    for (const spec of OUTPUTS) {
      const fresh = await readFile(path.join(outDir, spec.file));
      const committed = await readFile(path.join(ROOT, spec.file));
      expect(fresh.equals(committed), `${spec.file} drifted: run \`bun run assets:gen\` and commit`).toBe(true);
    }
  }, SLOW);
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `bunx vitest run --root scripts`

Expected: FAIL — `Failed to load ./gen-mobile-assets.mjs` (module does not exist).

- [ ] **Step 3: Write `scripts/gen-mobile-assets.mjs`**

```js
#!/usr/bin/env node
// scripts/gen-mobile-assets.mjs
//
// Derives every raster app icon from the one vector source of truth,
// apps/web/public/icon.svg (512 viewBox: a #0a0a0a rounded square rx=96
// carrying a #fafafa calendar glyph; already the web favicon). Outputs are
// committed and builds never run this; scripts/gen-mobile-assets.test.mjs
// regenerates into a temp dir and fails on drift. Edit the SVG, run
// `bun run assets:gen`, commit the PNGs together.
//
// Run with node (sharp's native binding): `node scripts/gen-mobile-assets.mjs`.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const SOURCE_SVG = 'apps/web/public/icon.svg';

const SOURCE_VIEWBOX = 512;
const BACKGROUND = '#0a0a0a';
const GLYPH = '#fafafa';
const TRANSPARENT = { r: 0, g: 0, b: 0, alpha: 0 };
// Deterministic encoder settings so regeneration is byte-identical.
const PNG_OPTIONS = { compressionLevel: 9, adaptiveFiltering: false };

/**
 * Every file the script writes, relative to the repo root.
 *  - original: the SVG as-is (rounded square, transparent corners)
 *  - square:   background rx forced to 0 and flattened (App Store icons must be opaque)
 *  - glyph:    background removed, glyph recolored to `color`, optionally scaled
 *              inside the canvas (`scale`) on a transparent background
 */
export const OUTPUTS = [
  { file: 'apps/mobile/assets/images/icon.png', size: 1024, variant: 'square' },
  { file: 'apps/mobile/assets/images/adaptive-icon.png', size: 1024, variant: 'glyph', color: GLYPH, scale: 0.6 },
  { file: 'apps/mobile/assets/images/splash-icon.png', size: 1024, variant: 'glyph', color: BACKGROUND },
  { file: 'apps/mobile/assets/images/splash-icon-dark.png', size: 1024, variant: 'glyph', color: GLYPH },
  { file: 'apps/mobile/assets/images/notification-icon.png', size: 96, variant: 'glyph', color: '#ffffff' },
  { file: 'apps/mobile/assets/images/favicon.png', size: 48, variant: 'original' },
  { file: 'apps/desktop/build/appicon.png', size: 1024, variant: 'original' },
];

const FIRST_RECT = /<rect\b[^>]*\/>/;

/** Splits the source SVG into the strings each variant is rendered from. */
export function deriveVariants(svg) {
  const match = FIRST_RECT.exec(svg);
  if (!match || !match[0].includes(`fill="${BACKGROUND}"`) || !/\brx="\d+"/.test(match[0])) {
    throw new Error(
      `${SOURCE_SVG}: expected a leading ${BACKGROUND} background <rect .../> with rx before the glyph`
    );
  }
  const background = match[0];
  const square = svg.replace(background, background.replace(/\brx="\d+"/, 'rx="0"'));
  const glyphBase = svg.replace(background, '');
  return {
    original: svg,
    square,
    glyph: (color) => glyphBase.split(GLYPH).join(color),
  };
}

function load(svg, size) {
  // Render the vector at the target size (density hint) instead of upscaling a
  // 512 px raster, then pin the exact pixel dimensions.
  return sharp(Buffer.from(svg), { density: (72 * size) / SOURCE_VIEWBOX }).resize(size, size);
}

/** Renders one OUTPUTS entry to a PNG buffer. */
export async function renderOutput(variants, spec) {
  const { size } = spec;
  if (spec.variant === 'original') return load(variants.original, size).png(PNG_OPTIONS).toBuffer();
  if (spec.variant === 'square') {
    return load(variants.square, size).flatten({ background: BACKGROUND }).png(PNG_OPTIONS).toBuffer();
  }
  const scale = spec.scale ?? 1;
  const inner = Math.round(size * scale);
  const glyph = await load(variants.glyph(spec.color), inner).png().toBuffer();
  const before = Math.floor((size - inner) / 2);
  const after = size - inner - before;
  return sharp(glyph)
    .extend({ top: before, bottom: after, left: before, right: after, background: TRANSPARENT })
    .png(PNG_OPTIONS)
    .toBuffer();
}

/** Writes every OUTPUTS entry under outDir (default: the repo). Returns the files written. */
export async function renderAll({ sourceSvg = path.join(ROOT, SOURCE_SVG), outDir = ROOT } = {}) {
  const variants = deriveVariants(await readFile(sourceSvg, 'utf8'));
  const written = [];
  for (const spec of OUTPUTS) {
    const abs = path.join(outDir, spec.file);
    await mkdir(path.dirname(abs), { recursive: true });
    await writeFile(abs, await renderOutput(variants, spec));
    written.push(spec.file);
  }
  return written;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  for (const file of await renderAll()) console.log(`assets:gen ${file}`);
}
```

- [ ] **Step 4: Generate the committed assets and remove the template art**

Run: `node scripts/gen-mobile-assets.mjs`

Expected: seven `assets:gen …` lines. Then:

```
git rm apps/mobile/assets/images/react-native-reusables-dark.png apps/mobile/assets/images/react-native-reusables-light.png
file apps/mobile/assets/images/*.png apps/desktop/build/appicon.png
```

Expected `file` output: `icon.png` `1024 x 1024, 8-bit/color RGB` (no alpha), `adaptive-icon.png`/`splash-icon.png`/`splash-icon-dark.png`/`appicon.png` `1024 x 1024, 8-bit/color RGBA`, `notification-icon.png` `96 x 96 … RGBA`, `favicon.png` `48 x 48 … RGBA`, `splash.png` still present (deleted in Task 3 with the `app.json` change that stops referencing it).

- [ ] **Step 5: Wire `test:scripts` into the root scripts and lefthook**

Root `package.json`: change the `test` line and add `test:scripts` directly after `test:e2e`:

```json
    "test": "bun run test:api && bun run test:shared && bun run test:web && bun run test:mobile && bun run test:desktop && bun run test:scripts",
```
```json
    "test:e2e": "bun run --cwd apps/web e2e",
    "test:scripts": "vitest run --root scripts",
```

`lefthook.yml`: append to `pre-push.jobs`:

```yaml
    - name: test:scripts
      run: bun run test:scripts
```

- [ ] **Step 6: Run the tests and lint**

Run: `bun run test:scripts`

Expected: `5 passed` (including the byte-identical drift test against the files just generated).

Run: `bunx biome check scripts`

Expected: `Checked 2 files … No fixes applied`.

- [ ] **Step 7: Commit**

```
git add scripts/gen-mobile-assets.mjs scripts/gen-mobile-assets.test.mjs apps/mobile/assets/images apps/desktop/build/appicon.png package.json lefthook.yml
git commit -m "feat(assets): generate mobile and desktop icons from icon.svg with sharp; drop template art" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 3: `app.json` identity, Info.plist keys, privacy manifest, Android permissions, splash plugin (Track A)

**Files:**
- Modify: `apps/mobile/app.json` (whole file rewritten; `app.config.js` untouched)
- Create: `apps/mobile/app-config.test.ts`
- Delete: `apps/mobile/assets/images/splash.png`

**Interfaces:**
- Consumes: the generated PNGs (Task 2).
- Produces: `apps/mobile/app.json` with `expo.version` at `"version": "1.0.0"` (the verbatim line `scripts/set-version.mjs` stamps, Task 12), `expo-splash-screen` + `expo-notifications` plugin entries, `ios.infoPlist`, `ios.privacyManifests`, `android.permissions`/`blockedPermissions`.

- [ ] **Step 1: Write the failing test `apps/mobile/app-config.test.ts`**

```ts
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

  it('ships the SDK 54 default required-reason API set with no tracking and no collected data', () => {
    const manifest = config.ios?.privacyManifests as {
      NSPrivacyTracking: boolean;
      NSPrivacyCollectedDataTypes: unknown[];
      NSPrivacyAccessedAPITypes: { NSPrivacyAccessedAPIType: string; NSPrivacyAccessedAPITypeReasons: string[] }[];
    };
    expect(manifest.NSPrivacyTracking).toBe(false);
    expect(manifest.NSPrivacyCollectedDataTypes).toEqual([]);
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
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/mobile && bunx jest app-config; cd ../..`

Expected: FAIL — `expected 'calendium' to be 'Calendium'`, `expected undefined to equal { ITSAppUsesNonExemptEncryption: false }`, splash plugin missing, `splash.png` still present.

- [ ] **Step 3: Rewrite `apps/mobile/app.json`**

```json
{
  "expo": {
    "name": "Calendium",
    "slug": "calendium",
    "version": "1.0.0",
    "orientation": "portrait",
    "icon": "./assets/images/icon.png",
    "scheme": "calendium",
    "userInterfaceStyle": "automatic",
    "newArchEnabled": true,
    "assetBundlePatterns": ["**/*"],
    "ios": {
      "supportsTablet": true,
      "bundleIdentifier": "app.calendium.mobile",
      "infoPlist": {
        "ITSAppUsesNonExemptEncryption": false
      },
      "privacyManifests": {
        "NSPrivacyTracking": false,
        "NSPrivacyCollectedDataTypes": [],
        "NSPrivacyAccessedAPITypes": [
          {
            "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryUserDefaults",
            "NSPrivacyAccessedAPITypeReasons": ["CA92.1"]
          },
          {
            "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryFileTimestamp",
            "NSPrivacyAccessedAPITypeReasons": ["C617.1"]
          },
          {
            "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategorySystemBootTime",
            "NSPrivacyAccessedAPITypeReasons": ["35F9.1"]
          },
          {
            "NSPrivacyAccessedAPIType": "NSPrivacyAccessedAPICategoryDiskSpace",
            "NSPrivacyAccessedAPITypeReasons": ["E174.1"]
          }
        ]
      }
    },
    "android": {
      "edgeToEdgeEnabled": true,
      "package": "app.calendium.mobile",
      "adaptiveIcon": {
        "foregroundImage": "./assets/images/adaptive-icon.png",
        "backgroundColor": "#0a0a0a"
      },
      "permissions": ["android.permission.POST_NOTIFICATIONS"],
      "blockedPermissions": [
        "android.permission.READ_EXTERNAL_STORAGE",
        "android.permission.WRITE_EXTERNAL_STORAGE",
        "android.permission.SYSTEM_ALERT_WINDOW",
        "android.permission.SCHEDULE_EXACT_ALARM"
      ]
    },
    "web": {
      "bundler": "metro",
      "output": "static",
      "favicon": "./assets/images/favicon.png"
    },
    "plugins": [
      "expo-router",
      [
        "expo-splash-screen",
        {
          "image": "./assets/images/splash-icon.png",
          "imageWidth": 200,
          "resizeMode": "contain",
          "backgroundColor": "#ffffff",
          "dark": {
            "image": "./assets/images/splash-icon-dark.png",
            "backgroundColor": "#0a0a0a"
          }
        }
      ],
      [
        "expo-notifications",
        {
          "icon": "./assets/images/notification-icon.png",
          "color": "#0a0a0a"
        }
      ]
    ],
    "experiments": {
      "typedRoutes": true
    }
  }
}
```

- [ ] **Step 4: Delete the legacy splash and validate the config resolves**

```
git rm apps/mobile/assets/images/splash.png
cd apps/mobile && bunx expo config --type public > /dev/null && echo CONFIG_OK; cd ../..
```

Expected: `CONFIG_OK` (schema accepts `infoPlist`, `privacyManifests`, `blockedPermissions`, the splash plugin).

- [ ] **Step 5: Run the tests**

Run: `cd apps/mobile && bunx jest app-config; cd ../..`

Expected: `9 passed`.

Run: `bun run test:mobile && bunx biome check apps/mobile`

Expected: PASS (the whole mobile suite, with `app-config.test.ts` included by jest's default `testMatch`).

- [ ] **Step 6: Commit**

```
git add apps/mobile/app.json apps/mobile/app-config.test.ts apps/mobile/assets/images
git commit -m "feat(mobile): store-ready app.json (identity, encryption flag, privacy manifest, minimal permissions, splash plugin)" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 4: EAS build and submit profiles (Track A)

**Files:**
- Modify: `apps/mobile/eas.json` (whole file)
- Modify: `apps/mobile/app-config.test.ts` (append a `describe('eas.json')`)

**Interfaces:**
- Consumes: nothing.
- Produces: profiles `development`, `preview`, `production` (the names `docs/self-hosting/clients.md` and `docs/release/store-readiness.md` cite, Tasks 17/18).

- [ ] **Step 1: Append the failing test to `apps/mobile/app-config.test.ts`**

```ts
describe('eas.json', () => {
  const eas = JSON.parse(fs.readFileSync(path.join(__dirname, 'eas.json'), 'utf8')) as {
    cli: Record<string, unknown>;
    build: Record<string, Record<string, unknown> | undefined>;
    submit: { production: { ios: Record<string, unknown>; android: Record<string, unknown> } };
  };

  it('pins the CLI and lets EAS own build numbers (appVersionSource remote + autoIncrement)', () => {
    expect(eas.cli).toEqual({ version: '>= 16.0.0', appVersionSource: 'remote' });
    expect(eas.build.development).toEqual({
      developmentClient: true,
      distribution: 'internal',
      channel: 'development',
    });
    expect(eas.build.preview).toEqual({ distribution: 'internal', channel: 'preview' });
    expect(eas.build.production).toEqual({
      distribution: 'store',
      channel: 'production',
      autoIncrement: true,
    });
    expect(eas.build.internal).toBeUndefined();
  });

  it('submit.production carries the Apple team id and a draft internal Play track, never credentials', () => {
    expect(eas.submit.production.ios).toEqual({ appleTeamId: 'CT22R575UG' });
    expect(eas.submit.production.android).toEqual({ track: 'internal', releaseStatus: 'draft' });
    expect(JSON.stringify(eas)).not.toMatch(/serviceAccountKeyPath|appleId|ascApiKey|password/i);
  });
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd apps/mobile && bunx jest app-config -t eas; cd ../..`

Expected: FAIL — `expected { version: '>= 5.9.0' } to equal { version: '>= 16.0.0', appVersionSource: 'remote' }`.

- [ ] **Step 3: Rewrite `apps/mobile/eas.json`**

```json
{
  "cli": {
    "version": ">= 16.0.0",
    "appVersionSource": "remote"
  },
  "build": {
    "development": {
      "developmentClient": true,
      "distribution": "internal",
      "channel": "development"
    },
    "preview": {
      "distribution": "internal",
      "channel": "preview"
    },
    "production": {
      "distribution": "store",
      "channel": "production",
      "autoIncrement": true
    }
  },
  "submit": {
    "production": {
      "ios": {
        "appleTeamId": "CT22R575UG"
      },
      "android": {
        "track": "internal",
        "releaseStatus": "draft"
      }
    }
  }
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/mobile && bunx jest app-config; cd ../..`

Expected: `11 passed`.

- [ ] **Step 5: Commit**

```
git add apps/mobile/eas.json apps/mobile/app-config.test.ts
git commit -m "feat(mobile): real EAS development/preview/production profiles with remote app version source" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 5: CI — `mobile-doctor` job, `test:scripts`, backend ldflags build (Track A)

**Files:**
- Modify: `.github/workflows/test.yml`

**Interfaces:**
- Consumes: root `test:scripts` (Task 2); symbol `calendium/backend/internal/adapter/in/httpapi.Version` (Task 13 — unknown `-X` symbols are ignored by the Go linker, so this step cannot fail before Task 13 merges).
- Produces: job `mobile-doctor` in `test.yml`.

- [ ] **Step 1: Add the `mobile-doctor` job**

Append after the `ts-tests` job (before `e2e`):

```yaml
  mobile-doctor:
    name: Expo doctor (apps/mobile)
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: apps/mobile
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Bun
        uses: oven-sh/setup-bun@v2

      - name: Install dependencies
        working-directory: .
        run: bun install --frozen-lockfile

      # Acceptance: 18/18. Duplicate react copies or SDK patch drift fail here
      # before they fail inside an EAS build.
      - name: expo-doctor
        run: bunx expo-doctor

      - name: expo install --check
        run: bunx expo install --check

      - name: expo config resolves (app.json + app.config.js)
        run: bunx expo config --type public > /dev/null
```

- [ ] **Step 2: Run the scripts suite in `ts-tests` and the ldflags build in `backend`**

In the `ts-tests` job, append after `Test apps/mobile`:

```yaml
      - name: Test scripts/
        run: bun run test:scripts
```

In the `backend` job, insert after the `Build` step:

```yaml
      # Same -X symbol release images use (backend/Dockerfile ARG VERSION);
      # keeps the ldflags path exercised on every push.
      - name: Build with release ldflags
        run: go build -ldflags "-X calendium/backend/internal/adapter/in/httpapi.Version=ci" ./cmd/api ./cmd/worker
```

- [ ] **Step 3: Validate the workflow parses**

Run: `ruby -ryaml -e 'y = YAML.load_file(".github/workflows/test.yml"); puts y["jobs"].keys.join(",")'`

Expected: `backend,lint-js,lint-go,ts-tests,mobile-doctor,e2e`.

Run (if installed): `which actionlint && actionlint .github/workflows/test.yml`

Expected: no output (or `actionlint` not installed — skip).

- [ ] **Step 4: Commit**

```
git add .github/workflows/test.yml
git commit -m "ci: mobile-doctor job (expo-doctor 18/18), scripts suite, release ldflags build" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 6: Desktop version variable and bundle identity (Track B)

**Files:**
- Modify: `apps/desktop/app.go:11-13` (`const appVersion` → `var version`), `:77-80` (`GetAppVersion`)
- Modify: `apps/desktop/app_test.go:83-88` (`TestGetAppVersion`)
- Modify: `apps/desktop/wails.json` (`info.companyName`, `info.copyright`)
- Modify: `apps/desktop/build/darwin/Info.plist:11,51`
- Create: `apps/desktop/identity_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `var version = "dev"` in package `main` (the `-X main.version=` target for Task 14); `wails.json` `info.productVersion` line `"productVersion": "0.1.0"` (the verbatim line Task 12 stamps); `info.companyName`/`info.copyright` templated by Wails into `Info.plist` (`NSHumanReadableCopyright`) and NSIS (`VIAddVersionKey`).

- [ ] **Step 1: Write the failing tests**

Replace `TestGetAppVersion` in `apps/desktop/app_test.go` with:

```go
func TestGetAppVersion_ReturnsTheLinkerStampedVariable(t *testing.T) {
	prev := version
	version = "1.2.3"
	t.Cleanup(func() { version = prev })
	a := NewApp()
	if got := a.GetAppVersion(); got != "1.2.3" {
		t.Fatalf("GetAppVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestVersion_DefaultsToDevForSourceBuilds(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want \"dev\" (release.yml stamps it with -ldflags \"-X main.version=X.Y.Z\")", version)
	}
}
```

Create `apps/desktop/identity_test.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The bundle identity is build metadata, not code, so these tests pin the
// templates the Wails CLI renders: a stray com.wails.* id or a non-numeric
// productVersion would only surface at notarization / NSIS time.

func TestInfoPlist_UsesTheCalendiumBundleIdentity(t *testing.T) {
	b, err := os.ReadFile("build/darwin/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"<string>app.calendium.desktop</string>",
		"<string>app.calendium.desktop.{{.Scheme}}</string>",
		"<string>{{.Info.ProductVersion}}</string>",
		"<string>{{.Info.Copyright}}</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Info.plist missing %s", want)
		}
	}
	if strings.Contains(s, "com.wails.") {
		t.Errorf("Info.plist still carries a com.wails.* identifier")
	}
}

func TestWailsJSON_CarriesCompanyCopyrightAndNumericVersion(t *testing.T) {
	b, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Info struct {
			ProductName    string `json:"productName"`
			ProductVersion string `json:"productVersion"`
			CompanyName    string `json:"companyName"`
			Copyright      string `json:"copyright"`
			Protocols      []struct {
				Scheme string `json:"scheme"`
			} `json:"protocols"`
		} `json:"info"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Info.ProductName != "Calendium" || cfg.Info.CompanyName != "Calendium" {
		t.Errorf("productName/companyName = %q/%q, want Calendium/Calendium", cfg.Info.ProductName, cfg.Info.CompanyName)
	}
	if cfg.Info.Copyright != "© 2026 Calendium" {
		t.Errorf("copyright = %q, want %q", cfg.Info.Copyright, "© 2026 Calendium")
	}
	// NSIS VIProductVersion needs "${INFO_PRODUCTVERSION}.0" to be numeric.
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(cfg.Info.ProductVersion) {
		t.Errorf("productVersion = %q, want X.Y.Z", cfg.Info.ProductVersion)
	}
	if len(cfg.Info.Protocols) != 1 || cfg.Info.Protocols[0].Scheme != "calendium" {
		t.Errorf("protocols = %+v, want exactly the calendium scheme", cfg.Info.Protocols)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `cd apps/desktop && go test -run 'TestGetAppVersion|TestVersion_|TestInfoPlist|TestWailsJSON' ./...; cd ../..`

Expected: compile error `undefined: version` (app_test.go), then after the var exists: `Info.plist missing <string>app.calendium.desktop</string>`, `companyName = ""`.

- [ ] **Step 3: Replace the constant in `apps/desktop/app.go`**

Replace lines 11-13:

```go
// version is the desktop build version. release.yml stamps it with
// -ldflags "-X main.version=X.Y.Z" (a plain release semver) or
// 0.0.0-dev.<sha7> for dry runs; source builds report "dev". update.go only
// checks for updates when this is a plain X.Y.Z.
var version = "dev"
```

and `GetAppVersion`:

```go
// GetAppVersion returns the desktop app version (see `version`).
func (a *App) GetAppVersion() string {
	return version
}
```

- [ ] **Step 4: Stamp identity into `wails.json` and `Info.plist`**

`apps/desktop/wails.json` `info` block becomes:

```json
  "info": {
    "companyName": "Calendium",
    "productName": "Calendium",
    "productVersion": "0.1.0",
    "copyright": "© 2026 Calendium",
    "protocols": [
      {
        "scheme": "calendium",
        "description": "Calendium deep links",
        "role": "Viewer"
      }
    ]
  }
```

`apps/desktop/build/darwin/Info.plist` line 11: `<string>com.wails.{{.Name}}</string>` → `<string>app.calendium.desktop</string>`; line 51: `<string>com.wails.{{.Scheme}}</string>` → `<string>app.calendium.desktop.{{.Scheme}}</string>`.

- [ ] **Step 5: Run the desktop Go suite**

Run: `cd apps/desktop && go vet ./... && go test ./...; cd ../..`

Expected: PASS (`ok  	calendium/desktop`).

- [ ] **Step 6: Commit**

```
git add apps/desktop/app.go apps/desktop/app_test.go apps/desktop/identity_test.go apps/desktop/wails.json apps/desktop/build/darwin/Info.plist
git commit -m "feat(desktop): ldflags-stamped version variable and app.calendium.desktop bundle identity" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 7: Update checker `apps/desktop/update.go` (Track B)

**Files:**
- Create: `apps/desktop/update.go`
- Create: `apps/desktop/update_test.go`

**Interfaces:**
- Consumes: `var version` (Task 6).
- Produces (package `main`): `const updateAvailableEvent = "update-available"`; `type UpdateInfo struct{Available bool; Current, Latest, URL string}` (JSON `available/current/latest/url`); `type updateChecker struct{...}`; `func newUpdateChecker(version string) *updateChecker`; `func (c *updateChecker) setEmit(func(UpdateInfo))`; `func (c *updateChecker) enabled() bool`; `func (c *updateChecker) status() UpdateInfo`; `func (c *updateChecker) check(ctx context.Context) (UpdateInfo, error)`; `func (c *updateChecker) run(ctx context.Context, initial, every time.Duration)`; `func parseSemver(string) (semver, bool)`; `func newer(latest, current semver) bool`; `func isReleaseVersion(string) bool`; `func resolveUpdateURL(env string) string`; constants `updateInitialDelay = 10 * time.Second`, `updateInterval = 24 * time.Hour`, `updateCheckTimeout = 5 * time.Second`.

- [ ] **Step 1: Write the failing tests `apps/desktop/update_test.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestChecker builds a checker against url with a temp cache file, a fixed
// clock and an emit sink that records every UpdateInfo it receives.
func newTestChecker(t *testing.T, version, url string) (*updateChecker, *[]UpdateInfo) {
	t.Helper()
	var emitted []UpdateInfo
	c := &updateChecker{
		version:   version,
		url:       url,
		hc:        &http.Client{Timeout: updateCheckTimeout},
		cachePath: filepath.Join(t.TempDir(), "update-check.json"),
		now:       func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
		latest:    UpdateInfo{Current: version},
	}
	c.setEmit(func(info UpdateInfo) { emitted = append(emitted, info) })
	return c, &emitted
}

// releaseServer mimics GET /repos/.../releases/latest: ETag "etag-1", 304 on
// a matching If-None-Match, and asserts the request shape the spec fixes.
func releaseServer(t *testing.T, tag string, draft, prerelease bool, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Calendium-Desktop/") {
			t.Errorf("User-Agent = %q, want Calendium-Desktop/<version>", ua)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query string %q (nothing beyond a UA may reach GitHub)", r.URL.RawQuery)
		}
		w.Header().Set("ETag", `"etag-1"`)
		if r.Header.Get("If-None-Match") == `"etag-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/GuilhermeVozniak/calendium/releases/tag/%s","draft":%t,"prerelease":%t}`, tag, tag, draft, prerelease)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateCheck_NewerReleaseEmitsOnce(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", false, false, &hits)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := UpdateInfo{Available: true, Current: "1.2.3", Latest: "1.3.0", URL: "https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0"}
	if info != want {
		t.Fatalf("check = %+v, want %+v", info, want)
	}
	if c.status() != want {
		t.Fatalf("status = %+v, want %+v", c.status(), want)
	}
	if len(*emitted) != 1 || (*emitted)[0] != want {
		t.Fatalf("emitted = %+v, want exactly [%+v]", *emitted, want)
	}
}

func TestUpdateCheck_EqualOlderPrereleaseDraftDoNotEmit(t *testing.T) {
	cases := []struct {
		name       string
		tag        string
		draft, pre bool
	}{
		{"equal", "v1.2.3", false, false},
		{"older", "v1.2.2", false, false},
		{"prerelease flag", "v2.0.0", false, true},
		{"draft flag", "v2.0.0", true, false},
		{"prerelease tag", "v1.2.4-rc.1", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := releaseServer(t, tc.tag, tc.draft, tc.pre, &hits)
			c, emitted := newTestChecker(t, "1.2.3", srv.URL)
			info, err := c.check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if info.Available {
				t.Fatalf("Available = true for %+v", info)
			}
			if len(*emitted) != 0 {
				t.Fatalf("emitted %+v, want nothing", *emitted)
			}
		})
	}
}

func TestUpdateCheck_ETagRoundTripServesCachedResult(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", false, false, &hits)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)

	if _, err := c.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	for _, key := range []string{`"etag":"\"etag-1\""`, `"tagName":"v1.3.0"`, `"htmlUrl":`, `"checkedAt":"2026-10-04T12:00:00Z"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("cache %s missing %s", b, key)
		}
	}

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2 (second request must still go out, conditionally)", hits.Load())
	}
	if !info.Available || info.Latest != "1.3.0" {
		t.Fatalf("304 result = %+v, want the cached v1.3.0", info)
	}
	if len(*emitted) != 1 {
		t.Fatalf("emitted %d times, want 1 (same version must not re-emit)", len(*emitted))
	}
}

func TestUpdateCheck_TimeoutErrorsWithoutEmitting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)
	c.hc = &http.Client{Timeout: 50 * time.Millisecond}

	info, err := c.check(context.Background())
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if info.Available || len(*emitted) != 0 {
		t.Fatalf("timeout must not change status (%+v) or emit (%+v)", info, *emitted)
	}
}

func TestUpdateCheck_DevBuildsNeverRequest(t *testing.T) {
	for _, v := range []string{"dev", "0.0.0-dev.abc1234", "dev (browser)", ""} {
		var hits atomic.Int32
		srv := releaseServer(t, "v9.9.9", false, false, &hits)
		c, emitted := newTestChecker(t, v, srv.URL)
		if c.enabled() {
			t.Errorf("enabled() = true for version %q", v)
		}
		info, err := c.check(context.Background())
		if err != nil || info.Available || hits.Load() != 0 || len(*emitted) != 0 {
			t.Errorf("version %q: err=%v info=%+v hits=%d emitted=%+v", v, err, info, hits.Load(), *emitted)
		}
	}
}

func TestResolveUpdateURL(t *testing.T) {
	if got := resolveUpdateURL(""); got != defaultUpdateURL {
		t.Errorf("empty -> %q, want default", got)
	}
	if got := resolveUpdateURL("off"); got != "" {
		t.Errorf("off -> %q, want disabled", got)
	}
	if got := resolveUpdateURL(" https://mirror.example/latest "); got != "https://mirror.example/latest" {
		t.Errorf("override -> %q", got)
	}
	c, _ := newTestChecker(t, "1.2.3", "")
	if c.enabled() {
		t.Error("a release build with CALENDIUM_UPDATE_URL=off must be disabled")
	}
}

// Review Focus 2: a 200 that is not a usable release must not emit or poison the cache.
func TestUpdateCheck_NonSemverTagOrBadJSONErrorsWithoutEmitOrCache(t *testing.T) {
	for _, body := range []string{`{"tag_name":"nightly","html_url":"https://x","draft":false,"prerelease":false}`, `<html>rate limited</html>`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"bad"`)
			_, _ = fmt.Fprint(w, body)
		}))
		c, emitted := newTestChecker(t, "1.2.3", srv.URL)
		info, err := c.check(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("body %q: expected an error", body)
		}
		if info.Available || len(*emitted) != 0 {
			t.Errorf("body %q: info=%+v emitted=%+v", body, info, *emitted)
		}
		if _, statErr := os.Stat(c.cachePath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("body %q: cache must not be written, stat err = %v", body, statErr)
		}
	}
}

// Review Focus 3: a corrupt cache is ignored, not fatal.
func TestUpdateCheck_CorruptCacheIsIgnoredAndRewritten(t *testing.T) {
	var hits atomic.Int32
	var sawConditional atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			sawConditional.Store(true)
		}
		w.Header().Set("ETag", `"etag-2"`)
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.3.0","html_url":"https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0","draft":false,"prerelease":false}`)
	}))
	t.Cleanup(srv.Close)
	c, emitted := newTestChecker(t, "1.2.3", srv.URL)
	if err := os.WriteFile(c.cachePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := c.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sawConditional.Load() {
		t.Error("corrupt cache must not produce an If-None-Match header")
	}
	if !info.Available || len(*emitted) != 1 {
		t.Fatalf("info=%+v emitted=%+v", info, *emitted)
	}
	b, _ := os.ReadFile(c.cachePath)
	if !strings.Contains(string(b), `"etag":"\"etag-2\""`) {
		t.Fatalf("cache not rewritten with the fresh ETag: %s", b)
	}
}

func TestParseSemverAndNewer(t *testing.T) {
	parse := []struct {
		in   string
		ok   bool
		want semver
	}{
		{"1.2.3", true, semver{1, 2, 3, ""}},
		{"v1.2.3", true, semver{1, 2, 3, ""}},
		{"1.10.0", true, semver{1, 10, 0, ""}},
		{"1.2.3-rc.1", true, semver{1, 2, 3, "rc.1"}},
		{"0.0.0-dev.abc1234", true, semver{0, 0, 0, "dev.abc1234"}},
		{"1.2.3+build.5", true, semver{1, 2, 3, ""}},
		{"dev", false, semver{}},
		{"1.2", false, semver{}},
		{"1.02.3", false, semver{}},
		{"1.2.3-", false, semver{}},
		{"", false, semver{}},
	}
	for _, tc := range parse {
		got, ok := parseSemver(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseSemver(%q) = %+v,%t want %+v,%t", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	cmp := []struct {
		latest, current string
		want            bool
	}{
		{"1.3.0", "1.2.9", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexical
		{"2.0.0", "1.99.99", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"1.2.3", "1.2.3-rc.1", true},  // release beats prerelease
		{"1.2.3-rc.1", "1.2.3", false}, // prerelease never beats the release
		{"1.2.3-rc.2", "1.2.3-rc.1", true},
	}
	for _, tc := range cmp {
		l, _ := parseSemver(tc.latest)
		c, _ := parseSemver(tc.current)
		if got := newer(l, c); got != tc.want {
			t.Errorf("newer(%s, %s) = %t, want %t", tc.latest, tc.current, got, tc.want)
		}
	}
	for v, want := range map[string]bool{"1.2.3": true, "1.2.3-rc.1": false, "dev": false, "0.0.0-dev.abc1234": false} {
		if got := isReleaseVersion(v); got != want {
			t.Errorf("isReleaseVersion(%q) = %t, want %t", v, got, want)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `cd apps/desktop && go test -run 'TestUpdateCheck|TestResolveUpdateURL|TestParseSemver' ./...; cd ../..`

Expected: compile error `undefined: updateChecker` (and `UpdateInfo`, `semver`, …).

- [ ] **Step 3: Write `apps/desktop/update.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Update check v1 = notification only. A release build asks GitHub for the
// latest release once shortly after launch and then daily, and tells the
// frontend when something newer exists. No download, no self-replace.
//
// Privacy: the only request is a conditional GET of the public releases
// endpoint carrying a User-Agent; no query parameters, no identifiers.

// updateAvailableEvent is emitted to the frontend (runtime.EventsOn) with an
// UpdateInfo payload when a newer release than `version` exists.
const updateAvailableEvent = "update-available"

const (
	defaultUpdateURL   = "https://api.github.com/repos/GuilhermeVozniak/calendium/releases/latest"
	updateCheckTimeout = 5 * time.Second
	updateInitialDelay = 10 * time.Second // never delays launch
	updateInterval     = 24 * time.Hour
	updateCacheFile    = "update-check.json"
	updateBodyLimit    = 1 << 20
)

// UpdateInfo is the update-available payload and the GetUpdateStatus result.
type UpdateInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}

// updateCache memoizes the last successful check so the next request can be
// conditional (ETag → 304) and still report the release it described.
type updateCache struct {
	ETag      string    `json:"etag"`
	TagName   string    `json:"tagName"`
	HTMLURL   string    `json:"htmlUrl"`
	CheckedAt time.Time `json:"checkedAt"`
}

type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver accepts X.Y.Z or X.Y.Z-pre (a leading "v" is tolerated because
// GitHub tags are vX.Y.Z); build metadata after "+" is dropped; leading zeros
// are rejected.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
		if v.pre == "" {
			return semver{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var nums [3]int
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, true
}

// newer reports whether latest is strictly newer than current: numeric
// major.minor.patch compare; at equal numbers a release beats a prerelease
// and two prereleases compare lexically.
func newer(latest, current semver) bool {
	if latest.major != current.major {
		return latest.major > current.major
	}
	if latest.minor != current.minor {
		return latest.minor > current.minor
	}
	if latest.patch != current.patch {
		return latest.patch > current.patch
	}
	if latest.pre == "" && current.pre != "" {
		return true
	}
	if latest.pre != "" && current.pre == "" {
		return false
	}
	return latest.pre > current.pre
}

// isReleaseVersion is true only for a plain X.Y.Z, so "dev" and
// 0.0.0-dev.<sha> builds never nag (or request anything).
func isReleaseVersion(v string) bool {
	parsed, ok := parseSemver(v)
	return ok && parsed.pre == ""
}

// resolveUpdateURL applies CALENDIUM_UPDATE_URL: empty → GitHub, "off" →
// disabled (empty string), anything else → that URL.
func resolveUpdateURL(env string) string {
	env = strings.TrimSpace(env)
	switch env {
	case "":
		return defaultUpdateURL
	case "off":
		return ""
	default:
		return env
	}
}

func defaultUpdateCachePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Calendium", updateCacheFile)
}

type updateChecker struct {
	version   string
	url       string // "" disables
	hc        *http.Client
	cachePath string // "" disables the on-disk cache
	now       func() time.Time

	mu     sync.Mutex
	emit   func(UpdateInfo)
	latest UpdateInfo
}

func newUpdateChecker(version string) *updateChecker {
	return &updateChecker{
		version:   version,
		url:       resolveUpdateURL(os.Getenv("CALENDIUM_UPDATE_URL")),
		hc:        &http.Client{Timeout: updateCheckTimeout},
		cachePath: defaultUpdateCachePath(),
		now:       time.Now,
		latest:    UpdateInfo{Current: version},
	}
}

// setEmit wires the sink that receives a newer release exactly once per
// version seen (runtime.EventsEmit in production; a recorder in tests).
func (c *updateChecker) setEmit(emit func(UpdateInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emit = emit
}

// enabled: only release builds with a URL ever talk to the network.
func (c *updateChecker) enabled() bool {
	return c.url != "" && isReleaseVersion(c.version)
}

// status returns the last result (zero-value Available=false before any check).
func (c *updateChecker) status() UpdateInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// check performs one conditional GET and returns the resulting status. Errors
// leave the previous status untouched and never emit.
func (c *updateChecker) check(ctx context.Context) (UpdateInfo, error) {
	if !c.enabled() {
		return c.status(), nil
	}
	cached := c.readCache()
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return c.status(), err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Calendium-Desktop/"+c.version)
	if cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.status(), err
	}
	defer func() { _ = resp.Body.Close() }()

	var rel githubRelease
	switch resp.StatusCode {
	case http.StatusNotModified:
		if cached.TagName == "" {
			return c.status(), errors.New("update check: 304 without a cached release")
		}
		rel = githubRelease{TagName: cached.TagName, HTMLURL: cached.HTMLURL}
	case http.StatusOK:
		if err := json.NewDecoder(io.LimitReader(resp.Body, updateBodyLimit)).Decode(&rel); err != nil {
			return c.status(), fmt.Errorf("update check: decode: %w", err)
		}
		if rel.Draft || rel.Prerelease {
			return c.status(), nil
		}
		if _, ok := parseSemver(rel.TagName); !ok {
			return c.status(), fmt.Errorf("update check: tag %q is not semver", rel.TagName)
		}
		c.writeCache(updateCache{ETag: resp.Header.Get("ETag"), TagName: rel.TagName, HTMLURL: rel.HTMLURL, CheckedAt: c.now()})
	default:
		return c.status(), fmt.Errorf("update check: unexpected status %d", resp.StatusCode)
	}
	return c.apply(rel), nil
}

// apply compares a release against the running version, records the result
// and emits when it is newer and not already announced.
func (c *updateChecker) apply(rel githubRelease) UpdateInfo {
	latest, ok := parseSemver(rel.TagName)
	current, _ := parseSemver(c.version)
	info := UpdateInfo{Current: c.version, Latest: strings.TrimPrefix(rel.TagName, "v"), URL: rel.HTMLURL}
	info.Available = ok && newer(latest, current)

	c.mu.Lock()
	prev := c.latest
	c.latest = info
	emit := c.emit
	c.mu.Unlock()

	if info.Available && emit != nil && (!prev.Available || prev.Latest != info.Latest) {
		emit(info)
	}
	return info
}

func (c *updateChecker) readCache() updateCache {
	if c.cachePath == "" {
		return updateCache{}
	}
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		return updateCache{}
	}
	var cached updateCache
	if err := json.Unmarshal(b, &cached); err != nil {
		return updateCache{} // corrupt → behave as if there were no cache
	}
	return cached
}

func (c *updateChecker) writeCache(cache updateCache) {
	if c.cachePath == "" {
		return
	}
	b, err := json.Marshal(cache)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.cachePath, b, 0o600)
}

// run blocks until ctx is done: first check after `initial`, then every
// `every`. Errors are logged; the loop never panics the host.
func (c *updateChecker) run(ctx context.Context, initial, every time.Duration) {
	if !c.enabled() {
		return
	}
	timer := time.NewTimer(initial)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if _, err := c.check(ctx); err != nil {
		log.Printf("update check: %v", err)
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.check(ctx); err != nil {
				log.Printf("update check: %v", err)
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/desktop && go vet ./... && go test -run 'TestUpdateCheck|TestResolveUpdateURL|TestParseSemver' -race ./...; cd ../..`

Expected: PASS (9 tests; the timeout test takes ~0.3 s).

- [ ] **Step 5: Commit**

```
git add apps/desktop/update.go apps/desktop/update_test.go
git commit -m "feat(desktop): stdlib GitHub release update checker with ETag cache and semver compare" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 8: Wire update checks into the App and the Wails lifecycle (Track B)

**Files:**
- Modify: `apps/desktop/app.go` (struct fields, `NewApp`, new methods `GetUpdateStatus`, `startUpdateChecks`, `stopUpdateChecks`)
- Modify: `apps/desktop/main.go` (`OnStartup`)
- Modify: `apps/desktop/tray.go:233-236` (`shutdown` calls `stopUpdateChecks`)
- Modify: `apps/desktop/app_test.go` (two tests)

**Interfaces:**
- Consumes: `updateChecker` API (Task 7).
- Produces: binding `func (a *App) GetUpdateStatus() UpdateInfo` (exposed as `window.go.main.App.GetUpdateStatus`, Task 10); event `update-available`.

- [ ] **Step 1: Write the failing tests (append to `apps/desktop/app_test.go`)**

```go
func TestGetUpdateStatus_DefaultBeforeAnyCheck(t *testing.T) {
	a := NewApp()
	got := a.GetUpdateStatus()
	want := UpdateInfo{Available: false, Current: version}
	if got != want {
		t.Fatalf("GetUpdateStatus() = %+v, want %+v", got, want)
	}
}

func TestStartStopUpdateChecks_DevBuildIsInertAndStoppable(t *testing.T) {
	// version is "dev" under `go test`, so the loop must exit without ever
	// touching the network or emitting; stop must be safe to call twice.
	a := NewApp()
	a.startUpdateChecks(context.Background())
	a.mu.Lock()
	cancel := a.updateCancel
	a.mu.Unlock()
	if cancel == nil {
		t.Fatal("startUpdateChecks did not record a cancel func")
	}
	a.stopUpdateChecks()
	a.stopUpdateChecks()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updateCancel != nil {
		t.Fatal("stopUpdateChecks must clear the cancel func")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `cd apps/desktop && go test -run 'TestGetUpdateStatus|TestStartStopUpdateChecks' ./...; cd ../..`

Expected: compile error `a.GetUpdateStatus undefined`.

- [ ] **Step 3: Add the fields and methods to `apps/desktop/app.go`**

In the `App` struct, after `autoJoin *autoJoinScheduler`:

```go
	// Update notifications (update.go): background GitHub release check.
	updates      *updateChecker
	updateCancel context.CancelFunc
```

In `NewApp`, before `a.initDesktopExtras()`:

```go
	a.updates = newUpdateChecker(version)
```

Append after `GetAppVersion`:

```go
// GetUpdateStatus returns the last update-check result. A late-mounted UI
// reads this instead of waiting up to 24 h for the next update-available
// event; before any check it is {Available: false, Current: version}.
func (a *App) GetUpdateStatus() UpdateInfo {
	return a.updates.status()
}

// startUpdateChecks wires the event sink and starts the background loop
// (first check after 10 s so launch is never delayed, then every 24 h).
// Called from OnStartup (main.go); a dev build returns immediately inside run.
func (a *App) startUpdateChecks(ctx context.Context) {
	a.updates.setEmit(func(info UpdateInfo) {
		runtime.EventsEmit(ctx, updateAvailableEvent, info)
	})
	runCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.updateCancel = cancel
	a.mu.Unlock()
	go a.updates.run(runCtx, updateInitialDelay, updateInterval)
}

// stopUpdateChecks ends the loop (OnShutdown). Safe to call repeatedly.
func (a *App) stopUpdateChecks() {
	a.mu.Lock()
	cancel := a.updateCancel
	a.updateCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
```

- [ ] **Step 4: Hook the lifecycle**

`apps/desktop/main.go` `OnStartup` becomes:

```go
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			// Menu-bar tray + auto-join (tray.go / scheduler.go). Started here
			// rather than inside startup so unit tests exercising startup never
			// touch the native systray loop.
			app.startDesktopExtras(ctx)
			// Daily GitHub release check (update.go); inert on dev builds.
			app.startUpdateChecks(ctx)
		},
```

`apps/desktop/tray.go` `shutdown` becomes:

```go
// shutdown is the Wails OnShutdown hook (main.go): stops the tray loop,
// countdown ticker, any pending auto-join timer, and the update-check loop.
func (a *App) shutdown(_ context.Context) {
	a.tray.stop()
	a.autoJoin.SetConfig(false, 0)
	a.stopUpdateChecks()
}
```

- [ ] **Step 5: Run the desktop suite**

Run: `cd apps/desktop && go vet ./... && go test -race ./...; cd ../..`

Expected: PASS.

- [ ] **Step 6: Commit**

```
git add apps/desktop/app.go apps/desktop/app_test.go apps/desktop/main.go apps/desktop/tray.go
git commit -m "feat(desktop): GetUpdateStatus binding and update-available event wired into startup/shutdown" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 9: Deep links on every platform — argv, single instance, Linux desktop entry (Track B)

**Files:**
- Modify: `apps/desktop/app.go` (`deepLinkFromArgs`, `consumeArgs`, `onSecondInstance`; imports `strings`, `github.com/wailsapp/wails/v2/pkg/options`)
- Modify: `apps/desktop/main.go` (`consumeArgs(os.Args[1:])`, `SingleInstanceLock`; import `os`)
- Create: `apps/desktop/build/linux/calendium.desktop`, `apps/desktop/build/linux/README.txt`
- Modify: `apps/desktop/app_test.go` (argv tests)
- Modify: `apps/desktop/identity_test.go` (desktop entry test)

**Interfaces:**
- Consumes: `handleURL`/`pendingURL` (existing), `options.SecondInstanceData{Args []string}` (Wails v2.12.0 `pkg/options/options.go:196`).
- Produces: `func deepLinkFromArgs(args []string) string`; `func (a *App) consumeArgs(args []string)`; `func (a *App) onSecondInstance(data options.SecondInstanceData)`; files `build/linux/calendium.desktop` and `build/linux/README.txt` that Task 14 copies into the Linux tarball.

- [ ] **Step 1: Write the failing tests**

Append to `apps/desktop/app_test.go`:

```go
func TestDeepLinkFromArgs_PicksTheFirstCalendiumURLFromMixedArgv(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"url after flags", []string{"--no-sandbox", "calendium://auth/callback?ott=abc123"}, "calendium://auth/callback?ott=abc123"},
		{"first of two", []string{"calendium://first", "calendium://second"}, "calendium://first"},
		{"scheme is case-insensitive", []string{"CALENDIUM://accounts/connected?status=ok"}, "CALENDIUM://accounts/connected?status=ok"},
		{"no link", []string{"https://example.com", "-x"}, ""},
		{"embedded, not a bare arg", []string{"--url=calendium://x"}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		if got := deepLinkFromArgs(tc.args); got != tc.want {
			t.Errorf("%s: deepLinkFromArgs(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestConsumeArgs_BuffersAColdLaunchURLBeforeStartup(t *testing.T) {
	a := NewApp()
	a.consumeArgs([]string{"--flag", "calendium://auth/callback?ott=abc123"})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingURL != "calendium://auth/callback?ott=abc123" {
		t.Fatalf("pendingURL = %q", a.pendingURL)
	}
}

func TestOnSecondInstance_ForwardsTheLinkAndIgnoresPlainRelaunches(t *testing.T) {
	// Pre-startup (ctx nil) so handleURL buffers instead of reaching
	// runtime.EventsEmit, and WindowShow is skipped (no Wails context).
	a := NewApp()
	a.onSecondInstance(options.SecondInstanceData{Args: []string{"Calendium.exe", "calendium://accounts/connected?status=ok"}, WorkingDirectory: "C:\\"})
	a.mu.Lock()
	got := a.pendingURL
	a.mu.Unlock()
	if got != "calendium://accounts/connected?status=ok" {
		t.Fatalf("pendingURL = %q", got)
	}

	b := NewApp()
	b.onSecondInstance(options.SecondInstanceData{Args: []string{"Calendium.exe"}})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingURL != "" {
		t.Fatalf("a relaunch without a link must not buffer anything, got %q", b.pendingURL)
	}
}
```

Add `"github.com/wailsapp/wails/v2/pkg/options"` to the test file's imports.

Append to `apps/desktop/identity_test.go`:

```go
func TestLinuxDesktopEntry_RegistersTheSchemeAndMatchesTheBinary(t *testing.T) {
	b, err := os.ReadFile("build/linux/calendium.desktop")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, line := range []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=Calendium",
		"Exec=Calendium %u",
		"Icon=calendium",
		"MimeType=x-scheme-handler/calendium;",
		"StartupWMClass=Calendium",
	} {
		if !strings.Contains(s, line+"\n") {
			t.Errorf("calendium.desktop missing line %q", line)
		}
	}
	readme, err := os.ReadFile("build/linux/README.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install -Dm755 Calendium", "install -Dm644 calendium.desktop", "xdg-mime default calendium.desktop x-scheme-handler/calendium"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.txt missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `cd apps/desktop && go test -run 'TestDeepLinkFromArgs|TestConsumeArgs|TestOnSecondInstance|TestLinuxDesktopEntry' ./...; cd ../..`

Expected: compile error `undefined: deepLinkFromArgs`.

- [ ] **Step 3: Implement in `apps/desktop/app.go`**

Add to imports: `"strings"` and `"github.com/wailsapp/wails/v2/pkg/options"`. Append after `handleURL`:

```go
// deepLinkScheme prefixes every URL the OS hands us for the calendium scheme.
const deepLinkScheme = "calendium://"

// deepLinkFromArgs returns the first bare calendium:// argument. Windows (NSIS
// registers "Calendium.exe" "%1") and Linux (.desktop Exec=Calendium %u) pass
// the opened URL as argv; macOS delivers it via OnUrlOpen and never does.
func deepLinkFromArgs(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), deepLinkScheme) {
			return arg
		}
	}
	return ""
}

// consumeArgs handles a cold launch via URL: main() calls it before wails.Run,
// so the link lands in pendingURL and startup flushes it once the WebView is up.
func (a *App) consumeArgs(args []string) {
	if u := deepLinkFromArgs(args); u != "" {
		a.handleURL(u)
	}
}

// onSecondInstance is the SingleInstanceLock callback: the OS started a second
// Calendium process (typically to open a calendium:// URL). Forward the link
// to this running instance and bring its window forward; a plain relaunch
// with no link just surfaces the window.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	if u := deepLinkFromArgs(data.Args); u != "" {
		a.handleURL(u)
	}
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.WindowShow(ctx)
	}
}
```

- [ ] **Step 4: Wire `main.go`**

Add `"os"` to imports. After `app := NewApp()`:

```go
	// Cold launch via URL on Windows/Linux: the OS passes the calendium:// link
	// as an argument; buffer it before the WebView exists (macOS uses OnUrlOpen).
	app.consumeArgs(os.Args[1:])
```

Inside `options.App{...}`, after `Bind`:

```go
		// One running instance per user: a second launch (how Windows/Linux
		// deliver calendium:// links to an already-open app) forwards its argv
		// to onSecondInstance instead of opening a second window.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "app.calendium.desktop",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
```

- [ ] **Step 5: Create the Linux files**

`apps/desktop/build/linux/calendium.desktop`:

```
[Desktop Entry]
Type=Application
Name=Calendium
Comment=Email and calendar, at the speed of thought
Exec=Calendium %u
Icon=calendium
Terminal=false
Categories=Network;Email;Calendar;Office;
MimeType=x-scheme-handler/calendium;
StartupWMClass=Calendium
```

`apps/desktop/build/linux/README.txt`:

```
Calendium for Linux
===================

This tarball contains the Calendium binary, a .desktop entry, and a 512 px icon.
Install for the current user:

  install -Dm755 Calendium ~/.local/bin/Calendium
  install -Dm644 calendium.png ~/.local/share/icons/hicolor/512x512/apps/calendium.png
  install -Dm644 calendium.desktop ~/.local/share/applications/calendium.desktop
  xdg-mime default calendium.desktop x-scheme-handler/calendium
  update-desktop-database ~/.local/share/applications

The xdg-mime line registers the calendium:// scheme so sign-in and
mailbox-connect links from your browser open in the app (a second launch
forwards the link to the running instance). ~/.local/bin must be on PATH for
"Exec=Calendium %u" to resolve; otherwise edit Exec= to the full path.

The desktop app checks GitHub once a day for a newer release and shows a
banner; set CALENDIUM_UPDATE_URL=off to disable.
```

- [ ] **Step 6: Run the desktop suite and lint**

Run: `cd apps/desktop && go vet ./... && go test -race ./... && golangci-lint run ./...; cd ../..`

Expected: PASS, `0 issues`.

- [ ] **Step 7: Commit**

```
git add apps/desktop/app.go apps/desktop/app_test.go apps/desktop/main.go apps/desktop/identity_test.go apps/desktop/build/linux
git commit -m "feat(desktop): calendium:// via argv + single-instance lock on Windows/Linux, Linux desktop entry" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 10: Frontend bridge — `GetUpdateStatus`, `UPDATE_EVENT`, `onUpdateAvailable` (Track B)

**Files:**
- Modify: `apps/desktop/frontend/src/lib/wails.ts`
- Modify: `apps/desktop/frontend/src/lib/wails.test.ts` (browser-fallback case, `installBridge`, bridge cases)

**Interfaces:**
- Consumes: Go binding `GetUpdateStatus()` and event `update-available` (Task 8).
- Produces: `export interface UpdateInfo {available: boolean; current: string; latest: string; url: string}`; `DesktopBindings.GetUpdateStatus(): Promise<UpdateInfo>`; `export const UPDATE_EVENT = 'update-available'`; `export function onUpdateAvailable(handler: (info: UpdateInfo) => void): () => void`.

- [ ] **Step 1: Write the failing tests**

In `wails.test.ts`, add to the browser-fallback `describe` (after the `GetAppVersion` case):

```ts
  it('GetUpdateStatus resolves to "no update" in the browser', async () => {
    const { desktop } = await import('./wails');
    await expect(desktop.GetUpdateStatus()).resolves.toEqual({
      available: false,
      current: 'dev (browser)',
      latest: '',
      url: '',
    });
  });

  it('onUpdateAvailable no-ops gracefully and returns an unsubscribe', async () => {
    const { onUpdateAvailable } = await import('./wails');
    const handler = vi.fn();
    const unsubscribe = onUpdateAvailable(handler);
    expect(() => unsubscribe()).not.toThrow();
    expect(handler).not.toHaveBeenCalled();
  });
```

Replace the existing `installBridge()` helper inside the WebView `describe` with this version (adds `GetUpdateStatus`; everything else is unchanged):

```ts
  function installBridge() {
    const OpenExternal = vi.fn().mockResolvedValue(undefined);
    const GetAppVersion = vi.fn().mockResolvedValue('1.2.3');
    const GetUpdateStatus = vi
      .fn()
      .mockResolvedValue({ available: false, current: '1.2.3', latest: '', url: '' });
    const SetGlobalShortcutsEnabled = vi.fn().mockResolvedValue(undefined);
    const EventsOn = vi.fn((_eventName: string, _cb: (...data: unknown[]) => void) => vi.fn());
    const EventsEmit = vi.fn();
    const WindowSetTitle = vi.fn();
    const WindowShow = vi.fn();
    const BrowserOpenURL = vi.fn();
    (window as unknown as { go: unknown }).go = {
      main: { App: { OpenExternal, GetAppVersion, GetUpdateStatus, SetGlobalShortcutsEnabled } },
    };
    (window as unknown as { runtime: unknown }).runtime = {
      EventsOn,
      EventsEmit,
      WindowSetTitle,
      WindowShow,
      BrowserOpenURL,
    };
    return {
      OpenExternal,
      GetAppVersion,
      GetUpdateStatus,
      SetGlobalShortcutsEnabled,
      EventsOn,
      EventsEmit,
      WindowSetTitle,
      WindowShow,
      BrowserOpenURL,
    };
  }
```

Add a new nested `describe` inside the WebView `describe`:

```ts
  describe('update notifications', () => {
    const NEWER = {
      available: true,
      current: '1.2.3',
      latest: '1.3.0',
      url: 'https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0',
    };

    it('exposes the bound GetUpdateStatus', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { desktop } = await import('./wails');
      expect(desktop.GetUpdateStatus).toBe(bridge.GetUpdateStatus);
    });

    it('onUpdateAvailable subscribes on the update-available event name', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onUpdateAvailable, UPDATE_EVENT } = await import('./wails');
      onUpdateAvailable(vi.fn());
      expect(bridge.EventsOn).toHaveBeenCalledTimes(1);
      expect(bridge.EventsOn.mock.calls[0]?.[0]).toBe(UPDATE_EVENT);
      expect(UPDATE_EVENT).toBe('update-available');
    });

    it('forwards a well-formed newer-release payload', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onUpdateAvailable } = await import('./wails');
      const handler = vi.fn();
      onUpdateAvailable(handler);
      const cb = bridge.EventsOn.mock.calls[0]?.[1] as (...data: unknown[]) => void;
      cb(NEWER);
      expect(handler).toHaveBeenCalledWith(NEWER);
    });

    it('ignores not-available and malformed payloads', async () => {
      const bridge = installBridge();
      vi.resetModules();
      const { onUpdateAvailable } = await import('./wails');
      const handler = vi.fn();
      onUpdateAvailable(handler);
      const cb = bridge.EventsOn.mock.calls[0]?.[1] as (...data: unknown[]) => void;
      cb({ ...NEWER, available: false });
      cb({ available: true, latest: 42, url: 'x' });
      cb({ available: true, latest: '1.3.0' });
      cb('1.3.0');
      cb(null);
      cb();
      expect(handler).not.toHaveBeenCalled();
    });
  });
```

- [ ] **Step 2: Run and watch them fail**

Run: `cd apps/desktop/frontend && bunx vitest run src/lib/wails.test.ts; cd ../../..`

Expected: FAIL — `desktop.GetUpdateStatus is not a function`, `onUpdateAvailable` not exported.

- [ ] **Step 3: Implement in `wails.ts`**

Above `DesktopBindings`:

```ts
/**
 * Result of the host's daily GitHub release check (apps/desktop/update.go):
 * the update-available event payload and the GetUpdateStatus return value.
 */
export interface UpdateInfo {
  available: boolean;
  current: string;
  latest: string;
  url: string;
}
```

In `DesktopBindings`, after `GetAppVersion(): Promise<string>;`:

```ts
  /** Last update-check result; safe to call before any check ran. */
  GetUpdateStatus(): Promise<UpdateInfo>;
```

In `browserFallback`, after `GetAppVersion`:

```ts
  async GetUpdateStatus() {
    return { available: false, current: 'dev (browser)', latest: '', url: '' };
  },
```

Append after `onDeepLink`:

```ts
/** Event the Go host emits (apps/desktop/update.go) when a newer release exists. */
export const UPDATE_EVENT = 'update-available';

/**
 * Subscribe to newer-release notifications. Only well-formed, available
 * payloads reach the handler. Returns an unsubscribe; no-ops in a browser.
 */
export function onUpdateAvailable(handler: (info: UpdateInfo) => void): () => void {
  return wailsRuntime.EventsOn(UPDATE_EVENT, (...data: unknown[]) => {
    const payload = data[0];
    if (typeof payload !== 'object' || payload === null) return;
    const info = payload as Partial<UpdateInfo>;
    if (info.available !== true || typeof info.latest !== 'string' || typeof info.url !== 'string') {
      return;
    }
    handler({
      available: true,
      current: typeof info.current === 'string' ? info.current : '',
      latest: info.latest,
      url: info.url,
    });
  });
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/desktop/frontend && bunx vitest run src/lib/wails.test.ts && bunx tsc --noEmit; cd ../../..`

Expected: PASS (all wails cases), `tsc` clean.

- [ ] **Step 5: Commit**

```
git add apps/desktop/frontend/src/lib/wails.ts apps/desktop/frontend/src/lib/wails.test.ts
git commit -m "feat(desktop): typed GetUpdateStatus binding and update-available subscription in the Wails bridge" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 11: `UpdateBanner` view mounted in `App.tsx` (Track B)

**Files:**
- Create: `apps/desktop/frontend/src/views/UpdateBanner.tsx`
- Create: `apps/desktop/frontend/src/views/UpdateBanner.test.tsx`
- Modify: `apps/desktop/frontend/src/App.tsx` (import + mount after `</header>`)

**Interfaces:**
- Consumes: `desktop.GetUpdateStatus`, `desktop.OpenExternal`, `onUpdateAvailable`, `UpdateInfo` (Task 10); `Button` from `@/ui/button`.
- Produces: `export function UpdateBanner(): JSX.Element | null`; localStorage key `calendium.update.dismissed`.

- [ ] **Step 1: Write the failing test `UpdateBanner.test.tsx`**

```tsx
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({
  GetUpdateStatus: vi.fn(),
  OpenExternal: vi.fn(),
  listeners: [] as Array<(info: unknown) => void>,
}));

vi.mock('@/lib/wails', () => ({
  desktop: { GetUpdateStatus: bridge.GetUpdateStatus, OpenExternal: bridge.OpenExternal },
  onUpdateAvailable: (handler: (info: unknown) => void) => {
    bridge.listeners.push(handler);
    return () => {
      bridge.listeners = bridge.listeners.filter((h) => h !== handler);
    };
  },
}));

import { UpdateBanner } from './UpdateBanner';

const NONE = { available: false, current: '1.2.3', latest: '', url: '' };
const NEWER = {
  available: true,
  current: '1.2.3',
  latest: '1.3.0',
  url: 'https://github.com/GuilhermeVozniak/calendium/releases/tag/v1.3.0',
};

async function flush() {
  await act(async () => {});
}

beforeEach(() => {
  localStorage.clear();
  bridge.listeners = [];
  bridge.GetUpdateStatus.mockReset().mockResolvedValue(NONE);
  bridge.OpenExternal.mockReset();
});

describe('UpdateBanner', () => {
  it('renders nothing while no update is available', async () => {
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('shows the newer version from GetUpdateStatus (late mount) and Download opens the release', async () => {
    bridge.GetUpdateStatus.mockResolvedValue(NEWER);
    render(<UpdateBanner />);
    await flush();
    expect(screen.getByRole('status').textContent).toContain('Calendium 1.3.0 is available');
    await userEvent.click(screen.getByRole('button', { name: /download/i }));
    expect(bridge.OpenExternal).toHaveBeenCalledWith(NEWER.url);
  });

  it('shows when the host emits update-available after mount', async () => {
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
    act(() => {
      for (const l of bridge.listeners) l(NEWER);
    });
    expect(screen.getByRole('status').textContent).toContain('1.3.0');
  });

  it('Later hides the banner and persists the dismissed version', async () => {
    bridge.GetUpdateStatus.mockResolvedValue(NEWER);
    const first = render(<UpdateBanner />);
    await flush();
    await userEvent.click(screen.getByRole('button', { name: /later/i }));
    expect(screen.queryByRole('status')).toBeNull();
    expect(localStorage.getItem('calendium.update.dismissed')).toBe('1.3.0');
    first.unmount();
    render(<UpdateBanner />);
    await flush();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('a newer version than the dismissed one re-shows', async () => {
    localStorage.setItem('calendium.update.dismissed', '1.3.0');
    bridge.GetUpdateStatus.mockResolvedValue({ ...NEWER, latest: '1.4.0' });
    render(<UpdateBanner />);
    await flush();
    expect(screen.getByRole('status').textContent).toContain('Calendium 1.4.0 is available');
  });

  it('unsubscribes on unmount', async () => {
    const view = render(<UpdateBanner />);
    await flush();
    expect(bridge.listeners).toHaveLength(1);
    view.unmount();
    expect(bridge.listeners).toHaveLength(0);
  });
});
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd apps/desktop/frontend && bunx vitest run src/views/UpdateBanner.test.tsx; cd ../../..`

Expected: FAIL — `Failed to resolve import "./UpdateBanner"`.

- [ ] **Step 3: Write `UpdateBanner.tsx`**

```tsx
import { Download, X } from 'lucide-react';
import { useEffect, useState } from 'react';

import { desktop, onUpdateAvailable, type UpdateInfo } from '@/lib/wails';
import { Button } from '@/ui/button';

const DISMISSED_KEY = 'calendium.update.dismissed';

function dismissedVersion(): string {
  try {
    return localStorage.getItem(DISMISSED_KEY) ?? '';
  } catch {
    return '';
  }
}

function persistDismissed(version: string): void {
  try {
    localStorage.setItem(DISMISSED_KEY, version);
  } catch {
    // Best effort: the banner still hides for this session.
  }
}

/**
 * One-line "a newer Calendium is available" bar (update v1 = notify only; no
 * in-place updater). Mounted between the titlebar and the body in App.tsx.
 * Reads GetUpdateStatus on mount (the host may have checked before this view
 * existed) and listens for update-available afterwards. Later hides it for
 * that version only; a newer release re-shows it. No modal, no toast.
 */
export function UpdateBanner() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [dismissed, setDismissed] = useState<string>(() => dismissedVersion());

  useEffect(() => {
    let active = true;
    void desktop.GetUpdateStatus().then((status) => {
      if (active && status.available) setInfo(status);
    });
    const off = onUpdateAvailable((next) => {
      if (active) setInfo(next);
    });
    return () => {
      active = false;
      off();
    };
  }, []);

  if (!info?.available || info.latest === dismissed) return null;

  return (
    <div
      role="status"
      className="flex h-9 shrink-0 items-center gap-3 border-b bg-accent/40 px-3 text-xs"
    >
      <span className="font-medium">Calendium {info.latest} is available.</span>
      <span className="text-muted-foreground">You have {info.current}.</span>
      <div className="ml-auto flex items-center gap-1">
        <Button
          variant="outline"
          size="sm"
          className="h-7"
          onClick={() => void desktop.OpenExternal(info.url)}
        >
          <Download /> Download
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="h-7"
          onClick={() => {
            persistDismissed(info.latest);
            setDismissed(info.latest);
          }}
        >
          <X /> Later
        </Button>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Mount it in `App.tsx`**

Add the import next to the other views:

```ts
import { UpdateBanner } from '@/views/UpdateBanner';
```

Directly after the closing `</header>` of the titlebar (before `<div className="flex min-h-0 flex-1">`):

```tsx
      <UpdateBanner />
```

- [ ] **Step 5: Run the desktop frontend suite, typecheck, lint and build**

Run: `bun run test:desktop && cd apps/desktop/frontend && bunx tsc --noEmit && bun run build && cd ../../.. && bunx biome check apps/desktop/frontend`

Expected: all PASS; `vite build` succeeds (main.go embeds `frontend/dist`).

- [ ] **Step 6: Commit**

```
git add apps/desktop/frontend/src/views/UpdateBanner.tsx apps/desktop/frontend/src/views/UpdateBanner.test.tsx apps/desktop/frontend/src/App.tsx
git commit -m "feat(desktop): one-line update banner with Download / Later" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 12: `scripts/set-version.mjs` with `--check` (Track C)

**Files:**
- Create: `scripts/set-version.mjs`
- Create: `scripts/set-version.test.mjs`

**Interfaces:**
- Consumes: the verbatim lines `"version": "<X.Y.Z>"` in `apps/mobile/app.json` (`expo.version`) and `"productVersion": "<X.Y.Z>"` in `apps/desktop/wails.json` (`info.productVersion`).
- Produces: CLI `node scripts/set-version.mjs X.Y.Z [--check]` (exit 0 ok / 1 drift or invalid / 2 usage); `export const SEMVER = /^\d+\.\d+\.\d+$/`; `export const VERSION_FILES`; `export async function checkVersion(rootDir, version): Promise<{file, current}[]>`; `export async function setVersion(rootDir, version): Promise<string[]>`; `export async function main(argv, rootDir): Promise<number>`. Used by root script `version:set` (Task 1) and `release.yml` (Task 14).

- [ ] **Step 1: Write the failing test `scripts/set-version.test.mjs`**

```js
import { cp, mkdir, mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { checkVersion, main, setVersion, VERSION_FILES } from './set-version.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** Temp copy of the real app.json / wails.json so tests never touch the tree. */
async function fixture() {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'set-version-'));
  for (const { file } of VERSION_FILES) {
    await mkdir(path.dirname(path.join(dir, file)), { recursive: true });
    await cp(path.join(ROOT, file), path.join(dir, file));
  }
  return dir;
}

function changedLines(before, after) {
  const a = before.split('\n');
  const b = after.split('\n');
  expect(b.length).toBe(a.length);
  return a.map((line, i) => [line, b[i]]).filter(([x, y]) => x !== y);
}

describe('set-version', () => {
  it('covers app.json expo.version and wails.json info.productVersion', () => {
    expect(VERSION_FILES.map((f) => f.file)).toEqual(['apps/mobile/app.json', 'apps/desktop/wails.json']);
  });

  it('stamps both files touching only the version line (key order, 2-space indent, trailing newline kept)', async () => {
    const dir = await fixture();
    const before = Object.fromEntries(
      await Promise.all(VERSION_FILES.map(async ({ file }) => [file, await readFile(path.join(dir, file), 'utf8')]))
    );
    const changed = await setVersion(dir, '7.8.9');
    expect(changed).toEqual(['apps/mobile/app.json', 'apps/desktop/wails.json']);
    for (const { file, path: keys } of VERSION_FILES) {
      const after = await readFile(path.join(dir, file), 'utf8');
      const diff = changedLines(before[file], after);
      expect(diff).toHaveLength(1);
      expect(diff[0][1]).toContain('"7.8.9"');
      expect(after.endsWith('\n')).toBe(true);
      expect(Object.keys(JSON.parse(after))).toEqual(Object.keys(JSON.parse(before[file])));
      expect(keys.reduce((o, k) => o[k], JSON.parse(after))).toBe('7.8.9');
    }
  });

  it('is idempotent', async () => {
    const dir = await fixture();
    await setVersion(dir, '7.8.9');
    expect(await setVersion(dir, '7.8.9')).toEqual([]);
    expect(await checkVersion(dir, '7.8.9')).toEqual([]);
  });

  // Review Focus 1: humans copy tag names; "v1.2.3" must be refused, not stamped.
  it('rejects anything that is not X.Y.Z, including a v prefix and prereleases', async () => {
    const dir = await fixture();
    for (const bad of ['v1.2.3', '1.2', '1.2.3-rc.1', '1.2.3.4', '', 'latest']) {
      await expect(setVersion(dir, bad)).rejects.toThrow(/is not X\.Y\.Z/);
      await expect(checkVersion(dir, bad)).rejects.toThrow(/is not X\.Y\.Z/);
    }
    expect(await main(['v1.2.3'], dir)).toBe(1);
    const untouched = await readFile(path.join(dir, 'apps/mobile/app.json'), 'utf8');
    expect(untouched).not.toContain('v1.2.3');
  });

  it('--check exits 1 on drift and 0 once stamped; no args is a usage error', async () => {
    const dir = await fixture();
    expect(await main(['--check', '7.8.9'], dir)).toBe(1);
    expect(await main(['7.8.9'], dir)).toBe(0);
    expect(await main(['7.8.9', '--check'], dir)).toBe(0);
    expect(await main([], dir)).toBe(2);
  });

  it('--check never writes', async () => {
    const dir = await fixture();
    const before = await readFile(path.join(dir, 'apps/desktop/wails.json'), 'utf8');
    await main(['--check', '7.8.9'], dir);
    expect(await readFile(path.join(dir, 'apps/desktop/wails.json'), 'utf8')).toBe(before);
  });
});
```

- [ ] **Step 2: Run and watch it fail**

Run: `bunx vitest run --root scripts set-version`

Expected: FAIL — `Failed to load ./set-version.mjs`.

- [ ] **Step 3: Write `scripts/set-version.mjs`**

```js
#!/usr/bin/env node
// scripts/set-version.mjs — the vX.Y.Z git tag is the single source of truth
// for every shipped version. This stamps X.Y.Z into the files that carry one:
//
//   apps/mobile/app.json      expo.version         (commit before tagging;
//                                                   release.yml --check refuses drift)
//   apps/desktop/wails.json   info.productVersion  (Wails templates it into
//                                                   Info.plist + the NSIS installer)
//
// Usage:  node scripts/set-version.mjs X.Y.Z [--check]
//   --check  write nothing; exit 1 when any file disagrees with X.Y.Z.
// Exit codes: 0 ok, 1 invalid version / drift, 2 usage.
//
// Only the exact `"<key>": "<current>"` text is replaced, so formatting and
// key order of both JSON files survive byte-for-byte.
import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const SEMVER = /^\d+\.\d+\.\d+$/;

export const VERSION_FILES = [
  { file: 'apps/mobile/app.json', path: ['expo', 'version'], key: 'version' },
  { file: 'apps/desktop/wails.json', path: ['info', 'productVersion'], key: 'productVersion' },
];

function assertSemver(version) {
  if (typeof version !== 'string' || !SEMVER.test(version)) {
    throw new Error(`set-version: "${version}" is not X.Y.Z (no "v" prefix, no prerelease, no build metadata)`);
  }
}

function getPath(obj, keys) {
  return keys.reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

async function readCurrent(rootDir, entry) {
  const raw = await readFile(path.join(rootDir, entry.file), 'utf8');
  const current = getPath(JSON.parse(raw), entry.path);
  if (typeof current !== 'string') {
    throw new Error(`set-version: ${entry.file} has no string at ${entry.path.join('.')}`);
  }
  return { raw, current };
}

/** Files whose stamped version differs from `version`: [{file, current}]. */
export async function checkVersion(rootDir, version) {
  assertSemver(version);
  const drift = [];
  for (const entry of VERSION_FILES) {
    const { current } = await readCurrent(rootDir, entry);
    if (current !== version) drift.push({ file: entry.file, current });
  }
  return drift;
}

/** Stamps `version` into every VERSION_FILES entry; returns the files changed. */
export async function setVersion(rootDir, version) {
  assertSemver(version);
  const changed = [];
  for (const entry of VERSION_FILES) {
    const { raw, current } = await readCurrent(rootDir, entry);
    if (current === version) continue;
    const needle = `"${entry.key}": "${current}"`;
    if (!raw.includes(needle)) {
      throw new Error(`set-version: ${entry.file} does not contain ${needle} verbatim`);
    }
    const next = raw.replace(needle, `"${entry.key}": "${version}"`);
    if (getPath(JSON.parse(next), entry.path) !== version) {
      throw new Error(`set-version: ${entry.file}: replacement did not land on ${entry.path.join('.')}`);
    }
    await writeFile(path.join(rootDir, entry.file), next);
    changed.push(entry.file);
  }
  return changed;
}

/** CLI entry: returns the process exit code. */
export async function main(argv, rootDir) {
  const check = argv.includes('--check');
  const [version] = argv.filter((a) => a !== '--check');
  if (!version) {
    console.error('usage: node scripts/set-version.mjs X.Y.Z [--check]');
    return 2;
  }
  try {
    if (check) {
      const drift = await checkVersion(rootDir, version);
      for (const d of drift) console.error(`set-version: ${d.file} is at ${d.current}, want ${version}`);
      if (drift.length) console.error(`Run \`bun run version:set ${version}\` and commit before tagging.`);
      return drift.length ? 1 : 0;
    }
    for (const file of await setVersion(rootDir, version)) console.log(`set-version: ${file} -> ${version}`);
    return 0;
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    return 1;
  }
}

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main(process.argv.slice(2), ROOT);
}
```

- [ ] **Step 4: Run the tests and a real `--check`**

Run: `bunx vitest run --root scripts set-version`

Expected: `6 passed`.

Run: `node scripts/set-version.mjs --check 1.0.0; echo exit=$?`

Expected (before the first release stamp): `set-version: apps/desktop/wails.json is at 0.1.0, want 1.0.0` and `exit=1` (`app.json` is already `1.0.0`). Do **not** stamp the tree now; the operator runs `bun run version:set 1.0.0` as the first step of the `v1.0.0` release (Task 18 procedure).

Run: `bunx biome check scripts`

Expected: clean.

- [ ] **Step 5: Commit**

```
git add scripts/set-version.mjs scripts/set-version.test.mjs
git commit -m "feat(release): set-version.mjs stamps app.json/wails.json from the tag, --check guards drift" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 13: Backend `httpapi.Version` variable and Dockerfile `ARG VERSION` (Track C)

**Files:**
- Modify: `backend/internal/adapter/in/httpapi/instance.go:5-6`
- Modify: `backend/internal/adapter/in/httpapi/instance_test.go` (two tests appended)
- Modify: `backend/Dockerfile:19-20`

**Interfaces:**
- Consumes: nothing.
- Produces: `var Version = "dev"` at `calendium/backend/internal/adapter/in/httpapi.Version` (the `-X` target for the Dockerfile, `test.yml` Task 5 and `docker-compose.yml` Task 16); Dockerfile build arg `VERSION` (default `dev`).

- [ ] **Step 1: Write the failing tests (append to `instance_test.go`)**

```go
// Version is a linker-stamped variable (backend/Dockerfile ARG VERSION):
// the discovery document must echo whatever the build set.
func TestHandleInstance_EchoesTheStampedVersion(t *testing.T) {
	prev := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = prev })

	h := New(Deps{
		Instance: InstanceInfo{Name: "Calendium", Mode: ModeCloud, Version: Version, AuthProviders: []string{"email"}},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/instance", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != "9.9.9" {
		t.Fatalf("version = %q, want 9.9.9", got.Version)
	}
}

func TestVersion_DefaultsToDevForSourceBuilds(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want \"dev\" (release images set it via -ldflags -X)", Version)
	}
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `cd backend && go test ./internal/adapter/in/httpapi/ -run 'TestHandleInstance_Echoes|TestVersion_'; cd ..`

Expected: compile error `cannot assign to Version (neither addressable nor a map index expression)` (it is a `const`).

- [ ] **Step 3: Make it a variable**

Replace `instance.go:5-6` with:

```go
// Version is the running API build, surfaced to clients via GET /v1/instance.
// Release images stamp it at link time:
//
//	go build -ldflags "-X calendium/backend/internal/adapter/in/httpapi.Version=X.Y.Z"
//
// (backend/Dockerfile ARG VERSION, passed by docker-compose.yml). Source
// builds and `go run` report "dev".
var Version = "dev"
```

- [ ] **Step 4: Stamp from the Dockerfile**

Replace `backend/Dockerfile:19-20` with:

```dockerfile
# Version baked into both binaries (GET /v1/instance "version"); docker-compose
# passes VERSION=${VERSION:-dev}, release pipelines pass the tag.
ARG VERSION=dev
RUN go build -ldflags="-s -w -X calendium/backend/internal/adapter/in/httpapi.Version=${VERSION}" -o /out/api ./cmd/api \
 && go build -ldflags="-s -w -X calendium/backend/internal/adapter/in/httpapi.Version=${VERSION}" -o /out/worker ./cmd/worker
```

- [ ] **Step 5: Run the tests and prove the ldflags path**

Run: `cd backend && go build ./... && go vet ./... && go test ./internal/adapter/in/httpapi/ && go build -ldflags "-X calendium/backend/internal/adapter/in/httpapi.Version=9.9.9" -o /tmp/calendium-api-ldflags ./cmd/api && go version -m /tmp/calendium-api-ldflags | grep -c ldflags; cd ..`

Expected: tests PASS; the last line prints `1` (the `-ldflags` setting is recorded in the binary's build info, proving the flag reached the link step). Do **not** run `docker build` (live stack in use).

- [ ] **Step 6: Commit**

```
git add backend/internal/adapter/in/httpapi/instance.go backend/internal/adapter/in/httpapi/instance_test.go backend/Dockerfile
git commit -m "feat(api): httpapi.Version becomes a ldflags-stamped variable; Dockerfile ARG VERSION" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 14: `release.yml` — dispatch inputs, `preflight`, stamped/NSIS/Linux builds, assertions (Track C)

**Files:**
- Modify: `.github/workflows/release.yml` (whole file rewritten; the `test` job and macOS signing steps are kept verbatim)

**Interfaces:**
- Consumes: `node scripts/set-version.mjs` (Task 12); `main.version` (Task 6); `apps/desktop/build/linux/calendium.desktop`, `README.txt` (Task 9); `apps/web/public/icon-512.png` (existing, same art as `icon.svg`) as the Linux `calendium.png`; stock Wails NSIS output `build/bin/Calendium-amd64-installer.exe` (`project.nsi:74`).
- Produces: artifacts `calendium_<build_version>_darwin_universal.dmg`, `calendium_<build_version>_windows_amd64-setup.exe`, `calendium_<build_version>_windows_amd64.zip`, `calendium_<build_version>_linux_amd64.tar.gz`; job outputs `preflight.outputs.version` / `build_version`.

- [ ] **Step 1: Write the new workflow**

Replace `.github/workflows/release.yml` with the following (the `test` job is today's text, repeated verbatim; everything after it is new).

```yaml
name: Release
on:
  push:
    tags: ["v*"]
  # Dry run: builds exactly like a tag release but uploads artifacts to the
  # run instead of publishing a GitHub Release. Signing secrets are still
  # required unless allow_unsigned is set; a TAG can never publish unsigned.
  workflow_dispatch:
    inputs:
      allow_unsigned:
        description: "Build without macOS signing secrets (dry runs only; tags always require them)"
        type: boolean
        default: false
      version:
        description: "Version to stamp, X.Y.Z (empty = 0.0.0, build_version 0.0.0-dev.<sha7>)"
        type: string
        default: ""

permissions:
  contents: write

jobs:
  # Gate the release on the desktop-facing suites. Full-repo quality is
  # already gated by test.yml on every push to main; this re-checks the
  # pieces a desktop binary embeds before any signing happens.
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
        with: { bun-version: latest }
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
          cache-dependency-path: apps/desktop/go.sum
      - name: Install Linux system deps
        # The desktop Go module cgo-compiles hotkey (X11) + systray
        # (appindicator) + wails (gtk/webkit) even for vet/test.
        run: sudo apt-get update && sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev libx11-dev libayatana-appindicator3-dev xvfb
      - run: bun install --frozen-lockfile
      - name: Shared package tests
        run: bun run test:shared
      - name: Desktop frontend tests
        run: bun run test:desktop
      - name: Desktop frontend build (main.go embeds frontend/dist)
        run: cd apps/desktop/frontend && bun run build
      - name: Desktop Go vet + test
        # hotkey's X11 init panics without a display; xvfb provides one on
        # the headless runner (vet doesn't execute code, test does).
        run: cd apps/desktop && go vet ./... && xvfb-run -a go test ./...

  # Single place that decides what version ships and whether shipping is
  # allowed at all. build-desktop never runs if this fails.
  preflight:
    needs: test
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.version.outputs.version }}
      build_version: ${{ steps.version.outputs.build_version }}
    env:
      # secrets are not readable in `if:`; surface presence here.
      HAS_MACOS_SIGNING: ${{ secrets.MACOS_CERT_P12 != '' }}
      ALLOW_UNSIGNED: ${{ github.event_name == 'workflow_dispatch' && inputs.allow_unsigned == true }}
      INPUT_VERSION: ${{ inputs.version }}
    steps:
      - uses: actions/checkout@v4
      - name: Compute version
        id: version
        shell: bash
        # tag vX.Y.Z -> version X.Y.Z, build_version X.Y.Z
        # dispatch     -> version <input or 0.0.0>, build_version <input or 0.0.0-dev.<sha7>>
        run: |
          set -euo pipefail
          SHA7="${GITHUB_SHA::7}"
          if [[ "$GITHUB_REF" == refs/tags/* ]]; then
            VERSION="${GITHUB_REF_NAME#v}"
            BUILD_VERSION="$VERSION"
          elif [[ -n "$INPUT_VERSION" ]]; then
            VERSION="$INPUT_VERSION"
            BUILD_VERSION="$VERSION"
          else
            VERSION="0.0.0"
            BUILD_VERSION="0.0.0-dev.${SHA7}"
          fi
          if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "::error::version '$VERSION' is not X.Y.Z (tag must be vX.Y.Z; dispatch input must be X.Y.Z or empty)"
            exit 1
          fi
          echo "version=$VERSION" >> "$GITHUB_OUTPUT"
          echo "build_version=$BUILD_VERSION" >> "$GITHUB_OUTPUT"
          echo "version=$VERSION build_version=$BUILD_VERSION"
      - name: Tag matches the committed versions (app.json, wails.json)
        if: startsWith(github.ref, 'refs/tags/')
        run: node scripts/set-version.mjs --check "${{ steps.version.outputs.version }}"
      - name: Signing secrets present, or explicitly waived for a dry run
        shell: bash
        run: |
          if [[ "$HAS_MACOS_SIGNING" == "true" ]]; then
            echo "macOS signing secrets present"
            exit 0
          fi
          if [[ "$ALLOW_UNSIGNED" == "true" ]]; then
            echo "::warning::MACOS_CERT_P12 is empty; building UNSIGNED because allow_unsigned=true (dry run only, never published)"
            exit 0
          fi
          echo "::error::MACOS_CERT_P12 is not set. A tag never publishes an unsigned macOS build. Add the five notarization secrets (MACOS_CERT_P12, MACOS_CERT_PASSWORD, APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD; see docs/release/store-readiness.md) or, for a dry run only, dispatch this workflow with allow_unsigned=true."
          exit 1

  build-desktop:
    needs: preflight
    strategy:
      fail-fast: false
      matrix:
        include:
          - os: macos-latest
            platform: darwin
            arch: universal
          - os: windows-latest
            platform: windows
            arch: amd64
          - os: ubuntu-latest
            platform: linux
            arch: amd64
    runs-on: ${{ matrix.os }}
    env:
      HAS_MACOS_SIGNING: ${{ secrets.MACOS_CERT_P12 != '' }}
      VERSION: ${{ needs.preflight.outputs.version }}
      BUILD_VERSION: ${{ needs.preflight.outputs.build_version }}
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
        with: { bun-version: latest }
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
          cache-dependency-path: apps/desktop/go.sum
      - name: Install Linux system deps
        if: matrix.platform == 'linux'
        # golang.design/x/hotkey (global hotkey, M2.6) needs X11 headers;
        # energye/systray needs ayatana-appindicator.
        run: sudo apt-get update && sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev libx11-dev libayatana-appindicator3-dev xvfb
      - name: Ensure NSIS (Windows)
        if: matrix.platform == 'windows'
        shell: pwsh
        # windows-latest ships makensis; the guard covers a future image change.
        run: if (-not (Get-Command makensis -ErrorAction SilentlyContinue)) { choco install nsis -y }
      - name: Install Wails CLI
        # Pinned to the wails/v2 library version in apps/desktop/go.mod
        # (reproducible builds; avoids silent CLI/library drift).
        run: go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
      - run: bun install --frozen-lockfile
      - name: Stamp version into app.json + wails.json (uncommitted; templated into Info.plist / NSIS)
        run: node scripts/set-version.mjs "$VERSION"
        shell: bash
      - name: Build desktop app
        # ubuntu-latest (24.04) ships webkit2gtk-4.1 only; wails needs the
        # webkit2_41 tag to link against it.
        # xvfb on linux: wails' binding generation executes the app briefly,
        # and hotkey's X11 init panics without a display (same as the tests).
        # -nsis on windows builds build/bin/Calendium-amd64-installer.exe with
        # the stock template, which registers the calendium:// protocol.
        run: cd apps/desktop && ${{ matrix.platform == 'linux' && 'xvfb-run -a' || '' }} wails build -platform ${{ matrix.platform }}/${{ matrix.arch }} ${{ matrix.platform == 'linux' && '-tags webkit2_41' || '' }} ${{ matrix.platform == 'windows' && '-nsis' || '' }} -ldflags "-X main.version=${{ needs.preflight.outputs.build_version }}"
      - name: Assert bundle version and identifier (macOS)
        if: matrix.platform == 'darwin'
        run: |
          PLIST=apps/desktop/build/bin/Calendium.app/Contents/Info.plist
          GOT="$(plutil -extract CFBundleShortVersionString raw -o - "$PLIST")"
          test "$GOT" = "$VERSION" || { echo "::error::CFBundleShortVersionString is '$GOT', want '$VERSION'"; exit 1; }
          ID="$(plutil -extract CFBundleIdentifier raw -o - "$PLIST")"
          test "$ID" = "app.calendium.desktop" || { echo "::error::CFBundleIdentifier is '$ID'"; exit 1; }
      # Signing/notarization run whenever the secrets exist; preflight already
      # refused a tag without them, so a dispatch with allow_unsigned=true is
      # the only way to reach here unsigned.
      - name: Import signing certificate (macOS)
        if: matrix.platform == 'darwin' && env.HAS_MACOS_SIGNING == 'true'
        env:
          MACOS_CERT_P12: ${{ secrets.MACOS_CERT_P12 }}
          MACOS_CERT_PASSWORD: ${{ secrets.MACOS_CERT_PASSWORD }}
        run: |
          KEYCHAIN="$RUNNER_TEMP/signing.keychain-db"
          KEYCHAIN_PASSWORD="$(openssl rand -base64 24)"
          echo "$MACOS_CERT_P12" | base64 --decode > "$RUNNER_TEMP/cert.p12"
          security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          security set-keychain-settings -lut 21600 "$KEYCHAIN"
          security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          security import "$RUNNER_TEMP/cert.p12" -k "$KEYCHAIN" -P "$MACOS_CERT_PASSWORD" -T /usr/bin/codesign
          security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
          security list-keychains -d user -s "$KEYCHAIN" login.keychain
          rm "$RUNNER_TEMP/cert.p12"
          # fail here, not at codesign, if the p12 lacks a Developer ID identity.
          security find-identity -v -p codesigning "$KEYCHAIN" | grep "Developer ID Application"
      - name: Sign app (macOS)
        if: matrix.platform == 'darwin' && env.HAS_MACOS_SIGNING == 'true'
        run: |
          codesign --force --deep --options runtime --timestamp \
            --sign "Developer ID Application" apps/desktop/build/bin/Calendium.app
          codesign --verify --deep --strict --verbose=2 apps/desktop/build/bin/Calendium.app
      - name: Package artifact (macOS .dmg)
        if: matrix.platform == 'darwin'
        run: |
          cd apps/desktop
          npx --yes appdmg build/darwin/dmg/appdmg.json \
            "build/bin/calendium_${BUILD_VERSION}_darwin_${{ matrix.arch }}.dmg"
      - name: Sign, notarize, and staple dmg (macOS)
        if: matrix.platform == 'darwin' && env.HAS_MACOS_SIGNING == 'true'
        env:
          APPLE_ID: ${{ secrets.APPLE_ID }}
          APPLE_TEAM_ID: ${{ secrets.APPLE_TEAM_ID }}
          APPLE_APP_PASSWORD: ${{ secrets.APPLE_APP_PASSWORD }}
        run: |
          DMG=(apps/desktop/build/bin/*.dmg)
          codesign --force --timestamp --sign "Developer ID Application" "${DMG[0]}"
          xcrun notarytool submit "${DMG[0]}" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
            --password "$APPLE_APP_PASSWORD" --wait
          # staple fails if notarization did not fully succeed, failing the job.
          xcrun stapler staple "${DMG[0]}"
          xcrun stapler validate "${DMG[0]}"
      - name: Package artifacts (Windows installer + portable zip) and assert VersionInfo
        if: matrix.platform == 'windows'
        shell: pwsh
        run: |
          $ErrorActionPreference = 'Stop'
          cd apps/desktop/build/bin
          $setup = "calendium_${env:BUILD_VERSION}_windows_${{ matrix.arch }}-setup.exe"
          Move-Item "Calendium-${{ matrix.arch }}-installer.exe" $setup
          $pv = (Get-Item $setup).VersionInfo.ProductVersion
          if (-not $pv.StartsWith($env:VERSION)) { Write-Error "installer ProductVersion '$pv' does not start with '$env:VERSION'"; exit 1 }
          Compress-Archive -Path Calendium.exe -DestinationPath "calendium_${env:BUILD_VERSION}_windows_${{ matrix.arch }}.zip"
          Get-ChildItem
      - name: Package artifact (Linux .tar.gz with desktop entry, icon, README)
        if: matrix.platform == 'linux'
        run: |
          set -euo pipefail
          cp apps/desktop/build/linux/calendium.desktop apps/desktop/build/linux/README.txt apps/desktop/build/bin/
          cp apps/web/public/icon-512.png apps/desktop/build/bin/calendium.png
          cd apps/desktop/build/bin
          tar -czf "calendium_${BUILD_VERSION}_linux_${{ matrix.arch }}.tar.gz" Calendium calendium.desktop calendium.png README.txt
          tar -tzf "calendium_${BUILD_VERSION}_linux_${{ matrix.arch }}.tar.gz"
      # Dry runs (workflow_dispatch) upload to the run; tag pushes publish.
      - name: Upload artifacts (dry run)
        if: "!startsWith(github.ref, 'refs/tags/')"
        uses: actions/upload-artifact@v4
        with:
          name: calendium-${{ matrix.platform }}-${{ matrix.arch }}
          path: |
            apps/desktop/build/bin/*.dmg
            apps/desktop/build/bin/*-setup.exe
            apps/desktop/build/bin/*.zip
            apps/desktop/build/bin/*.tar.gz
      - name: Publish GitHub Release
        if: startsWith(github.ref, 'refs/tags/')
        uses: softprops/action-gh-release@v2
        with:
          generate_release_notes: true
          files: |
            apps/desktop/build/bin/*.dmg
            apps/desktop/build/bin/*-setup.exe
            apps/desktop/build/bin/*.zip
            apps/desktop/build/bin/*.tar.gz
```

- [ ] **Step 2: Validate the YAML and the version logic locally**

Run: `ruby -ryaml -e 'y = YAML.load_file(".github/workflows/release.yml"); puts y["jobs"].keys.join(","); puts y["jobs"]["build-desktop"]["needs"]; puts y["on"]["workflow_dispatch"]["inputs"].keys.join(",")'`

Expected:
```
test,preflight,build-desktop
preflight
allow_unsigned,version
```

Run the `Compute version` snippet for the three shapes (copy the `run:` body into a file `/private/tmp/claude-501/-Users-guilherme-Dev-pessoal-calendium/701a3919-b211-4073-85be-204be1f64f97/scratchpad/compute.sh`, then):

```
S=/private/tmp/claude-501/-Users-guilherme-Dev-pessoal-calendium/701a3919-b211-4073-85be-204be1f64f97/scratchpad
GITHUB_REF=refs/tags/v1.2.3 GITHUB_REF_NAME=v1.2.3 GITHUB_SHA=abcdef0123 INPUT_VERSION= GITHUB_OUTPUT=$S/o1 bash $S/compute.sh && cat $S/o1
GITHUB_REF=refs/heads/main GITHUB_REF_NAME=main GITHUB_SHA=abcdef0123 INPUT_VERSION= GITHUB_OUTPUT=$S/o2 bash $S/compute.sh && cat $S/o2
GITHUB_REF=refs/heads/main GITHUB_REF_NAME=main GITHUB_SHA=abcdef0123 INPUT_VERSION=1.2 GITHUB_OUTPUT=$S/o3 bash $S/compute.sh; echo exit=$?
```

Expected: `version=1.2.3` / `build_version=1.2.3`; `version=0.0.0` / `build_version=0.0.0-dev.abcdef0`; `::error::version '1.2' is not X.Y.Z …` with `exit=1`.

Run (if installed): `which actionlint && actionlint .github/workflows/release.yml`

Expected: no findings (or skip when absent).

- [ ] **Step 3: Commit**

```
git add .github/workflows/release.yml
git commit -m "ci(release): preflight version/signing gate, stamped builds, NSIS installer, Linux desktop entry, release notes" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 15: Web identity — `env.supportEmail`, real repo URL, dynamic copyright (Track D)

**Files:**
- Modify: `apps/web/lib/env.ts` (add `supportEmail` getter)
- Modify: `apps/web/lib/env.test.ts` (append)
- Modify: `apps/web/components/marketing/links.ts:13,17`
- Create: `apps/web/components/marketing/links.test.ts`
- Modify: `apps/web/components/marketing/site-footer.tsx:64` (+ exported `copyrightLine`)
- Create: `apps/web/components/marketing/site-footer.test.ts`
- Modify: `apps/web/Dockerfile` (build arg), `docker-compose.yml` (web `args`/`environment`), `.env.example` (web section)

**Interfaces:**
- Consumes: nothing.
- Produces: `env.supportEmail: string` (default `support@calendium.app`); `SUPPORT_EMAIL`, `GITHUB_URL = 'https://github.com/GuilhermeVozniak/calendium'`; `export function copyrightLine(year?: number): string`; build arg / env `NEXT_PUBLIC_SUPPORT_EMAIL`.

- [ ] **Step 1: Write the failing tests**

Append to `apps/web/lib/env.test.ts`:

```ts
describe('env.supportEmail', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('defaults to the Calendium Cloud address when unset', () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', undefined);
    expect(env.supportEmail).toBe('support@calendium.app');
  });

  it('returns the configured address', () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', 'help@example.org');
    expect(env.supportEmail).toBe('help@example.org');
  });

  // Review Focus 4: a blank value in .env must never render "mailto: ".
  it('falls back on blank or whitespace and trims padding', () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', '');
    expect(env.supportEmail).toBe('support@calendium.app');
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', '   ');
    expect(env.supportEmail).toBe('support@calendium.app');
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', '  help@example.org ');
    expect(env.supportEmail).toBe('help@example.org');
  });
});
```

Create `apps/web/components/marketing/links.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';

// links.ts evaluates SUPPORT_EMAIL at import time (Next inlines the
// NEXT_PUBLIC_* read at build), so each case re-imports a fresh module.
describe('marketing links', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it('points at the real public repository', async () => {
    const { GITHUB_URL } = await import('@/components/marketing/links');
    expect(GITHUB_URL).toBe('https://github.com/GuilhermeVozniak/calendium');
  });

  it('SUPPORT_EMAIL defaults to support@calendium.app', async () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', undefined);
    vi.resetModules();
    const { SUPPORT_EMAIL } = await import('@/components/marketing/links');
    expect(SUPPORT_EMAIL).toBe('support@calendium.app');
  });

  it('SUPPORT_EMAIL follows NEXT_PUBLIC_SUPPORT_EMAIL', async () => {
    vi.stubEnv('NEXT_PUBLIC_SUPPORT_EMAIL', 'help@example.org');
    vi.resetModules();
    const { SUPPORT_EMAIL } = await import('@/components/marketing/links');
    expect(SUPPORT_EMAIL).toBe('help@example.org');
  });
});
```

Create `apps/web/components/marketing/site-footer.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { copyrightLine } from '@/components/marketing/site-footer';

describe('copyrightLine', () => {
  it('renders © <year> Calendium with no legal suffix', () => {
    expect(copyrightLine(2026)).toBe('© 2026 Calendium');
    expect(copyrightLine(2031)).toBe('© 2031 Calendium');
  });

  it('defaults to the current year so the footer never goes stale', () => {
    expect(copyrightLine()).toBe(`© ${new Date().getFullYear()} Calendium`);
  });
});
```

- [ ] **Step 2: Run and watch them fail**

Run: `cd apps/web && bunx vitest run lib/env.test.ts components/marketing; cd ../..`

Expected: FAIL — `env.supportEmail` undefined; `GITHUB_URL` is `https://github.com/calendium/calendium`; `copyrightLine` is not exported.

- [ ] **Step 3: Implement**

`apps/web/lib/env.ts` — add inside the `env` object after `apiUrl`:

```ts
  get supportEmail(): string {
    // Contact address on the footer, pricing, privacy and terms pages.
    // Self-hosted / white-label deployments set NEXT_PUBLIC_SUPPORT_EMAIL at
    // build time; blank falls back to the Calendium Cloud mailbox so the
    // mailto: links never render empty.
    const raw = process.env.NEXT_PUBLIC_SUPPORT_EMAIL?.trim();
    return raw ? raw : 'support@calendium.app';
  },
```

`apps/web/components/marketing/links.ts` — add `import { env } from '@/lib/env';` at the top and replace lines 13 and 17:

```ts
/** Operator contact (NEXT_PUBLIC_SUPPORT_EMAIL; defaults to Calendium Cloud's). */
export const SUPPORT_EMAIL = env.supportEmail;
```
```ts
export const GITHUB_URL = 'https://github.com/GuilhermeVozniak/calendium';
```

`apps/web/components/marketing/site-footer.tsx` — add above `export function SiteFooter()`:

```ts
/** "© <year> Calendium" — computed at render so it is never stale. */
export function copyrightLine(year: number = new Date().getFullYear()): string {
  return `© ${year} Calendium`;
}
```

and replace line 64:

```tsx
          <p className="text-xs text-muted-foreground">{copyrightLine()}</p>
```

- [ ] **Step 4: Carry the build arg through Docker, compose and `.env.example`**

`apps/web/Dockerfile` — after `ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL`:

```dockerfile
# Support address on the marketing/legal pages (blank = support@calendium.app).
ARG NEXT_PUBLIC_SUPPORT_EMAIL
ENV NEXT_PUBLIC_SUPPORT_EMAIL=$NEXT_PUBLIC_SUPPORT_EMAIL
```

`docker-compose.yml` `web` service — `build.args` becomes:

```yaml
      args:
        NEXT_PUBLIC_API_URL: ${NEXT_PUBLIC_API_URL:-}
        NEXT_PUBLIC_SUPPORT_EMAIL: ${NEXT_PUBLIC_SUPPORT_EMAIL:-}
```

and `environment` gains, after `NEXT_PUBLIC_API_URL: ${NEXT_PUBLIC_API_URL:-}`:

```yaml
      NEXT_PUBLIC_SUPPORT_EMAIL: ${NEXT_PUBLIC_SUPPORT_EMAIL:-}
```

`.env.example` — after the `NEXT_PUBLIC_API_URL=http://localhost:8080` line in the Web app section:

```
# Contact address shown on the marketing footer, pricing, privacy and terms
# pages. Build-time (inlined into the web bundle); blank = support@calendium.app.
NEXT_PUBLIC_SUPPORT_EMAIL=
```

- [ ] **Step 5: Run the web suite, typecheck, lint, compose YAML parse**

Run: `bun run test:web && bun run --cwd apps/web typecheck && bunx biome check apps/web && ruby -ryaml -e 'y = YAML.load_file("docker-compose.yml"); puts y["services"]["web"]["build"]["args"].keys.join(",")'`

Expected: PASS, typecheck clean, lint clean, `NEXT_PUBLIC_API_URL,NEXT_PUBLIC_SUPPORT_EMAIL`. (No `docker compose` invocation — a live stack is in use.)

- [ ] **Step 6: Commit**

```
git add apps/web/lib/env.ts apps/web/lib/env.test.ts apps/web/components/marketing/links.ts apps/web/components/marketing/links.test.ts apps/web/components/marketing/site-footer.tsx apps/web/components/marketing/site-footer.test.ts apps/web/Dockerfile docker-compose.yml .env.example
git commit -m "feat(web): real GitHub URL, NEXT_PUBLIC_SUPPORT_EMAIL-driven contact, dynamic copyright year" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 16: APNs default topic, `VERSION` through compose, backend docs (Track D)

**Files:**
- Modify: `backend/internal/adapter/out/push/apns.go:24-25`
- Modify: `backend/internal/adapter/out/push/apns_test.go` (append)
- Modify: `docker-compose.yml` (`api` and `worker` `build.args`), `.env.example` (APNs comment + `VERSION`)
- Modify: `docs/self-hosting/configuration.md:112,209`, `docs/self-hosting/providers.md` (§5a env block), `docs/self-hosting/upgrades.md:12`

**Interfaces:**
- Consumes: Dockerfile `ARG VERSION` (Task 13 — compose passing an arg the Dockerfile does not yet declare only warns).
- Produces: `defaultAPNsTopic = "app.calendium.mobile"`; compose build arg `VERSION: ${VERSION:-dev}` for `api` and `worker`.

- [ ] **Step 1: Write the failing tests (append to `apns_test.go`)**

```go
// The default topic must equal the shipped iOS bundle id (apps/mobile/app.json
// ios.bundleIdentifier); APNs rejects pushes whose topic does not match.
func TestDefaultAPNsTopic_IsTheMobileBundleID(t *testing.T) {
	if defaultAPNsTopic != "app.calendium.mobile" {
		t.Fatalf("defaultAPNsTopic = %q, want app.calendium.mobile", defaultAPNsTopic)
	}
	t.Setenv("APNS_TOPIC", "")
	if s := newAPNsSender(config.APNs{}, nil); s.topic != "app.calendium.mobile" {
		t.Fatalf("topic = %q, want the default", s.topic)
	}
}

func TestAPNsTopic_EnvOverrideWins(t *testing.T) {
	t.Setenv("APNS_TOPIC", "com.example.custom")
	if s := newAPNsSender(config.APNs{}, nil); s.topic != "com.example.custom" {
		t.Fatalf("topic = %q, want com.example.custom", s.topic)
	}
}
```

(`config` is already imported by `apns.go`; add `"calendium/backend/internal/config"` to the test file's imports if it is not there yet.)

- [ ] **Step 2: Run and watch them fail**

Run: `cd backend && go test ./internal/adapter/out/push/ -run 'TestDefaultAPNsTopic|TestAPNsTopic_'; cd ..`

Expected: FAIL — `defaultAPNsTopic = "app.calendium", want app.calendium.mobile`.

- [ ] **Step 3: Fix the constant**

`apns.go:24-25`:

```go
	// defaultAPNsTopic is the iOS app bundle id (apps/mobile/app.json
	// ios.bundleIdentifier); override with APNS_TOPIC for custom builds.
	defaultAPNsTopic = "app.calendium.mobile"
```

- [ ] **Step 4: Compose and `.env.example`**

`docker-compose.yml` — for both `api` and `worker`, extend `build`:

```yaml
    build:
      context: .
      dockerfile: backend/Dockerfile
      args:
        VERSION: ${VERSION:-dev}
```

`.env.example` — replace the APNs topic comment (lines 111-112):

```
# APNs topic = your iOS app's bundle id (default app.calendium.mobile, the
# bundleIdentifier in apps/mobile/app.json). Set it only if you ship a build
# under another bundle id, or APNs rejects pushes.
APNS_TOPIC=
```

and add a new section before `# ─── Host port mappings`:

```
# ─── Image version stamp (docker compose build) ────────────────────────────────
# Baked into the api/worker binaries and reported as `version` by
# GET /v1/instance. Release builds pass the tag (e.g. 1.0.0); blank = dev.
VERSION=
```

- [ ] **Step 5: Docs**

`docs/self-hosting/configuration.md:112` — the `APNS_TOPIC` row: default `` `app.calendium` `` → `` `app.calendium.mobile` `` and description “APNs topic — must equal your iOS app's **bundle id** (`ios.bundleIdentifier` in `apps/mobile/app.json`). Change it only for a build under another bundle id (see [Clients → build from source](./clients.md)), or APNs rejects the pushes.”

`docs/self-hosting/configuration.md:209` — `| \`version\` | The running API build constant (currently \`0.1.0\`). |` → `| \`version\` | The running API build: \`dev\` for source builds, the release tag (e.g. \`1.0.0\`) for images built with \`VERSION\` (\`docker compose build\` passes \`${VERSION:-dev}\`). |`

`docs/self-hosting/upgrades.md:12` — `(\`httpapi.Version\`, currently \`0.1.0\`)` → `(\`httpapi.Version\`: \`dev\` for source builds, the tag for release images)`.

`docs/self-hosting/providers.md` §5a — extend the `.env` block:

```bash
   APNS_KEY_ID=XXXXXXXXXX
   APNS_TEAM_ID=YYYYYYYYYY
   # PEM *contents* of the .p8 with newlines escaped as \n — NOT a file path:
   APNS_KEY_P8=-----BEGIN PRIVATE KEY-----\nMIGT...\n-----END PRIVATE KEY-----\n
   # Bundle id of the iOS build you distribute; defaults to app.calendium.mobile
   # (apps/mobile/app.json). Only set it for a build under another bundle id.
   APNS_TOPIC=app.calendium.mobile
```

- [ ] **Step 6: Run the backend push tests and validate compose YAML**

Run: `cd backend && go vet ./internal/adapter/out/push/ && go test ./internal/adapter/out/push/; cd .. && ruby -ryaml -e 'y = YAML.load_file("docker-compose.yml"); puts y["services"]["api"]["build"]["args"]["VERSION"]; puts y["services"]["worker"]["build"]["args"]["VERSION"]' && grep -n "APNS_TOPIC\|^VERSION=" .env.example`

Expected: tests PASS (the existing `apns-topic` header assertion compares against the constant and keeps passing); two lines `${VERSION:-dev}`; the grep shows the new comment and `VERSION=`.

- [ ] **Step 7: Commit**

```
git add backend/internal/adapter/out/push/apns.go backend/internal/adapter/out/push/apns_test.go docker-compose.yml .env.example docs/self-hosting/configuration.md docs/self-hosting/providers.md docs/self-hosting/upgrades.md
git commit -m "fix(push): APNs default topic app.calendium.mobile; VERSION build arg through compose; docs" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 17: Env-driven cloud/demo hosts and `webUrl` consumption in the mobile and desktop clients (Track D)

**Files:**
- Modify: `apps/mobile/lib/server-config.ts` (`CLOUD_PRESET`, `DEMO_CONFIG`, `ServerConfig.webUrl`, `discoverServer`, new `envUrl`)
- Modify: `apps/mobile/lib/server-config.test.ts` (append)
- Modify: `apps/mobile/.env.example`
- Modify: `apps/desktop/frontend/src/lib/server-config.ts` (`CLOUD_PRESET`, `ServerConfig.webUrl`, `webOrigin`, `discoverServer`, new `envUrl`)
- Modify: `apps/desktop/frontend/src/lib/server-config.test.ts` (append)
- Modify: `apps/desktop/frontend/.env.example`
- Modify: `docs/self-hosting/clients.md` (discovery bullet, Cloud preset notes, `EXPO_PUBLIC_*` list, `--profile preview`)

**Interfaces:**
- Consumes: `InstanceInfo.webUrl?: string` from `packages/shared/src/types.ts` (piece 1 — consume only). `normalizeServerUrl` (existing in both files).
- Produces: `export function envUrl(value: string | undefined, fallback: string): string` (both clients); `ServerConfig.webUrl?: string`; desktop `webOrigin()` prefers `webUrl`; env vars `EXPO_PUBLIC_CLOUD_API_URL`, `EXPO_PUBLIC_DEMO_SERVER_URL`, `VITE_CLOUD_API_URL`.

- [ ] **Step 1: Confirm piece 1 is present**

Run: `grep -n "webUrl" packages/shared/src/types.ts backend/internal/adapter/in/httpapi/instance.go`

Expected: a `webUrl?: string;` member inside `InstanceInfo` (and the Go `WebURL` field). If the grep is empty, STOP: rebase onto the branch that contains piece 1 before continuing; do not add the field here.

- [ ] **Step 2: Write the failing mobile tests (append to `apps/mobile/lib/server-config.test.ts`)**

```ts
// Extend the existing `import { … } from './server-config'` at the top of the
// file with: CLOUD_PRESET, DEMO_CONFIG, envUrl

describe('cloud preset and demo hosts', () => {
  it('CLOUD_PRESET defaults to Calendium Cloud when EXPO_PUBLIC_CLOUD_API_URL is unset', () => {
    expect(CLOUD_PRESET.serverUrl).toBe('https://api.calendium.app');
  });

  it('DEMO_CONFIG defaults to demo.calendium.app and carries a webUrl', () => {
    expect(DEMO_CONFIG.serverUrl).toBe('https://demo.calendium.app');
    expect(DEMO_CONFIG.authBaseUrl).toBe('https://demo.calendium.app/api/auth');
    expect(DEMO_CONFIG.webUrl).toBe('https://demo.calendium.app');
    expect(DEMO_CONFIG.demoMode).toBe(true);
  });

  it('envUrl normalizes an override (trailing slash, missing scheme) and falls back on blank', () => {
    expect(envUrl('https://cloud.example.com/', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('cloud.example.com', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('   ', 'https://api.calendium.app')).toBe('https://api.calendium.app');
    expect(envUrl(undefined, 'https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('honours EXPO_PUBLIC_CLOUD_API_URL at module load', () => {
    // babel-preset-expo's inline-env-vars plugin only runs for production
    // bundles (api.caller(getIsProd)); under jest the module reads the live
    // process.env, so isolate a fresh load with the override set.
    const prev = process.env.EXPO_PUBLIC_CLOUD_API_URL;
    process.env.EXPO_PUBLIC_CLOUD_API_URL = 'https://cloud.example.com/';
    try {
      jest.isolateModules(() => {
        const fresh = require('./server-config') as typeof import('./server-config');
        expect(fresh.CLOUD_PRESET.serverUrl).toBe('https://cloud.example.com');
      });
    } finally {
      if (prev === undefined) delete process.env.EXPO_PUBLIC_CLOUD_API_URL;
      else process.env.EXPO_PUBLIC_CLOUD_API_URL = prev;
    }
  });
});

describe('discoverServer webUrl', () => {
  it('carries webUrl when the instance advertises it (piece 1) and omits it otherwise', async () => {
    mockFetchInstance.mockResolvedValue({
      name: 'My Calendium',
      mode: 'self_host',
      version: '1.0.0',
      authBaseUrl: 'https://example.com/api/auth',
      authProviders: ['email'],
      undoSendSeconds: 15,
      features: { billing: false, google: true, microsoft: false, ai: false, push: false },
      webUrl: 'https://mail.example.com',
    });
    const withWeb = await discoverServer('example.com');
    expect(withWeb.webUrl).toBe('https://mail.example.com');

    mockFetchInstance.mockResolvedValue({
      name: 'Old',
      mode: 'self_host',
      version: '0.1.0',
      authBaseUrl: 'https://old.example.com/api/auth',
      authProviders: ['email'],
      undoSendSeconds: 15,
      features: { billing: false, google: false, microsoft: false, ai: false, push: false },
    });
    const legacy = await discoverServer('old.example.com');
    expect('webUrl' in legacy).toBe(false);
  });
});
```

- [ ] **Step 3: Write the failing desktop tests (append to `apps/desktop/frontend/src/lib/server-config.test.ts`)**

```ts
// Add `envUrl` to the existing `import { … } from './server-config'` at the top.

describe('CLOUD_PRESET and envUrl', () => {
  it('defaults to Calendium Cloud', () => {
    expect(CLOUD_PRESET.serverUrl).toBe('https://api.calendium.app');
  });

  it('envUrl normalizes an override and falls back on blank', () => {
    expect(envUrl('https://cloud.example.com/', 'x')).toBe('https://cloud.example.com');
    expect(envUrl('   ', 'https://api.calendium.app')).toBe('https://api.calendium.app');
    expect(envUrl(undefined, 'https://api.calendium.app')).toBe('https://api.calendium.app');
  });

  it('honours VITE_CLOUD_API_URL at module load', async () => {
    vi.stubEnv('VITE_CLOUD_API_URL', 'https://cloud.example.com/');
    vi.resetModules();
    const fresh = await import('./server-config');
    expect(fresh.CLOUD_PRESET.serverUrl).toBe('https://cloud.example.com');
    vi.unstubAllEnvs();
    vi.resetModules();
  });
});

describe('webOrigin prefers the advertised webUrl', () => {
  const base = { ...DEMO_CONFIG, authBaseUrl: 'https://api.example.com/api/auth' };

  it('uses webUrl when present', () => {
    expect(webOrigin({ ...base, webUrl: 'https://mail.example.com/' })).toBe('https://mail.example.com');
  });

  it('falls back to the authBaseUrl origin when webUrl is absent or invalid', () => {
    expect(webOrigin(base)).toBe('https://api.example.com');
    expect(webOrigin({ ...base, webUrl: 'not a url' })).toBe('https://api.example.com');
  });
});

describe('discoverServer webUrl', () => {
  it('carries webUrl from the instance descriptor when present', async () => {
    fetchInstanceMock.mockResolvedValue({
      name: 'My Calendium',
      mode: 'self_host',
      version: '1.0.0',
      authBaseUrl: 'https://example.com/api/auth',
      authProviders: ['email'],
      undoSendSeconds: 15,
      features: { billing: false, google: true, microsoft: false, ai: false, push: false },
      webUrl: 'https://mail.example.com',
    } as InstanceInfo);
    const config = await discoverServer('example.com');
    expect(config.webUrl).toBe('https://mail.example.com');
  });
});
```

- [ ] **Step 4: Run and watch them fail**

Run: `cd apps/mobile && bunx jest lib/server-config; cd ../desktop/frontend && bunx vitest run src/lib/server-config.test.ts; cd ../../..`

Expected: FAIL — `envUrl` is not exported; `DEMO_CONFIG.webUrl` undefined; `webOrigin` returns the authBaseUrl origin even with `webUrl`.

- [ ] **Step 5: Implement the mobile side (`apps/mobile/lib/server-config.ts`)**

Replace the `CLOUD_PRESET` block (lines 20-23) with:

```ts
/**
 * Resolves a build-time EXPO_PUBLIC_* host override: blank/unset falls back,
 * anything else is normalized like a user-entered URL (scheme added,
 * trailing slash dropped).
 */
export function envUrl(value: string | undefined, fallback: string): string {
  const normalized = normalizeServerUrl(value ?? '');
  return normalized || fallback;
}

/**
 * Calendium Cloud — our managed, paid server (docs/payments.md). This is only
 * the bootstrap URL: after discovery the client follows `authBaseUrl` and
 * `webUrl` from /v1/instance. White-label hosts override it at build time.
 */
export const CLOUD_PRESET = {
  serverUrl: envUrl(process.env.EXPO_PUBLIC_CLOUD_API_URL, 'https://api.calendium.app'),
} as const;
```

In `ServerConfig`, after `authBaseUrl: string;`:

```ts
  /**
   * Public web origin of the server (billing, sign-in pages), from the
   * /v1/instance `webUrl` field when the server advertises it; absent on
   * older servers, where the authBaseUrl origin is the fallback.
   */
  webUrl?: string;
```

Replace the `DEMO_CONFIG` block with:

```ts
/** Demo label only — demo never dials out (lib/mock); configurable for white-label hosts. */
const DEMO_SERVER_URL = envUrl(process.env.EXPO_PUBLIC_DEMO_SERVER_URL, 'https://demo.calendium.app');

/**
 * Ready-made config for the offline "Try the demo" experience. It points at no
 * real server — every screen reads the mock data in lib/mock and auth is
 * short-circuited to a demo user. This is the ONLY place mock data is enabled.
 */
export const DEMO_CONFIG: ServerConfig = {
  serverUrl: DEMO_SERVER_URL,
  authBaseUrl: `${DEMO_SERVER_URL}/api/auth`,
  webUrl: DEMO_SERVER_URL,
  mode: 'cloud',
  name: 'Calendium Demo',
  authProviders: ['email', 'google', 'apple'],
  features: { billing: true, google: true, microsoft: true, ai: true, push: false },
  demoMode: true,
};
```

In `discoverServer`, after `authBaseUrl: info.authBaseUrl,`:

```ts
    ...(info.webUrl ? { webUrl: info.webUrl } : {}),
```

`apps/mobile/.env.example` — append:

```
# Calendium Cloud preset behind the Connect screen's Cloud button; white-label
# hosts override it at build time. Default https://api.calendium.app.
# EXPO_PUBLIC_CLOUD_API_URL=
# Label for the offline "Try the demo" config (never dialled). Default
# https://demo.calendium.app.
# EXPO_PUBLIC_DEMO_SERVER_URL=
```

- [ ] **Step 6: Implement the desktop side (`apps/desktop/frontend/src/lib/server-config.ts`)**

Replace the `CLOUD_PRESET` block (lines 26-29) with:

```ts
/** Resolves a build-time VITE_* host override: blank/unset falls back, else normalized. */
export function envUrl(value: string | undefined, fallback: string): string {
  const normalized = normalizeServerUrl(value ?? '');
  return normalized || fallback;
}

/**
 * Calendium Cloud — our managed, paid server (docs/payments.md). Bootstrap URL
 * only: after discovery the client follows `authBaseUrl` / `webUrl` from
 * /v1/instance. White-label hosts override it at build time.
 */
export const CLOUD_PRESET = {
  serverUrl: envUrl(import.meta.env.VITE_CLOUD_API_URL as string | undefined, 'https://api.calendium.app'),
} as const;
```

In `ServerConfig`, after `authBaseUrl: string;`:

```ts
  /** Public web origin from /v1/instance `webUrl` (piece 1); absent on older servers. */
  webUrl?: string;
```

Replace `webOrigin` with:

```ts
/**
 * Web origin (scheme://host) for billing / browser sign-in: the server's
 * advertised `webUrl` when present, else the Better Auth base URL's origin.
 */
export function webOrigin(config: ServerConfig | null): string | null {
  if (!config) return null;
  if (config.webUrl) {
    try {
      return new URL(config.webUrl).origin;
    } catch {
      // Fall through to the authBaseUrl origin.
    }
  }
  if (!config.authBaseUrl) return null;
  try {
    return new URL(config.authBaseUrl).origin;
  } catch {
    return null;
  }
}
```

In `discoverServer`, after `authBaseUrl: info.authBaseUrl,`:

```ts
    ...(info.webUrl ? { webUrl: info.webUrl } : {}),
```

`apps/desktop/frontend/.env.example` — append:

```
# Calendium Cloud preset behind the Connect screen's Cloud button; white-label
# hosts override it at build time. Default https://api.calendium.app.
# VITE_CLOUD_API_URL=
```

- [ ] **Step 7: Update `docs/self-hosting/clients.md`**

In “How discovery works”, add a third bullet after the feature-flags bullet:

```markdown
- the **public web URL** (`webUrl`, when the server advertises it) — where
  billing and browser sign-in pages live. Older servers omit it and the apps
  fall back to the origin of `authBaseUrl`.
```

Desktop section, replace `- **Calendium Cloud** — one button; uses the built-in preset\n  \`https://api.calendium.app\`.` with:

```markdown
- **Calendium Cloud** — one button; uses the built-in preset
  `https://api.calendium.app` (a build-time default: set `VITE_CLOUD_API_URL`
  when building a white-label desktop app).
```

Mobile section, replace `The mobile app has the same **Connect** screen: a **Calendium Cloud** button\n(preset \`https://api.calendium.app\`) and a **custom server** field.` with:

```markdown
The mobile app has the same **Connect** screen: a **Calendium Cloud** button
(preset `https://api.calendium.app`, overridable at build time with
`EXPO_PUBLIC_CLOUD_API_URL`) and a **custom server** field.
```

Replace `Defaults can be seeded for development via \`EXPO_PUBLIC_API_URL\` (the only\n\`EXPO_PUBLIC_*\` var the app reads) so a client exists before discovery finishes;` with:

```markdown
Defaults can be seeded for development via `EXPO_PUBLIC_API_URL` so a client
exists before discovery finishes (the other `EXPO_PUBLIC_*` vars the app reads
are `EXPO_PUBLIC_CLOUD_API_URL` and `EXPO_PUBLIC_DEMO_SERVER_URL`, both
white-label overrides with Calendium Cloud defaults);
```

Replace `# eas build --platform ios --profile internal` with `# eas build --platform ios --profile preview`.

- [ ] **Step 8: Run both client suites, typecheck, lint**

Run: `bun run test:mobile && bun run test:desktop && cd apps/desktop/frontend && bunx tsc --noEmit && cd ../../.. && bunx biome check apps/mobile apps/desktop/frontend`

Expected: PASS; `tsc` clean (`info.webUrl` typed by piece 1); lint clean.

- [ ] **Step 9: Commit**

```
git add apps/mobile/lib/server-config.ts apps/mobile/lib/server-config.test.ts apps/mobile/.env.example apps/desktop/frontend/src/lib/server-config.ts apps/desktop/frontend/src/lib/server-config.test.ts apps/desktop/frontend/.env.example docs/self-hosting/clients.md
git commit -m "feat(clients): env-driven Cloud/demo hosts; prefer the server's advertised webUrl" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 18: Operator doc `docs/release/store-readiness.md` and Apple key-path hygiene (Track D)

**Files:**
- Create: `docs/release/store-readiness.md`
- Modify: `apps/mobile/docs/apple/secret-gem.rb:3`, `apps/mobile/docs/apple/README.md:16` (relative key path)

**Interfaces:**
- Consumes: EAS profile names (Task 4), `bun run version:set` (Tasks 1/12), the five notarization secrets (Task 14), `APNS_TOPIC` default (Task 16).
- Produces: the operator runbook that Task 20 follows.

- [ ] **Step 1: Verify the stale path and the ignore rules**

Run: `grep -n "key_file" apps/mobile/docs/apple/secret-gem.rb apps/mobile/docs/apple/README.md && git check-ignore -v apps/mobile/docs/apple/AuthKey_8MX6Q9WW35.p8 && git ls-files apps/mobile/docs/apple`

Expected: both files point at `/Users/guilherme/Dev/pessoal/calendium/docs/apple/AuthKey_8MX6Q9WW35.p8` (stale); `.gitignore:44:AuthKey_*.p8` matches; `git ls-files` lists only `README.md` and `secret-gem.rb` (the `.p8` stays untracked).

- [ ] **Step 2: Fix the key path**

`apps/mobile/docs/apple/secret-gem.rb:3`:

```ruby
key_file = File.expand_path("AuthKey_8MX6Q9WW35.p8", __dir__)
```

`apps/mobile/docs/apple/README.md:16` (inside the ```ruby block):

```ruby
key_file = File.expand_path("AuthKey_8MX6Q9WW35.p8", __dir__)   # the gitignored .p8 next to this script
```

Run: `ruby -c apps/mobile/docs/apple/secret-gem.rb`

Expected: `Syntax OK`.

- [ ] **Step 3: Write `docs/release/store-readiness.md`**

```markdown
# Store and release readiness — operator runbook

Piece 5 of the production-readiness program. Everything the repository can do
is done: a `vX.Y.Z` tag builds version-stamped desktop installers and the
mobile config passes `expo-doctor` 18/18. What remains needs a human in a
console. Work top to bottom; each section names its owner.

## 1. Hard prerequisites (block submission)

| Item | Owner | Status |
| --- | --- | --- |
| **Account deletion in-app** (Apple Guideline 5.1.1(v), Google Play User Data policy). Shipped by piece 3; both stores reject a build without it. | piece 3 | required |
| Privacy policy URL: `https://<DOMAIN>/privacy` (exists) | operator (hosting, piece 6) | required |
| Support URL / email: `https://<DOMAIN>` + `NEXT_PUBLIC_SUPPORT_EMAIL` (default `support@calendium.app`) | operator | required |
| Apple Developer Program membership, Team ID `CT22R575UG` | operator | have |
| Google Play developer account | operator | required |

## 2. App Store Connect (iOS)

1. Create the app record: bundle id `app.calendium.mobile`, name **Calendium**,
   primary language, SKU `calendium-mobile`.
2. First submission is interactive from the release tag:
   `cd apps/mobile && eas submit --platform ios --profile production`.
   EAS asks for the App Store Connect app and writes `submit.production.ios.ascAppId`
   into `eas.json` — commit that change (it is an identifier, not a secret).
3. App Privacy ("nutrition labels"), derived from `/privacy`:
   - Data collected: **Contact info** (email, name — account), **User content**
     (emails, calendar events — processed to provide the service, never sold).
   - Linked to the user: yes (account). Used for tracking: **no**.
   - Third-party: none beyond the mail/calendar providers the user connects.
4. Export compliance: `ITSAppUsesNonExemptEncryption=false` is in `app.json`
   (only HTTPS); answer **No** to "does your app use encryption" beyond that.
5. Age rating: 4+. Category: Productivity.

## 3. Google Play

1. Create the app: package `app.calendium.mobile`, **Calendium**, Productivity.
2. Data safety form from the same source as §2.3 (collects contact info and
   user content; encrypted in transit; users can request deletion — piece 3).
3. Release track: EAS submits to the **internal** track as a **draft**
   (`eas.json` `submit.production.android`). Promote to production from the console.
4. Service account: Play Console → Setup → API access → create a service
   account with Release Manager role, download its JSON, upload it once with
   `eas credentials --platform android` (EAS-managed; never committed).
5. Permissions declared: only `POST_NOTIFICATIONS` (`app.json`); the storage,
   overlay and exact-alarm permissions are blocked so no declaration form is
   needed.

## 4. EAS

1. `eas init` once against the Calendium Expo account; put the id in the EAS
   project environment as `EAS_PROJECT_ID` (`app.config.js` injects it; it is
   not committed so self-hosters build against their own project).
2. File secret `GOOGLE_SERVICES_JSON` (Firebase Android config, `apps/mobile/docs/push.md`).
3. `eas credentials --platform ios`: distribution certificate + provisioning
   profile + **APNs key** (EAS-managed). The server side needs the same key as
   `APNS_KEY_ID` / `APNS_TEAM_ID` / `APNS_KEY_P8`; the default `APNS_TOPIC` is
   `app.calendium.mobile` and matches `app.json`.
4. `cli.appVersionSource` is `remote`: EAS owns `buildNumber` / `versionCode`
   (`autoIncrement` on the production profile), so no commit per build.
   `expo.version` (the marketing version) comes from the tag via
   `bun run version:set` (§6).

## 5. Desktop

**macOS notarization** needs five GitHub secrets: `MACOS_CERT_P12` (base64 of
the Developer ID Application `.p12`), `MACOS_CERT_PASSWORD`, `APPLE_ID`,
`APPLE_TEAM_ID` (`CT22R575UG`), `APPLE_APP_PASSWORD` (app-specific password).
Since this piece, **a tag fails at `preflight` when `MACOS_CERT_P12` is
empty**; only a manual dispatch with `allow_unsigned=true` builds unsigned, and
it never publishes. Dry-run after setting the secrets: Actions → Release →
Run workflow (`allow_unsigned=false`, `version` empty) and confirm the DMG
staples.

**Windows** is unsigned (no Authenticode certificate yet): SmartScreen shows
"Windows protected your PC" on first run of `calendium_<v>_windows_amd64-setup.exe`;
users click *More info → Run anyway*. The installer registers `calendium://`
and installs to `Program Files\Calendium\Calendium`. The portable zip does not
register the scheme.

**Linux**: the tarball ships `Calendium`, `calendium.desktop`, `calendium.png`
and `README.txt`; the README's `install` / `xdg-mime` lines register the scheme.

**Update banner**: release builds check
`https://api.github.com/repos/GuilhermeVozniak/calendium/releases/latest` 10 s
after launch and daily; `CALENDIUM_UPDATE_URL=off` disables it. Drafts and
prereleases are ignored, so draft the GitHub release freely.

## 6. Release procedure (every version)

```bash
bun run version:set X.Y.Z          # stamps apps/mobile/app.json + apps/desktop/wails.json
git commit -am "release: vX.Y.Z"
git tag vX.Y.Z && git push origin main vX.Y.Z
# release.yml: preflight (version check, signing gate) -> desktop artifacts -> GitHub Release
cd apps/mobile && eas build -p all --profile production --auto-submit   # from the tag checkout
```

The first tag after this piece is **`v1.0.0`** (mobile already shipped `1.0.0`
in its config and store versions cannot go backwards). `preflight` refuses a
tag whose `X.Y.Z` differs from the committed `app.json` / `wails.json`.

## 7. Hygiene

- `apps/mobile/docs/apple/secret-gem.rb` reads the `.p8` next to itself; the
  key file is gitignored (`.gitignore`: `*.p8`, `AuthKey_*.p8`) and must stay
  out of the repository.
- `eas.json` holds no credentials; Apple/Google credentials live in EAS
  (`eas credentials`). Rotate the Apple client secret (§2 of
  `go-live-external-checklist.md`) every 170 days.
```

- [ ] **Step 4: Validate links and Markdown**

Run: `grep -n "profile production\|version:set\|CT22R575UG\|allow_unsigned\|app.calendium.mobile" docs/release/store-readiness.md | wc -l && bunx biome check apps/mobile/docs 2>/dev/null; true`

Expected: a non-zero count (the doc references every identifier above); Biome ignores `.md`/`.rb`.

- [ ] **Step 5: Commit**

```
git add docs/release/store-readiness.md apps/mobile/docs/apple/secret-gem.rb apps/mobile/docs/apple/README.md
git commit -m "docs(release): store-readiness operator runbook; Apple key script reads the .p8 relatively" -m "Co-Authored-By: WOZCODE <contact@withwoz.com>"
```

### Task 19: FULL-SUITE GATE (after all tracks merge)

**Files:** none modified.

- [ ] **Step 1: Backend**

Run: `cd backend && go build ./... && go vet ./... && go test ./... && go build -ldflags "-X calendium/backend/internal/adapter/in/httpapi.Version=gate" ./cmd/api ./cmd/worker; cd ..`

Expected: all packages `ok` (Postgres suites run via testcontainers when Docker is up; set `REQUIRE_DOCKER=1` to fail loudly instead of skipping).

- [ ] **Step 2: Desktop Go**

Run: `cd apps/desktop/frontend && bun run build && cd .. && go build ./... && go vet ./... && go test -race ./...; cd ../..`

Expected: PASS (the frontend build first so `//go:embed all:frontend/dist` resolves).

- [ ] **Step 3: TypeScript suites**

Run: `bun run test:shared && bun run test:web && bun run test:desktop && bun run test:mobile && bun run test:scripts`

Expected: every suite PASS, including `app-config.test.ts` (11), `UpdateBanner.test.tsx` (6), `set-version.test.mjs` (6), `gen-mobile-assets.test.mjs` (5).

- [ ] **Step 4: Lint**

Run: `bunx biome check . && bun run lint:go`

Expected: Biome clean (now including `scripts/*.mjs`); golangci-lint `0 issues` for `backend` and `apps/desktop`.

- [ ] **Step 5: Mobile doctor and config**

Run: `cd apps/mobile && bunx expo-doctor && bunx expo install --check && bunx expo config --type public > /dev/null; cd ../..`

Expected: `18/18 checks passed`, dependencies up to date, config resolves.

- [ ] **Step 6: Version drift and typecheck**

Run: `bun run typecheck && cd apps/desktop/frontend && bunx tsc --noEmit && cd ../../.. && node scripts/set-version.mjs --check "$(node -p "require('./apps/mobile/app.json').expo.version")"; echo exit=$?`

Expected: typechecks clean; the `--check` reports `apps/desktop/wails.json is at 0.1.0, want 1.0.0` with `exit=1` until the operator performs the `v1.0.0` stamp (Task 18 §6) — that is the intended pre-release state, not a gate failure. After `bun run version:set 1.0.0` it prints nothing and `exit=0`.

- [ ] **Step 7: End-to-end**

Run: `bun run test:e2e`

Expected: Playwright demo-mode suite PASS (marketing footer/pricing copy changes are not asserted; demo config defaults unchanged).

- [ ] **Step 8: Working tree**

Run: `git status --short`

Expected: empty (every task committed; `bun.lock` committed in Task 1).

### Task 20: VERIFICATION runbook — release dry run, mobile build, deep links (marks itself BLOCKED, not failed, when operator secrets are absent)

**Files:** none modified.

- [ ] **Step 1: Release dry run (unsigned)**

Precondition: Tasks 1-19 merged to `main` and pushed. Dispatch: GitHub → Actions → Release → Run workflow with `allow_unsigned=true`, `version=0.0.0` (or empty).

Expected: `preflight` logs `version=0.0.0 build_version=0.0.0-dev.<sha7>` and the `::warning::… building UNSIGNED …`; `build-desktop` produces three run artifacts: `calendium_0.0.0-dev.<sha7>_darwin_universal.dmg`, `calendium_0.0.0-dev.<sha7>_windows_amd64-setup.exe` + `.zip`, `calendium_0.0.0-dev.<sha7>_linux_amd64.tar.gz`; the macOS `Assert bundle version` step prints nothing (passes with `0.0.0` / `app.calendium.desktop`); the Windows step shows `ProductVersion` starting with `0.0.0`; the Linux `tar -tzf` lists `Calendium`, `calendium.desktop`, `calendium.png`, `README.txt`.

- [ ] **Step 2: Signing gate refuses an unsigned tag-like run**

If `MACOS_CERT_P12` is **not** configured: dispatch again with `allow_unsigned=false`.

Expected: `preflight` fails at “Signing secrets present, or explicitly waived for a dry run” with the `::error::MACOS_CERT_P12 is not set. A tag never publishes an unsigned macOS build…` message; `build-desktop` is skipped.

If `MACOS_CERT_P12` **is** configured: dispatch with `allow_unsigned=false`, `version` empty.

Expected: the DMG is signed, notarized and stapled (`xcrun stapler validate` passes). If notarytool returns 401, mark this step **BLOCKED (operator: re-issue APPLE_APP_PASSWORD / verify APPLE_ID, see go-live-external-checklist.md §6)** — not failed.

- [ ] **Step 3: Windows deep link (manual, per release)**

On a Windows machine: run `calendium_<v>_windows_amd64-setup.exe` (accept the SmartScreen *More info → Run anyway*), launch Calendium, then in a browser open `calendium://accounts/connected?status=ok`.

Expected: no second window opens; the running app comes to the front and the Settings view refreshes the account list (the deep link reached `onSecondInstance` → `handleURL`). With the app closed, the same URL launches it and the link is delivered after the window appears (`consumeArgs` → `pendingURL` → `startup`).

No Windows machine available → **BLOCKED (operator)**.

- [ ] **Step 4: Linux deep link (manual, per release)**

Extract the tarball, run the five lines from `README.txt`, then `xdg-open 'calendium://accounts/connected?status=ok'`.

Expected: same behaviour as Step 3 (focus + delivery; cold launch delivers after startup). `xdg-mime query default x-scheme-handler/calendium` prints `calendium.desktop`.

No Linux desktop available → **BLOCKED (operator)**.

- [ ] **Step 5: Update banner**

On any platform with a release build (`main.version` = `X.Y.Z`), run with `CALENDIUM_UPDATE_URL` pointing at a local file server that returns `{"tag_name":"v99.0.0","html_url":"https://example.com/r","draft":false,"prerelease":false}` (e.g. `python3 -m http.server` in a directory with that JSON saved as `latest` and `CALENDIUM_UPDATE_URL=http://127.0.0.1:8000/latest`).

Expected: ~10 s after launch a one-line bar reads `Calendium 99.0.0 is available. You have X.Y.Z.`; **Download** opens `https://example.com/r` in the browser; **Later** hides it and `localStorage['calendium.update.dismissed'] === '99.0.0'`. A dev build (`wails dev`) never shows the banner and never requests.

- [ ] **Step 6: Mobile store build**

Precondition: EAS project, `EAS_PROJECT_ID`, `GOOGLE_SERVICES_JSON` secret and `eas credentials` for both platforms (Task 18 §4). From a checkout of the release tag:

Run: `cd apps/mobile && eas build -p all --profile production`

Expected: both builds succeed; the iOS build's `CFBundleShortVersionString` equals `app.json` `expo.version`, `CFBundleVersion` is EAS-assigned (remote app version source); Android `versionName` equals `expo.version`, `versionCode` auto-incremented. The app icon on the device is the black rounded square with the calendar glyph; the splash shows the glyph centred on white (light) / near-black (dark).

Missing EAS project or credentials → **BLOCKED (operator: Task 18 §4)**.

- [ ] **Step 7: Mobile submission**

Run: `cd apps/mobile && eas submit --platform ios --profile production` (interactive the first time; commit the `ascAppId` it writes), then `eas submit --platform android --profile production`.

Expected: TestFlight processing starts; Play shows a draft release on the internal track. Store review itself is out of scope.

No App Store Connect / Play app record, or account deletion (piece 3) not yet merged → **BLOCKED (operator / piece 3)**.

- [ ] **Step 8: Record the outcome**

Append a dated line to `docs/release/store-readiness.md` §1 status column for each item verified or BLOCKED, commit as `docs(release): verification <date>` with the `Co-Authored-By: WOZCODE <contact@withwoz.com>` trailer.

