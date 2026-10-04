# Collaboration Architecture (M2.7)

M2.7 adds the team layer: teams/members/invitations, shared conversations, team comments with @mentions, team read statuses, team snippets, shared calendars with granular permissions, team availability (Find a Time / Find Time inline), team scheduling links, and EA delegation with an audit log. This document records the four architectural decisions that shape the whole layer, the deliberate scope cuts, and the billing note.

## Decision 1 — Authorization lives in the service layer, expressed as `ErrForbidden`

Every team-scoped operation authorizes inside the service layer before touching data, never in HTTP handlers and never by trusting the query alone. The doctrine is GetMember-first: a service call resolves the caller's membership in the target team up front; a non-member gets `ErrNotFound` (404 — the resource's existence is not revealed), while a member whose role is insufficient gets `domain.ErrForbidden`, which the HTTP codec maps to 403. Repositories additionally scope every team query by membership in SQL, so a service-layer mistake cannot widen into a cross-tenant leak — the phase's #1 security risk. Cross-tenant negative tests ride along with each repository's integration tests.

## Decision 2 — Realtime is SSE over the `EventBus` port; LISTEN/NOTIFY is the scale path

Live updates (shared-conversation views, comments, read statuses) stream over Server-Sent Events from `GET /v1/stream`, authenticated with a real `Authorization` header (no token-in-URL). The endpoint subscribes through a driven `EventBus` port whose current adapter is an in-process broker — correct for a single API process. When the API scales past one process, the same port gets a Postgres LISTEN/NOTIFY adapter so publishers on any process reach subscribers on every process; no handler or service code changes. Connection state is surfaced truthfully to clients — no fabricated liveness. Known consequence, accepted for now: events published by `cmd/worker` (a separate process) do not reach API-process SSE subscribers until the LISTEN/NOTIFY adapter lands.

## Decision 3 — Explicit-share-only privacy

Team membership alone grants visibility into nothing. A thread becomes team-visible only through an explicit share (`thread_shares`); a calendar only through an explicit grant (`calendar_shares`). Supporting rules:

- Public share links store only a hash of the token (`token_hash`), and the public view fails closed — a revoked or unknown token yields nothing, and internal identifiers (account ids, provider thread ids) are never serialized into the public payload.
- Calendar grants are tiered (free/busy-only upward); the free/busy tier redacts event fields to opaque busy blocks server-side, not client-side.
- Team availability shows opaque busy/free blocks only (no titles/attendees), capped to a 35-day window.
- Read statuses are shared only while the member's `share_read_statuses` opt-in/opt-out flag allows it.
- Grants do not outlive membership: a share is re-checked against the sharer's current team membership at resolution time (fail-closed), so leaving a team retires the shares that person created.
- Delegated (EA) actions record the acting principal in an append-only `audit_entries` log (resource type + metadata), written with the action.

## Decision 4 — Conversations correlate across mailboxes via RFC Message-ID

Provider thread ids are per-mailbox: the "same" conversation has a different Gmail/Graph thread id in each teammate's account. Team read statuses and reply indicators therefore correlate conversations by RFC 5322 `Message-ID` headers, which are stable across recipients. This makes cross-member features provider-neutral and works for mixed Gmail/Outlook teams without any provider-side cooperation.

## Out of scope: provider-level sharing

Collaboration is a Calendium-local layer over the Postgres mirror. We deliberately do not write Google Calendar ACLs or Microsoft Graph permissions when a user shares a calendar or thread. Rationale: teams span providers (a Gmail user sharing with an Outlook teammate has no provider-side sharing primitive at all); provider ACLs would create a second source of truth with divergent revocation semantics; and local-only sharing keeps revocation instant and fail-closed. If provider-side sharing is ever wanted, it becomes an explicit, separate export action — never an implicit side effect of a Calendium share.

Also deliberately deferred: round-robin team scheduling links (collective all-free mode shipped; round-robin noted in code as a follow-up), multi-instance SSE fan-out (Decision 2), and mobile/desktop collaboration screens beyond the shared `ApiClient` plumbing (web is the reference UI this phase).

## Billing note — team plans are future work

Billing is unchanged: per-user $50/yr via Paddle (Spotify model). Teams have no billing dimension yet — every member needs their own active subscription/trial. A seat-based team plan (seat-quantity subscription, owner-pays, proration on member changes) is flagged as future Paddle work and intentionally not part of M2.7.
