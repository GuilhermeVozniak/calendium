# Calendium

An email + calendar manager designed around speed and keyboard-first UX — a Superhuman-class email client deeply integrated with a best-in-class calendar, on **web, desktop, and mobile**.

## Monorepo layout (bun workspaces)

| Path | What | Stack |
| --- | --- | --- |
| `backend/` | API + sync workers | Go (stdlib only), hexagonal architecture, Postgres |
| `apps/web/` | Landing page + web app | Next.js (App Router), React, Tailwind, shadcn/ui |
| `apps/desktop/` | Desktop app | Wails v2 (Go) + React/Vite frontend |
| `apps/mobile/` | iOS / Android app | Expo + React Native, react-native-reusables, NativeWind |
| `packages/shared/` | Shared domain types + API client | TypeScript |
| `docs/` | Architecture, tech stack, feature map, payments | — |

## Getting started

```bash
bun install            # installs all JS workspaces
bun run dev:api        # Go API on :8080
bun run dev:web        # Next.js on :3000
bun run dev:mobile     # Expo dev server
bun run dev:desktop    # Wails dev (requires wails CLI)
```

See `docs/architecture.md` for the full system design, `docs/tech-stack.md` for the stack map, `docs/feature-map.md` for the Superhuman/calendar feature parity plan, and `docs/payments.md` for the Stripe subscription flow.
