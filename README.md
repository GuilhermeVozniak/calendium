# Calendium

An email + calendar manager designed around speed and keyboard-first UX — a Superhuman-class email client deeply integrated with a best-in-class calendar, on **web, desktop, and mobile**.

> **License:** open source under the [GNU AGPLv3](LICENSE) — self-host the entire stack for free. A managed, commercial **Calendium Cloud** ($50/year) is also offered. See [docs/pricing-model.md](docs/pricing-model.md).

## Two ways to run Calendium

Calendium is **open core** (like Supabase, Cal.com, Plausible, n8n): the software is free and open; managed hosting is the paid option. Both run the same binaries — the only difference is who operates the servers.

### Self-hosting (free)

Run Calendium yourself in minutes with Docker Compose — the Go backend, Postgres, and the web app on your own VPS, home server, or cloud — then point the desktop & mobile clients at **your** server. Every feature is unlocked, no Stripe, no license keys.

```bash
cp .env.example .env   # fill in Better Auth secrets + a token key
docker compose up -d --build
```

Full guide (HTTPS, providers, upgrades): **[docs/self-hosting/](docs/self-hosting/README.md)**.

### Calendium Cloud (paid)

Prefer we run it? **Calendium Cloud** is our managed hosting — patched, backed up, and supported — for **$50/year** via Stripe. The billing flow lives in [docs/payments.md](docs/payments.md), and entitlement (`SELF_HOSTED` toggles the paywall) is explained in [docs/pricing-model.md](docs/pricing-model.md).

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

See `docs/architecture.md` for the full system design, `docs/tech-stack.md` for the stack map, `docs/feature-map.md` for the Superhuman/calendar feature parity plan, `docs/pricing-model.md` for the open-core Cloud-vs-self-hosted model, `docs/payments.md` for the Stripe subscription flow, and `docs/self-hosting/` for running Calendium yourself.

## License

Calendium is licensed under the [GNU Affero General Public License v3.0](LICENSE). The source is free to use, modify, and self-host under the AGPLv3; **Calendium Cloud** is a separate, commercial managed-hosting offering built on the same code. See [docs/pricing-model.md](docs/pricing-model.md).
