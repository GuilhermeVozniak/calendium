# Task 14: Web compose — signature auto-apply, Smart Send nudge, Instant Intro (M2.5)

## Status: Complete

Branch: `worktree-agent-abdfb2284f791a670` (based on `main`, merged `feat/m2-5-compose`)

## Setup

- `git merge feat/m2-5-compose --no-edit` — clean merge, no conflicts (50 files,
  +4952/-36). Confirmed `packages/shared/src/mail-transforms.ts` has
  `buildInstantIntro` and `packages/shared/src/types.ts` has `SendSuggestion`
  (both from `58f280c`); `suggestSendTime` itself lives server-side
  (`backend/internal/service/smartsend.go`'s `suggestFromHistogram`) and is
  exposed to the web client as `ApiClient#getSendSuggestion` /
  `GET /v1/mail/send-suggestion`.
- `bun install` — 1145 packages (fresh worktree, no prior `node_modules`).

## Files changed

- `apps/web/lib/use-mail.ts` — added `useSendSuggestion(email)`: a
  React Query hook (`staleTime: 5min`, `retry: false`) that resolves to
  `SendSuggestion | null`, treating a 404 (insufficient history — the normal
  case) and any other failure identically as "quietly absent," never
  inventing a time. Falls back to `mockSendSuggestion` only in `DEMO_MODE`.
- `apps/web/lib/mail-mock.ts` — added `mockSendSuggestion(email)`: only two
  seeded demo contacts (Sofia, Daniel) have "enough history," mirroring the
  real 404-when-thin-data behavior; everyone else gets `null`. Also fixed a
  pre-existing tsc break in this file (missing `reactions: []` on mock
  `Message`s, required by the M2.5 shared-types merge) since it's in my
  exclusive ownership.
- `apps/web/components/app/compose.tsx` (my exclusive file):
  - **Signature auto-apply**: a ref-tracked plain-text signature block
    (`\n\n--\n` + `htmlToText(signatureHtml)`) is appended/swapped on
    account selection/switch via a `useEffect` keyed on
    `fromAccount?.signatureHtml`. `buildBodyHtml()` strips that placeholder
    at send/draft time and appends the real rich `signatureHtml` instead
    (`<p>--</p>${signatureHtml}`), used in both `ensureDraftId` and
    `handleSend`.
  - **Smart Send nudge**: `to[0]?.email` debounced 500ms into
    `useSendSuggestion`; a dismissible chip next to Send Later reads "Best
    time: {time} — their {morning|afternoon|evening}" (bucketed via new
    `localTimeBucket` helper) when `confidence >= 0.3` and no `scheduledAt`
    is set; click sets `scheduledAt`.
- `apps/web/components/app/command-palette.tsx` — small, self-contained
  "Instant Intro" entry in the Mail group (icon + one `CommandItem`, ~35
  lines). Given the ownership boundary forbids touching `thread-view.tsx` /
  the mail page, this reads the open thread itself: `useSearchParams().get('t')`
  gated on `pathname === '/mail'`, feeds `useThreadDetail` (existing,
  unmodified hook) and `useSelfEmails()` (existing, unmodified), calls
  `buildInstantIntro` and either `openCompose(...)` or a "doesn't look like
  an intro thread" toast. No thread-view keyboard shortcut was added (that
  half of the brief belongs to whichever task owns `thread-view.tsx`).
- `apps/web/components/app/compose.test.tsx` — fixed the `ACCOUNT` fixture
  (missing `signatureHtml`/`autoBcc`, a pre-existing tsc break from the
  shared-types merge) and added two new describe blocks (13 tests): account
  signature apply/swap/absent, rich-HTML-at-send, nudge debounce, chip
  render/click/dismiss, and confidence-below-0.3 / null (404) silence.
- `apps/web/components/app/command-palette.test.tsx` — added mocks for
  `next/navigation`'s `useSearchParams`, `@/lib/use-mail`'s `useThreadDetail`,
  `@/lib/use-identity`'s `useSelfEmails`, extended the `sonner` mock with
  `toast.error`, and a new "Instant Intro" describe block (4 tests): hidden
  off-thread, hidden off `/mail` even with a stray thread id, dispatches a
  prefilled compose, toasts on a non-intro thread.

## A real bug caught by TDD

The first draft of the signature-swap logic set
`appliedSignatureRef.current = nextBlock` *after* calling `setBody(prev => ...)`.
The updater closure read `appliedSignatureRef.current` for "what to strip,"
but by the time React actually invoked that updater, the ref had already been
overwritten to `nextBlock` — so the strip check compared the body against the
*new* block instead of the *old* one, silently stacking both signatures on
every account switch. The "swaps the previous signature" test caught this
immediately; fixed by capturing `prevBlock` in a local `const` before mutating
the ref.

## Honesty-policy notes

- `useSendSuggestion` never fabricates a time: 404 and any other failure both
  resolve to `null`; the chip only renders on a real, resolved suggestion
  with `confidence >= 0.3`.
- Signature text comes from the real `signatureHtml` on the selected
  account (whatever the backend/settings surface has stored — none of it is
  invented here); the sent body uses that same rich value, not a
  reconstruction.
- Instant Intro only opens a prefilled compose when `buildInstantIntro`
  (pure, already-tested shared transform) returns non-null; otherwise a toast
  says so plainly instead of composing a bad draft.

## Verification (explicit exit codes)

- `cd apps/web && bunx tsc --noEmit` — exit 2, but **zero errors in any file
  this task touches**. All remaining errors are pre-existing (confirmed via
  `git diff --stat feat/m2-5-compose -- <file>`) in files outside this task's
  ownership boundary (`lib/settings-mock.ts`, `lib/calendar-accounts.test.ts`,
  `components/app/calendar/conflict-warning.test.tsx`,
  `components/app/event-dialog.test.tsx`, `components/app/thread-view.test.tsx`)
  — the same M2.5 shared-types fallout (`ConnectedAccount` gained required
  `signatureHtml`/`autoBcc`; `Message` gained required `reactions`) that
  other parallel tasks own.
- `cd apps/web && bunx vitest run` — **53 files / 719 tests passed**, exit 0
  (91 of those in `compose.test.tsx` + `command-palette.test.tsx`, all new or
  touched by this task). One `components/public/booking-page.test.tsx` test
  flaked once mid-session on a `waitFor` timeout; reran 4x in isolation both
  with and without this diff — passes every time either way, confirmed
  unrelated (that file isn't imported by, and doesn't import, anything this
  task touches).
- `bunx biome check <6 touched files>` — exit 0. Fixed the one lint finding
  introduced by my new code (unused `err` binding in `useSendSuggestion`'s
  catch). The remaining 7 warnings biome reports in `compose.tsx`/
  `compose.test.tsx` (autofocus, static-element-interaction, missing effect
  deps in a pre-existing test harness) are pre-existing, confirmed via
  `git diff feat/m2-5-compose -- apps/web/components/app/compose.tsx`.

## Ownership-boundary compliance

- Did not touch `thread-view.tsx`, the mail page, settings, or any
  `ConnectedAccount`/`Message` literal owned by other parallel tasks.
- `command-palette.tsx` gets one small, distinct hunk (new imports + one
  `CommandItem` + local state), as explicitly permitted by the brief's
  "(a palette entry for Instant Intro is OK if briefed — small distinct
  hunk)" carve-out.
- `use-mail.ts` gets one new exported hook (`useSendSuggestion`) plus its
  import wiring — no changes to any existing hook.

## Concerns

- The brief also describes a thread-view keyboard shortcut for Instant
  Intro; per the explicit ownership boundary ("Do NOT touch ... thread
  view") this was intentionally not implemented here. The command-palette
  entry covers the same user-facing action end-to-end (visible only on an
  open thread, builds and opens the same prefilled compose), so the feature
  is reachable, just not bound to a bare keystroke from `thread-view.tsx`.
- Whole-repo `tsc`/`biome` won't be fully green until the other parallel
  tasks land their own fixes to the shared-types fallout listed above — not
  a regression from this commit.
