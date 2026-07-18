# Task 13: Web settings — signature editor + auto-BCC per account (M2.5)

## Status: Complete

Branch: `worktree-agent-a9af7888b5905ffa9` (based on `main`, merged `feat/m2-5-compose`)

## Setup

- `git merge feat/m2-5-compose --no-edit` — fast-forward, no conflicts (worktree
  was already at the merge-base commit).
- Confirmed `setSignature`/`setAutoBcc` exist on `ApiClient`
  (`packages/shared/src/client.ts`) and `signatureHtml`/`autoBcc` exist on
  `ConnectedAccount` (`packages/shared/src/types.ts`), both from commit
  `58f280c`.
- `node_modules` was entirely absent in the worktree; ran `bun install`
  (1145 packages, lefthook hooks re-synced).

## Files changed

- `apps/web/components/app/chips-row.tsx` (new) — `EMAIL_RE`, `parseAddress`,
  and `ChipsRow` extracted verbatim from `compose.tsx` so the settings
  auto-BCC editor can reuse the exact same recipient-chip parse/commit logic
  instead of duplicating it.
- `apps/web/components/app/compose.tsx` (modified) — removed the local
  `EMAIL_RE`/`parseAddress`/`ChipsRow` definitions; imports them from
  `chips-row.tsx` and re-exports `{ ChipsRow, parseAddress }` so any existing
  import of these symbols from `compose.tsx` keeps working unchanged.
  `compose.test.tsx` was not modified and needed no changes — all 36 tests
  still pass since behavior is byte-for-byte identical.
- `apps/web/lib/settings-data.ts` (modified) — added `setSignatureApi` and
  `setAutoBccApi`, mirroring the existing `setVipSendersApi` wrapper pattern
  (hits `getApiClient()`, falls back to `settingsMock` only in explicit demo
  mode, otherwise propagates the real error/ApiRequestError).
- `apps/web/lib/settings-mock.ts` (modified) — added `signatureHtml`/`autoBcc`
  to both seeded mock accounts, plus `settingsMock.setSignature` /
  `settingsMock.setAutoBcc` mutators.
- `apps/web/app/(app)/settings/page.tsx` (modified) —
  - `AccountsSection` is now exported (was previously private to the module)
    so the new test file can render it directly, matching the mocking
    boundary used by `compose.test.tsx` (mock `@/lib/api`, not `@/lib/settings-data`,
    so the real wrapper logic — including the demo-mode fallback — runs for real).
  - New "Compose" button per connected-account row (next to VIP), opening
    `ComposeSettingsDialog`.
  - New `SignatureEditor`: a `contentEditable` div with Bold/Italic/Link
    buttons driving `document.execCommand` (no new rich-text dependency, per
    the brief), hydrated once on mount (not on every keystroke, to avoid
    stomping the live DOM/caret), plus a live preview pane below it.
  - New `ComposeSettingsDialog`: renders the signature editor and an
    auto-BCC `ChipsRow` (adapted from `EmailAddress[]` to `string[]` via a
    thin map), each with its own independent "Save signature" / "Save
    auto-BCC" button and mutation — so an in-flight signature edit and a
    rejected auto-BCC save don't block each other. A 400 from `setAutoBccApi`
    is surfaced inline under the chip row (`bccError` state); other errors
    and the signature-save path use a toast. Both success toasts are
    resolution-gated (only fire in the mutation's `onSuccess`).
- `apps/web/app/(app)/settings/settings-compose.test.tsx` (new) — 9 tests:
  - Renders current signature/auto-BCC from account data.
  - Save calls `setSignature` with the edited HTML.
  - Save calls `setAutoBcc` with the edited address list.
  - Invalid auto-BCC email is rejected inline by `ChipsRow`'s own
    `parseAddress` gate (chip never created, draft text preserved,
    `setAutoBcc` never called).
  - A 400 from `setAutoBcc` is surfaced inline (not as a toast).
  - Demo-mode round trip: `setSignatureApi`/`setAutoBccApi` fall back to
    `settingsMock` when the API rejects and `NEXT_PUBLIC_DEMO_MODE=true`, and
    the change is verified to actually persist in the mock store (not just
    the return value).
  - Outside demo mode, a real API failure propagates (no silent fallback) —
    pins the honesty-policy contract.

## Honesty-policy notes

- No mock data outside the demo gate: `setSignatureApi`/`setAutoBccApi` only
  fall back to `settingsMock` when `DEMO_MODE` is true; both paths are
  covered by tests (fallback in demo mode, propagation outside it).
- Success toasts only fire from each mutation's `onSuccess` (post-resolution),
  never optimistically.
- Invalid auto-BCC entries are rejected client-side inline (same
  `parseAddress` gate compose uses for To/Cc/Bcc — never committed as a
  chip), and a genuine 400 from the server is surfaced inline next to the
  chip row rather than swallowed or shown as a generic toast.

## Verification (all commands run with explicit exit-code capture)

- `cd apps/web && bunx tsc --noEmit` — exit 2, but diffed against the
  pre-my-changes baseline (`git stash` + rerun): the same 8 errors exist
  before and after my changes, all in files I don't own (`compose.test.tsx`,
  `event-dialog.test.tsx`, `conflict-warning.test.tsx`, `thread-view.test.tsx`,
  `calendar-accounts.test.ts`, `mail-mock.ts`) — pre-existing fallout from the
  M2.5 shared-types merge (`ConnectedAccount` gained required
  `signatureHtml`/`autoBcc`; `Message` gained required `reactions`) that other
  parallel tasks own and will fix in their own account/message literals. Zero
  new errors from this task's own files.
- `cd apps/web && bunx vitest run` — **54 files / 713 tests passed**, exit 0.
- `bunx biome check <all 6 touched/new files>` — exit 0, only 1 pre-existing
  warning (`queryClient` unused in the untouched `MailboxSection`, confirmed
  via the same pre/post `git stash` diff). Fixed 3 real lint errors introduced
  by the new `SignatureEditor`/preview markup along the way:
  - `useFocusableInteractive` — added `tabIndex={0}` to the contentEditable
    `role="textbox"` div.
  - `useSemanticElements` — suppressed with a `biome-ignore` (rich-text
    formatting via `execCommand` can't be represented by a plain
    `<textarea>`), matching the existing convention in
    `calendar/agenda-view.tsx`/`month-view.tsx`/`time-grid.tsx`.
  - `noDangerouslySetInnerHtml` on the signature preview pane — suppressed
    with a `biome-ignore` (signature HTML is sanitized server-side at the
    HTTP boundary before storage, per `backend/internal/service/account.go`'s
    `SetSignature`; this renders the owner's own already-sanitized signature,
    not third-party input). Note: this rule's suppression comment only took
    effect as an inline `//` comment directly preceding the
    `dangerouslySetInnerHTML` JSX *attribute* — a `{/* biome-ignore ... */}`
    comment placed as a JSX sibling before the element (which worked fine for
    the other two rules above) was silently ignored by Biome 2.5.2 for this
    specific rule.

## Ownership-boundary compliance

- Touched only: `apps/web/app/(app)/settings/page.tsx`,
  `apps/web/lib/settings-data.ts`, `apps/web/lib/settings-mock.ts`,
  `apps/web/components/app/chips-row.tsx` (new),
  `apps/web/components/app/compose.tsx` (only to extract `ChipsRow`, not to
  change any behavior), and the new test file. Did not touch thread view,
  mail page, palette, or any `ConnectedAccount`/`Message` literal in files
  owned by other parallel tasks (confirmed via `git status --short` before
  every edit and a `git stash` diff of `tsc`/`biome` output to isolate
  pre-existing vs. newly introduced issues).

## Concerns

- None blocking. The 8 pre-existing `tsc` errors and 1 pre-existing `biome`
  warning outside my ownership are called out explicitly above so the
  integrating agent doesn't mistake them for regressions from this commit.
