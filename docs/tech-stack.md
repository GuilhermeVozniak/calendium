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
| Auth | Supabase JWT verified locally with stdlib crypto (HS256 secret or RS256/ES256 JWKS) |
| Mail/calendar providers | Gmail API + Google Calendar API, Microsoft Graph — raw REST via `net/http` |
| Billing | Stripe REST via `net/http`; webhook HMAC-SHA256 verification via `crypto/hmac` |
| Push | APNs (HTTP/2, ES256 JWT), FCM v1 (service-account JWT), Web Push (VAPID) — all stdlib |
| AI | OpenRouter chat completions (`net/http`), model configurable |
| Secrets at rest | Provider refresh tokens AES-256-GCM encrypted |

## apps/web — Next.js

| Concern | Choice |
| --- | --- |
| Framework | Next.js 15 App Router, React 19, TypeScript |
| Styling | Tailwind CSS v4 + shadcn/ui (**new-york style, neutral base** — same design language as react-native-reusables on mobile) |
| Structure | `(marketing)` route group: landing page; `(app)` route group: the mail + calendar client |
| Auth | `@supabase/supabase-js` (Google/Apple OAuth) |
| Data | `@calendium/shared` ApiClient + TanStack Query |
| Icons / fonts | lucide-react; `next/font` (Inter / Geist) |
| Push | Web Push via service worker + VAPID |

## apps/desktop — Wails v2

| Concern | Choice |
| --- | --- |
| Shell | Wails v2 (Go host, native WebView; menu bar, global shortcuts, badge counts) |
| Frontend | React 19 + Vite + TypeScript in `apps/desktop/frontend` (bun workspace member) |
| Styling | Tailwind v4 + the same shadcn/ui new-york components as web |
| Auth/data | Same Supabase session + `@calendium/shared` ApiClient; deep-link `calendium://` for OAuth callbacks |
| Payments | No in-app purchase — opens the web checkout in the default browser (Spotify model) |

## apps/mobile — Expo / React Native

| Concern | Choice |
| --- | --- |
| Runtime | Expo SDK 54, React Native 0.81, New Architecture, Expo Router |
| UI | **react-native-reusables** (shadcn new-york port) + NativeWind 4 — the design system of record |
| Auth | Supabase (`expo-auth-session` deep-link flow, already implemented) |
| Data | `@calendium/shared` ApiClient + TanStack Query |
| Push | `expo-notifications` → APNs / FCM tokens registered at `/v1/devices` |
| Payments | **No IAP.** Subscription managed on the web (Spotify model); app shows plan state + link out |

## packages/shared — TypeScript contract

Domain types mirroring `backend/internal/domain` + a typed `ApiClient` used by web, desktop, and mobile. No runtime deps.

## Design system

One visual language across platforms, imposed by react-native-reusables / shadcn (new-york, neutral):
tokens in HSL CSS variables (`--background`, `--foreground`, `--primary`, … with the neutral palette in `apps/mobile/global.css`), radius `0.625rem`, lucide icon set, `cva` variants; light + dark themes everywhere.
