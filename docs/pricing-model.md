# Pricing & the open-core model

Calendium is **open core**, in the same spirit as Supabase, Cal.com, Plausible, and
n8n. The entire product — the Go backend, sync workers, the Next.js web app, and the
desktop & mobile clients — is free and open source under the **GNU AGPLv3** (see
[`LICENSE`](../LICENSE)). There are exactly two ways to run it:

| | **Calendium Cloud** | **Self-hosted** |
| --- | --- | --- |
| Who operates it | We do — managed hosting | You do — your VPS, home server, or your AWS/GCP/Azure |
| Price | **$50 / year** via Paddle (14-day trial, Paddle is merchant of record) | **Free**, forever |
| License | Same AGPLv3 source; you pay for the *service* | AGPLv3 |
| Software features | All of them | All of them — nothing is held back |
| Updates & migrations | Applied for you | You `git pull` / re-deploy |
| Backups, monitoring, uptime | Ours | Yours |
| Support | Email support, SLA-backed | Community (GitHub issues / discussions) |
| Data location | Our infrastructure | Wherever you put it |
| Provider keys (Google/Microsoft/AI) | Managed for you | Bring your own |
| Paddle / billing code path | Active | Disabled (`SELF_HOSTED=true`) |

The two tiers run the **same binaries from the same repository**. The only difference is
who runs the servers and whether the billing path is switched on.

## Where the line is

The product is open; **the paid thing is the managed service**, not the software.

- **What you pay for on Cloud:** we run and scale the Postgres + Go API + web app, keep
  them patched and backed up, register and rotate the Google/Microsoft/OpenRouter/push
  credentials, monitor uptime, and answer support. That operational work — hosting,
  updates, support — is the value of the $50/year subscription.
- **What is always free and open:** every feature of the software. There is no
  "enterprise edition", no feature flag gated behind a license key, no closed core. A
  self-hoster gets the identical mail + calendar + AI + push feature set that a Cloud
  subscriber gets. If a capability ships, it ships in the open-source tree.

This is deliberately the Supabase / Cal.com boundary: **the code is a commodity we give
away; the convenience of not operating it is the commercial offering.** The AGPLv3
ensures that anyone who runs a modified Calendium as a network service must share their
modifications, which keeps the open core healthy while still allowing us to sell hosting.

## How entitlement works

Entitlement is decided by one backend flag: **`SELF_HOSTED`** (see the Environment
section of [`architecture.md`](./architecture.md)).

- **`SELF_HOSTED=false` (default — Cloud):** billing is live. Users go through the Paddle
  flow in [`payments.md`](./payments.md); the paywall gates access on subscription state
  (`none → trialing → active → …`).
- **`SELF_HOSTED=true` (self-hosted):** the paywall is **turned off**. Concretely:
  - `GET /v1/instance` reports `mode: "self_host"` and `features.billing: false`, so every
    client self-configures without a purchase surface.
  - `GET /v1/billing/subscription` returns a synthetic **active** annual plan (no period
    bounds), so the app treats the user as fully entitled.
  - `POST /v1/billing/checkout`, `POST /v1/billing/portal`, and the Paddle webhook all
    return **`501 self_hosted`** — there is no Paddle integration to reach, and no Paddle
    keys are required to boot.

Clients never hard-code which mode they are in. They call `GET /v1/instance` (public,
unauthenticated) against whatever server base URL they were pointed at, read `mode` and
`features`, and hide the paywall / billing UI when `features.billing` is false. Point the
desktop or mobile app at your self-hosted server and it simply never shows a purchase
screen; point it at Cloud and it does.

## See also

- [`payments.md`](./payments.md) — the Cloud billing flow (Paddle, $50/year, states,
  webhooks). Applies only when `SELF_HOSTED=false`.
- [`self-hosting/README.md`](./self-hosting/README.md) — how to run the whole stack
  yourself with Docker Compose in a few minutes.
- [`architecture.md`](./architecture.md) — `GET /v1/instance` discovery, the `SELF_HOSTED`
  environment flag, and the deployment-modes overview.
- [`LICENSE`](../LICENSE) — the full GNU Affero General Public License v3.0.
