# Task 14 Report — backend/internal/ics (minimal RFC 5545 parser, stdlib-only)

## Status: COMPLETE

Branch: `worktree-task-14-ics` (worktree `.claude/worktrees/task-14-ics`, based on main; `git merge feat/m2-8-integrations` reported already up to date — main contains the integrations merge; `backend/internal/domain/team.go` verified present).

Note: this agent was killed twice mid-task (spend limit, stream stall). No prior worktree existed on reorientation, so the task was executed cleanly from scratch in a fresh isolated worktree.

## Produces (matches brief verbatim)

- `Calendar{Name, Events}` / `Event{UID, Summary, Description, Location, Start, End, AllDay, RRule, Status, LastModified}`
- `func Parse(r io.Reader) (Calendar, error)` — tolerant; malformed VEVENTs dropped with UID via `errors.Join`
- `func Expand(ev Event, from, to time.Time) []Event` — bounded (1000-occurrence safety valve), DAILY/WEEKLY/MONTHLY/YEARLY with INTERVAL/COUNT/UNTIL and weekly BYDAY

## Files

- `backend/internal/ics/ics.go` — lexer: unfolding (linear-time even for pathological folds), content-line parsing (quote-aware colon/semicolon split), TEXT unescaping. Imports `_ "time/tzdata"` (stdlib) so TZID resolution works on hosts without system zoneinfo.
- `backend/internal/ics/parse.go` — component stack (VALARM/VTIMEZONE props cannot leak into events), DATE/DATE-TIME with TZID→LoadLocation and UTC fallback, DTEND→DURATION→AllDay+1d end resolution, ISO-8601 duration subset.
- `backend/internal/ics/rrule.go` — tolerant RRULE parse (INTERVAL clamped ≥1, bad COUNT/UNTIL/BYDAY tokens skipped, unsupported FREQ degrades to non-recurring), monthly/yearly skip normalized overflow days (Jan 31 → no Feb), UNTIL compared in UTC inclusive of equal starts, COUNT consumed from DTSTART inclusive, 200k iteration guard.
- Tests: `ics_test.go`, `parse_test.go`, `rrule_test.go` — table-driven; fixtures `testdata/{google,outlook,holidays,folded,weekly,malformed}.ics` (Google export w/ TZID+escapes+DURATION, Outlook w/ VTIMEZONE+VALARM+unknown Windows TZID, all-day holidays w/ yearly RRULE + UTF-8, folded lines, weekly BYDAY+UNTIL, malformed feed). Hostile-input battery: binary garbage, truncated files/lines, unterminated quotes, orphan/huge folds (20k parts), 1MB lines, BEGIN floods, nested VEVENTs, bad escapes, hostile RRULEs (INTERVAL=0/negative/1e9, garbage parts) — no panics, no hangs.

## Verification (explicit exit codes)

- `go test ./internal/ics/...` — 95 passed, exit 0 (red-first confirmed before implementation)
- `gofmt -l internal/ics` — clean, exit 0; `go vet ./internal/ics/...` — exit 0; `golangci-lint run ./internal/ics/...` — "No issues found", exit 0
- Full `go build ./... && go test ./...` — 2,190 passed in 19 packages, exit 0

## Constraints honored

Pure stdlib (only `time/tzdata` blank import, which is stdlib). No network, no migration, no service wiring, no files outside `backend/internal/ics/**` (plus this report). Merge into feat/m2-8-integrations will be trivially clean.

## Concerns

- Floating (no-TZID, no-Z) DATE-TIMEs are treated as UTC — acceptable for public feeds; Task 15 can revisit if a feed-local default zone is ever wanted.
- Weekly BYDAY expansion assumes DTSTART's weekday is in the BYDAY set (true for real feeds); a DTSTART outside the set is simply not emitted rather than force-included per strict RFC reading.
- The 1000-occurrence cap applies to *returned* occurrences; pre-window occurrences still consume COUNT correctly.
- This file previously held a stale M2.5 Task 14 report; overwritten intentionally per brief (worktree copy).
