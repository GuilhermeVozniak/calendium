# Calendium Tech Stack Map

## Repository

| Concern | Choice | Notes |
| --- | --- | --- |
| Package manager / workspaces | **bun** (`bun install`, workspaces `apps/*`, `packages/*`) | Single root lockfile (`bun.lock`) |
| Formatting | Prettier (root `.prettierrc`, tailwind plugin) | |
| Language baselines | TypeScript 5.9 strict everywhere; Go 1.26 | |

## backend/ — Go API + workers

| Concern | Choice |
| --- | --- |
| Architecture | Hexagonal (ports & adapters); `domain ← port ← service`, adapters at the edges |
| HTTP | stdlib `net/http` with Go 1.22+ method/pattern routing — **no framework** |
| DB | Postgres via `database/sql` + `pgx/v5/stdlib` driver (only external dep); embedded SQL migrations |
| Auth | Pure resource server: verifies Better Auth JWTs locally with stdlib crypto — EdDSA/Ed25519 (default) via JWKS, RS256/ES256 also supported; `iss` pinned, `sub`→user upsert |
| Mail/calendar providers | Gmail API + Google Calendar API, Microsoft Graph — raw REST via `net/http` |
| Billing | Paddle Billing REST via `net/http`; webhook HMAC-SHA256 verification via `crypto/hmac` |
| Push | APNs (HTTP/2, ES256 JWT), FCM v1 (service-account JWT), Web Push (VAPID) — all stdlib |
| AI | OpenRouter chat completions (`net/http`), model configurable |
| Secrets at rest | Provider refresh tokens AES-256-GCM encrypted |

## apps/web — Next.js

| Concern | Choice |
| --- | --- |
| Framework | Next.js 15 App Router, React 19, TypeScript |
| Styling | Tailwind CSS v4 + shadcn/ui (**new-york style, neutral base** — same design language as react-native-reusables on mobile) |
| Structure | `(marketing)` route group: landing page; `(app)` route group: the mail + calendar client |
| Auth | **Hosts Better Auth** (`better-auth`) at `/api/auth/*` — email+password + Google/Apple, `jwt()` (EdDSA/Ed25519 JWKS) + `bearer()` + `@better-auth/expo` plugins; own tables in the shared Postgres |
| Data | `@calendium/shared` ApiClient + TanStack Query |
| Icons / fonts | lucide-react; `next/font` (Inter / Geist) |
| Push | Web Push via service worker + VAPID |

## apps/desktop — Wails v2

| Concern | Choice |
| --- | --- |
| Shell | Wails v2 (Go host, native WebView; menu bar, global shortcuts, badge counts) |
| Frontend | React 19 + Vite + TypeScript in `apps/desktop/frontend` (bun workspace member) |
| Styling | Tailwind v4 + the same shadcn/ui new-york components as web |
| Auth/data | Better Auth client (bearer tokens) + `@calendium/shared` ApiClient; deep-link `calendium://` for OAuth callbacks |
| Payments | No in-app purchase — opens the web checkout in the default browser (Spotify model) |

## apps/mobile — Expo / React Native

| Concern | Choice |
| --- | --- |
| Runtime | Expo SDK 54, React Native 0.81, New Architecture, Expo Router |
| UI | **react-native-reusables** (shadcn new-york port) + NativeWind 4 — the design system of record |
| Auth | Better Auth via `@better-auth/expo` (email+password + Google/Apple, deep-link flow) |
| Data | `@calendium/shared` ApiClient + TanStack Query |
| Push | `expo-notifications` → APNs / FCM tokens registered at `/v1/devices` |
| Payments | **No IAP.** Subscription managed on the web (Spotify model); app shows plan state + link out |

## packages/shared — TypeScript contract

Domain types mirroring `backend/internal/domain` + a typed `ApiClient` used by web, desktop, and mobile. No runtime deps.

## Design system

One visual language across platforms, imposed by react-native-reusables / shadcn (new-york, neutral):
tokens in HSL CSS variables (`--background`, `--foreground`, `--primary`, … with the neutral palette in `apps/mobile/global.css`), radius `0.625rem`, lucide icon set, `cva` variants; light + dark themes everywhere.
