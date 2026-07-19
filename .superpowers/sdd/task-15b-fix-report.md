# Task 15b Fix Report — Offline outbox acting-identity isolation (M2.7 review)

## Bug
Outbox entries were not tagged with the acting identity. `startOutboxReplay` drained the
whole queue through `getApiClient()`, which returns the X-Calendium-Act-As client whenever
acting state is set — so entries queued as SELF could replay against the PRINCIPAL's
account (and vice versa). Cross-identity mutation.

## Fix (entry tagging + identity-filtered replay)
- `packages/shared/src/offline/outbox.ts`: added optional `actingAs?: string | null` to
  `OutboxEntry` (missing ⇒ null ⇒ self, so legacy persisted entries keep working).
  `enqueue(action, { actingAs })` tags new entries, and coalescing/dedupe/replace are now
  identity-scoped — a self star and an acting unstar are different accounts' mutations and
  both survive. `replay(client, { actingAs })` only sends entries whose tag matches the
  requested identity (default null = self); non-matching entries stay queued untouched and
  replay when the user returns to that identity. Purely additive optional params —
  desktop/mobile callers (no opts, no tags) behave exactly as before.
- `apps/web/lib/offline/queue.ts`: `queueAction` tags with `getActingAs()` at enqueue time;
  `startOutboxReplay` passes `{ actingAs: getActingAs() }` alongside `getApiClient()` — the
  client and the filter read the same synchronous state, so the client always carries
  exactly the identity of the entries it replays.
- Sign-out semantics unchanged: `clearOfflineState()` / `Outbox.clear()` still wipe every
  entry regardless of tag; existing sign-out tests untouched and passing.

Scope note: the reviewer's tag-at-enqueue recommendation requires the entry schema and
replay loop, which live in the shared outbox engine — the shared change is the minimal
additive surface for a durable (reload-surviving) tag.

## Tests
- `apps/web/lib/offline/queue.test.ts` — new "acting identity isolation" describe:
  (1) self-queued entry parked while acting, replays after acting stops;
  (2) acting-queued entry never replays as self, replays when acting again;
  (3) legacy untagged entry treated as self (parked while acting, replays as self).
- `packages/shared/src/offline/outbox.test.ts` — tagging, no cross-identity
  coalesce/dedupe/replace, replay filters by requested identity (matching-identity replay).
- No existing tests weakened.

## Validation
- `apps/web` vitest: 81 files / 954 tests passed (exit 0)
- `packages/shared` vitest: 7 files / 301 tests passed (exit 0)
- `tsc --noEmit`: clean in apps/web and packages/shared
- `biome check` on the 4 touched files: clean
