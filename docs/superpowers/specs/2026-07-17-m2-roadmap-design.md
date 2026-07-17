# M2 Roadmap — Full Parity, Ordered by User Importance

> Approved Jul 17 2026. M0 + M1 are complete in code and fully tested (see `docs/state-and-gaps.md`). This spec decomposes M2 — full Superhuman email parity + best-in-class calendar — into eight phases ordered by importance to the user, each of which gets its own implementation plan in `docs/superpowers/plans/`.

## Ordering principle

1. **Daily-use frequency first** — features users touch dozens of times a day (triage) beat features touched weekly.
2. **The product bet second** — a best-in-class calendar is Calendium's differentiation vs Superhuman; it lands before AI because AI features act on mail/calendar primitives that must feel instant first.
3. **Dependency order** — collaboration needs new multi-user infrastructure (teams, permissions) and serves fewer users on day one, so it comes after single-player value is maxed.
4. The feature map's own P0/P1/P2 ratings (`docs/feature-map.md`) break ties inside each phase.

## Phases

### M2.1 — Triage power & speed (plan: `2026-07-17-m2-1-triage-power.md`)
Auto-advance; bulk triage (range select + bulk archive/read/label); Get Me To Zero; one-click + bulk unsubscribe (List-Unsubscribe/mailto+one-click); stars/labels fully keyboard-driven; reorderable splits; undo-anything (Z); inbox-zero celebration; shortcut-teaching UX + expanded shortcut coverage; sub-100ms interaction budget measured and enforced (perf harness).

### M2.2 — Calendar core parity (plan: `2026-07-17-m2-2-calendar-core.md`)
Natural-language event parsing (extend `lib/quick-add`); full view range day/week/month/quarter/year + DayTicker; multi-account overlay with cross-account conflict blocking; conference-link detection with Join button + one-click add conferencing (Meet/Teams via providers); multi-timezone grid columns; keyboard-first calendar (T/J/K/S, unified ⌘K actions); calendar peek beside the inbox; event templates; calendar sets.

### M2.3 — AI suite (plan: `2026-07-17-m2-3-ai-suite.md`)
Auto Drafts; Instant Reply (3 precomputed replies); Ask AI with cited sources + persistent sidebar; live Auto Summarize; AI editing commands (improve/shorten/tone); Auto Labels + custom natural-language classifiers routed to splits; Auto Reminders; personal voice learning; AI scheduling drafts (availability-aware replies); Instant Event AI (event from thread).

### M2.4 — Scheduling & booking (plan: `2026-07-17-m2-4-scheduling-booking.md`)
Personal booking links/pages; appointment schedules (windows, buffers, daily limits); meeting polls (propose times, invitees vote, confirm winner); propose-new-time RSVP flow; drag-and-copy availability as recipient-timezone text; recipient-TZ preview; Time Travel timezone overlay; working hours/location; Find-a-Time guest availability grid.

### M2.5 — Compose extras & contact context (plan: `2026-07-17-m2-5-compose-contact.md`)
Smart Send (recipient-timezone send-time suggestions); Instant Intro; Recent Opens feed; per-account rich signatures; attachment quick access + inline previews; Auto Bcc; emoji reactions; contact pane with insights + recent conversations.

### M2.6 — Offline & platform polish (plan: `2026-07-17-m2-6-offline-platform.md`)
Offline mode (local cache + queued send); preloading architecture; global desktop shortcuts (Wails); menu-bar mini calendar + next-event countdown with instant join; auto-join meetings; multi-account switching shortcuts; named themes.

### M2.7 — Collaboration & teams (plan: `2026-07-17-m2-7-collaboration.md`)
Teams/membership model + permissions (new backend domain); shared conversations (live thread links); team comments with @mentions; team read statuses/reply indicators; team snippets; shared calendars with granular permissions; team availability overview; team scheduling links; Find Time inline; EA delegation mode.

### M2.8 — Calendar life integrations & long tail (plan: `2026-07-17-m2-8-integrations-longtail.md`)
Tasks/todos on the grid (+ external todo tools); docs/notes attached to events; travel time + Maps locations; FocusGuard + focus auto-decline + auto buffers; OOO auto-decline; time analytics/insights; weather; interesting-calendar subscriptions; email-to-event drag; CRM integrations (HubSpot/Salesforce/Pipedrive); concierge onboarding; delight extras.

## Cross-plan notes

- **Migration numbering:** phase plans were authored in parallel and each references provisional migration filenames (e.g. `0005_*.sql`); the executing agent assigns the next free number in `backend/migrations/` at execution time.
- **Declared dependencies:** M2.7 team scheduling links build on M2.4 `booking_links`; M2.3's ai_jobs worker loop and M2.4's hold-expiry sweep both extend `cmd/worker`.
- M2.6's audit found a real existing gap to fix in that phase: `handleListThreads` never parses `accountId` although `ThreadQuery.AccountID` filtering exists.

## Execution model

Each phase: brainstorm deltas if needed → detailed implementation plan (TDD, bite-sized tasks) → execute → full test gates → ship → next phase. Phase plans for later phases are written at task granularity now (files, interfaces, test strategy) and get a final code-level pass when their turn arrives, so they never rot. M2.1's plan is written execution-ready immediately.

## Success criteria

A phase is done when: every feature in its list works on live data on the platforms named in `docs/feature-map.md`, is covered by tests at every touched layer, passes the lefthook/CI gates, and (for interaction features) meets the sub-100ms local-interaction budget.
