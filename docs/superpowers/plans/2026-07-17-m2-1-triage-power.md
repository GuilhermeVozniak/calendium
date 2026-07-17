# M2.1 — Triage Power & Speed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

**Goal**

Ship every M2.1 triage feature on live data: auto-advance, bulk triage (range select + bulk archive/read/label), Get Me To Zero, one-click + bulk unsubscribe (RFC 2369 / RFC 8058), fully keyboard-driven stars/labels (`l` picker), reorderable splits (server-persisted prefs), undo-anything (`Z`), inbox-zero celebration, shortcut-teaching toasts, and a measured sub-100ms interaction budget enforced in the CI Playwright suite.

**Architecture**

Backend work lands first: one migration (unsubscribe columns on `threads` + a new `user_prefs` table), List-Unsubscribe parsing at sync ingest, and six new REST v1 surfaces (`bulk-actions`, `zero`, `labels`, `unsubscribe`, `DELETE snooze`, `prefs`) added to the hexagon as `port.MailService`/`port.PrefsService` methods with provider write-through, plus one new outbound adapter (`internal/adapter/out/unsubscribe`) for RFC 8058 one-click POSTs. The shared package gains the mirrored `ApiClient` methods plus a framework-agnostic triage core (range-selection model + undo stack) consumed by web, desktop, and mobile; the web app wires it into `use-mail.ts` optimistic mutations and the mail page keyboard layer, with demo fallbacks only behind `DEMO_MODE`.

**Tech Stack**

- Backend: Go stdlib only (`net/http` ServeMux routing, `database/sql`), hexagonal (`domain` → `port` → `service`, adapters in `internal/adapter/{in/httpapi,out/*}`), Postgres migrations in `backend/migrations/`, testcontainers for repo tests.
- Shared: TypeScript, `packages/shared` (`ApiClient` in `src/client.ts`, types in `src/types.ts`), Vitest (node env).
- Web: Next.js 15 App Router, shadcn new-york, TanStack Query, sonner, Vitest + Testing Library (jsdom), Playwright e2e in demo mode (`apps/web/e2e`).
- Desktop: Wails v2 + React (`apps/desktop/frontend`), Vitest. Mobile: Expo RN (`apps/mobile`), Jest (jest-expo).

**Global Constraints**

- **Mocks only behind demo flags.** Web mock data lives in `apps/web/lib/*-mock.ts` and is reachable ONLY when `DEMO_MODE` (`lib/demo.ts`) is true; desktop/mobile follow their existing demo-fallback contracts. Outside demo mode, failures surface as real error states — never fabricated success.
- **Gates.** Every commit must pass `bun run lint:js` (Biome), `bun run lint:go` (golangci-lint, both Go modules), and the touched workspace's test suite; pre-push (lefthook) runs `test:api`, `test:shared`, `test:web`, `test:desktop`, `test:mobile` — keep them all green. `test:api` postgres repo tests need a running Docker daemon (they skip without it; run them with Docker up before committing repo changes).
- **Conventional commits** (`feat(backend): …`, `test(web): …` etc.), one commit per task, from the repo root `/Users/guilherme/Dev/pessoal/calendium`.
- **Backend stays stdlib-only hexagonal**: no new Go dependencies; business logic in `internal/service`, HTTP translation only in `internal/adapter/in/httpapi`, SQL only in `internal/adapter/out/postgres`; new driven surfaces get `port` interfaces + fakes in `internal/service/fakes_test.go` and `internal/adapter/in/httpapi/harness_test.go`.
- **Interface ripple rule**: every task that grows `port.MailService` must add matching stub methods to `fakeMailService` in `backend/internal/adapter/in/httpapi/harness_test.go` in the same task, or `test:api` will not compile.

---

### Task 1: Migration 0005 + unsubscribe fields persisted on threads

**Files:**
- Create: `backend/migrations/0005_triage_power.sql`
- Modify: `backend/internal/domain/mail.go` (Thread struct), `backend/internal/adapter/out/postgres/mail.go` (threadCols, scanThread, Upsert, Update)
- Test: Create `backend/internal/adapter/out/postgres/thread_triage_test.go`

**Interfaces:**
- Consumes: `newTestStore(t *testing.T) (*Store, *sql.DB)` (existing harness in `postgres_test.go`), `port.ThreadRepo.Upsert/GetByID/Update`.
- Produces: `domain.Thread` gains `UnsubscribeMailto *string`, `UnsubscribeURL *string`, `UnsubscribeOneClick bool` (JSON: `unsubscribeMailto`, `unsubscribeUrl`, `unsubscribeOneClick`); columns `threads.unsubscribe_mailto text`, `threads.unsubscribe_url text`, `threads.unsubscribe_one_click boolean NOT NULL DEFAULT false`; table `user_prefs`.

**Steps:**

- [ ] Write the failing repo test `backend/internal/adapter/out/postgres/thread_triage_test.go`:

```go
package postgres

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestThreadPersistsUnsubscribeFields(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Users().Upsert(ctx, domain.User{ID: "u1", Email: "u1@example.com"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := store.Accounts().Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "u1@example.com",
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	mailto := "mailto:unsub@news.example"
	link := "https://news.example/unsub?u=1"
	saved, err := store.Threads().Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", ProviderThreadID: "pt1",
		LastMessageAt:       time.Now().UTC(),
		UnsubscribeMailto:   &mailto,
		UnsubscribeURL:      &link,
		UnsubscribeOneClick: true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Threads().GetByID(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UnsubscribeMailto == nil || *got.UnsubscribeMailto != mailto {
		t.Fatalf("UnsubscribeMailto = %v, want %q", got.UnsubscribeMailto, mailto)
	}
	if got.UnsubscribeURL == nil || *got.UnsubscribeURL != link {
		t.Fatalf("UnsubscribeURL = %v, want %q", got.UnsubscribeURL, link)
	}
	if !got.UnsubscribeOneClick {
		t.Fatal("UnsubscribeOneClick = false, want true")
	}

	// Update clears them (sender stopped offering unsubscribe).
	got.UnsubscribeMailto, got.UnsubscribeURL, got.UnsubscribeOneClick = nil, nil, false
	if err := store.Threads().Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := store.Threads().GetByID(ctx, saved.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got2.UnsubscribeMailto != nil || got2.UnsubscribeURL != nil || got2.UnsubscribeOneClick {
		t.Fatalf("unsubscribe fields not cleared: %+v", got2)
	}
}
```

- [ ] Run to verify it fails: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/out/postgres/ -run TestThreadPersistsUnsubscribeFields` — expected: compile error `unknown field UnsubscribeMailto in struct literal of type domain.Thread`.
- [ ] Implement. Create `backend/migrations/0005_triage_power.sql`:

```sql
-- M2.1 triage power: unsubscribe targets on threads + per-user preferences.
--
-- unsubscribe_* mirror the newest message's List-Unsubscribe (RFC 2369) and
-- List-Unsubscribe-Post (RFC 8058) headers, parsed at sync ingest, so the
-- clients can offer one-click / mailto / link unsubscribe without refetching
-- raw headers. All empty/false when the sender offers no unsubscribe.
ALTER TABLE threads ADD COLUMN unsubscribe_mailto    text;
ALTER TABLE threads ADD COLUMN unsubscribe_url       text;
ALTER TABLE threads ADD COLUMN unsubscribe_one_click boolean NOT NULL DEFAULT false;

-- Per-user client preferences shared across devices. split_order is a jsonb
-- array of inbox-split names ('important', 'vip', ...); '[]' = default order.
CREATE TABLE user_prefs (
    user_id     text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    split_order jsonb NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Backs GET-ME-TO-ZERO's "oldest inbox threads before cutoff" scan.
CREATE INDEX threads_zero_idx ON threads (account_id, last_message_at ASC)
    WHERE in_inbox;
```

- [ ] Add the three fields to `Thread` in `backend/internal/domain/mail.go`, after `RemindAt`:

```go
	// UnsubscribeMailto / UnsubscribeURL / UnsubscribeOneClick are parsed from
	// the newest message's List-Unsubscribe / List-Unsubscribe-Post headers at
	// sync ingest (RFC 2369 / RFC 8058). All zero when the sender offers no
	// unsubscribe. OneClick means the URL accepts the RFC 8058 POST.
	UnsubscribeMailto   *string `json:"unsubscribeMailto"`
	UnsubscribeURL      *string `json:"unsubscribeUrl"`
	UnsubscribeOneClick bool    `json:"unsubscribeOneClick"`
```

- [ ] Persist them in `backend/internal/adapter/out/postgres/mail.go`:
  - Append to the `threadCols` constant: `, t.unsubscribe_mailto, t.unsubscribe_url, t.unsubscribe_one_click` (after the existing `remind_at`-adjacent columns, before the label-ids subselect if the constant orders it last — keep scan order in sync).
  - In `scanThread`, add `var unsubMailto, unsubURL sql.NullString` scanned in the matching position and map to pointers (`if unsubMailto.Valid { t.UnsubscribeMailto = &unsubMailto.String }`, same for URL), plus scan `&t.UnsubscribeOneClick` directly.
  - In `Upsert`, extend the INSERT to:

```go
		INSERT INTO threads (id, account_id, provider_thread_id, subject, snippet, participants,
			split, message_count, unread, starred, in_inbox, last_message_at, opened_at, snoozed_until, remind_at,
			unsubscribe_mailto, unsubscribe_url, unsubscribe_one_click)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
```

  and add to the `DO UPDATE SET` list: `unsubscribe_mailto = EXCLUDED.unsubscribe_mailto, unsubscribe_url = EXCLUDED.unsubscribe_url, unsubscribe_one_click = EXCLUDED.unsubscribe_one_click`, appending `t.UnsubscribeMailto, t.UnsubscribeURL, t.UnsubscribeOneClick` to the args slice.
  - In `Update`, add the three columns to the SET list using the next three placeholders after the current last data placeholder, appending the same three args.
- [ ] Run to verify pass (Docker running): `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/out/postgres/ -run TestThreadPersistsUnsubscribeFields` — expected: `ok  calendium/backend/internal/adapter/out/postgres`.
- [ ] Run the full backend suite to catch scan-order regressions: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./...` — expected: all `ok`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): persist unsubscribe targets on threads + user_prefs table (migration 0005)"`

---

### Task 2: Parse List-Unsubscribe headers at sync ingest

**Files:**
- Create: `backend/internal/service/unsubscribe.go`
- Modify: `backend/internal/service/sync.go` (`applyMailPage` steps 2–3)
- Test: Create `backend/internal/service/unsubscribe_test.go`

**Interfaces:**
- Consumes: `port.IncomingMessage.Headers map[string]string` (canonical MIME keys), existing `applyMailPage` newest-message loop.
- Produces:

```go
type unsubscribeInfo struct {
	Mailto   string
	URL      string
	OneClick bool
}
func parseListUnsubscribe(headers map[string]string) unsubscribeInfo
func optionalString(s string) *string // nil when empty
```

**Steps:**

- [ ] Write the failing tests `backend/internal/service/unsubscribe_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestParseListUnsubscribe(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    unsubscribeInfo
	}{
		{"no header", map[string]string{}, unsubscribeInfo{}},
		{
			"mailto only",
			map[string]string{"List-Unsubscribe": "<mailto:unsub@news.example>"},
			unsubscribeInfo{Mailto: "mailto:unsub@news.example"},
		},
		{
			"https only, no post header",
			map[string]string{"List-Unsubscribe": "<https://news.example/u?id=1>"},
			unsubscribeInfo{URL: "https://news.example/u?id=1"},
		},
		{
			"both uris, rfc 8058 one-click",
			map[string]string{
				"List-Unsubscribe":      "<mailto:unsub@news.example>, <https://news.example/u?id=1>",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
			unsubscribeInfo{
				Mailto:   "mailto:unsub@news.example",
				URL:      "https://news.example/u?id=1",
				OneClick: true,
			},
		},
		{
			"post header without url is not one-click",
			map[string]string{
				"List-Unsubscribe":      "<mailto:unsub@news.example>",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
			unsubscribeInfo{Mailto: "mailto:unsub@news.example"},
		},
		{
			"lowercase header keys still match",
			map[string]string{
				"list-unsubscribe":      "<https://news.example/u>",
				"list-unsubscribe-post": "list-unsubscribe=one-click",
			},
			unsubscribeInfo{URL: "https://news.example/u", OneClick: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseListUnsubscribe(tt.headers); got != tt.want {
				t.Fatalf("parseListUnsubscribe() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSyncStampsUnsubscribeOnThread(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle,
		Email: "owner@acme.com", Status: domain.AccountActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "tok", RefreshToken: "r", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Weekly digest", LastMessageAt: now, InInbox: true}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm1", ThreadID: "pt1",
				From:   domain.EmailAddress{Email: "digest@news.example"},
				To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
				SentAt: now,
			},
			Headers: map[string]string{
				"List-Unsubscribe":      "<mailto:unsub@news.example>, <https://news.example/u?id=1>",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
		}},
	}

	threads := newThreadRepo()
	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: newLabelRepo(), Threads: threads,
		Messages: newMessageRepo(), SyncState: newSyncStateRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})
	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}

	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:unsub@news.example" {
		t.Fatalf("UnsubscribeMailto = %v, want mailto:unsub@news.example", th.UnsubscribeMailto)
	}
	if th.UnsubscribeURL == nil || *th.UnsubscribeURL != "https://news.example/u?id=1" {
		t.Fatalf("UnsubscribeURL = %v", th.UnsubscribeURL)
	}
	if !th.UnsubscribeOneClick {
		t.Fatal("UnsubscribeOneClick = false, want true")
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run 'TestParseListUnsubscribe|TestSyncStampsUnsubscribeOnThread'` — expected: compile error `undefined: unsubscribeInfo`.
- [ ] Implement `backend/internal/service/unsubscribe.go`:

```go
package service

import "strings"

// unsubscribeInfo carries the parsed List-Unsubscribe targets of one message.
type unsubscribeInfo struct {
	Mailto   string
	URL      string
	OneClick bool
}

// parseListUnsubscribe extracts unsubscribe targets from the RFC 2369
// List-Unsubscribe header (comma-separated, angle-bracketed mailto:/http(s):
// URIs; first of each kind wins) and flags RFC 8058 one-click support when
// List-Unsubscribe-Post declares "List-Unsubscribe=One-Click". Header keys
// and values are matched case-insensitively; one-click requires an HTTP URL.
func parseListUnsubscribe(headers map[string]string) unsubscribeInfo {
	var info unsubscribeInfo
	var raw, post string
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "list-unsubscribe":
			raw = v
		case "list-unsubscribe-post":
			post = v
		}
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(strings.TrimPrefix(part, "<"), ">")
		lower := strings.ToLower(part)
		switch {
		case strings.HasPrefix(lower, "mailto:") && info.Mailto == "":
			info.Mailto = part
		case (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) && info.URL == "":
			info.URL = part
		}
	}
	info.OneClick = info.URL != "" &&
		strings.EqualFold(strings.TrimSpace(post), "List-Unsubscribe=One-Click")
	return info
}

// optionalString returns nil for "" so empty parses stay NULL in the mirror.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
```

- [ ] Wire it into `applyMailPage` in `backend/internal/service/sync.go`. In step 2, alongside `splitByThread`, track the newest message's unsubscribe info:

```go
	splitByThread := map[string]domain.InboxSplit{}
	unsubByThread := map[string]unsubscribeInfo{}
	newestByThread := map[string]time.Time{}
	for _, im := range page.Messages {
		providerThreadID := im.Message.ThreadID // provider id at this boundary
		if last, seen := newestByThread[providerThreadID]; !seen || im.Message.SentAt.After(last) {
			newestByThread[providerThreadID] = im.Message.SentAt
			splitByThread[providerThreadID] = ClassifySplit(im, acct.Email, vips)
			unsubByThread[providerThreadID] = parseListUnsubscribe(im.Headers)
		}
	}
```

  In step 3, inside `case err == nil:` (existing-thread branch), preserve local state next to the `OpenedAt` copy:

```go
			t.UnsubscribeMailto = existing.UnsubscribeMailto
			t.UnsubscribeURL = existing.UnsubscribeURL
			t.UnsubscribeOneClick = existing.UnsubscribeOneClick
```

  and after the `switch` (before the label mapping), stamp fresh info when this page carried a newer message:

```go
		if info, ok := unsubByThread[t.ProviderThreadID]; ok {
			t.UnsubscribeMailto = optionalString(info.Mailto)
			t.UnsubscribeURL = optionalString(info.URL)
			t.UnsubscribeOneClick = info.OneClick
		}
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/` — expected: `ok  calendium/backend/internal/service`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): parse List-Unsubscribe headers at sync ingest and stamp threads"`

---

### Task 3: Unsnooze endpoint (DELETE /v1/mail/threads/{id}/snooze)

Needed so `Z` can undo a snooze client-side.

**Files:**
- Modify: `backend/internal/port/driving.go` (MailService), `backend/internal/service/mail.go`, `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/httpapi.go`, `backend/internal/adapter/in/httpapi/harness_test.go` (fakeMailService stub)
- Test: Modify `backend/internal/service/mail_test.go`

**Interfaces:**
- Produces: `port.MailService` gains `UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error)`; route `DELETE /v1/mail/threads/{id}/snooze` → 200 with the updated Thread JSON.

**Steps:**

- [ ] Write the failing service test (append to `backend/internal/service/mail_test.go`):

```go
func TestUnsnoozeThreadClearsSnoozeWithoutUnread(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	until := f.clock.Now().Add(4 * time.Hour)
	f.seedThread(t, "t1", "a1", func(th *domain.Thread) {
		th.SnoozedUntil = &until
		th.Unread = false
	})

	got, err := f.svc.UnsnoozeThread(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("UnsnoozeThread: %v", err)
	}
	if got.SnoozedUntil != nil {
		t.Fatalf("SnoozedUntil = %v, want nil", got.SnoozedUntil)
	}
	if got.Unread {
		t.Fatal("Unread flipped to true; unsnooze must not fake a wake-up")
	}

	// Foreign user gets 404 semantics.
	if _, err := f.svc.UnsnoozeThread(ctx, "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign unsnooze err = %v, want ErrNotFound", err)
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestUnsnoozeThreadClearsSnoozeWithoutUnread` — expected: compile error `f.svc.UnsnoozeThread undefined`.
- [ ] Implement. Add to `port.MailService` (in `driving.go`, right under `SnoozeThread`):

```go
	// UnsnoozeThread clears a pending snooze (client-side undo of snooze)
	// without marking the thread unread.
	UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error)
```

  Add to `backend/internal/service/mail.go` (after `SnoozeThread`):

```go
func (s *MailService) UnsnoozeThread(ctx context.Context, userID, threadID string) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	t.SnoozedUntil = nil
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	return t, nil
}
```

  Add the handler to `backend/internal/adapter/in/httpapi/mail.go`:

```go
func (s *server) handleUnsnoozeThread(w http.ResponseWriter, r *http.Request) {
	thread, err := s.deps.Mail.UnsnoozeThread(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}
```

  Register in `httpapi.go` next to the snooze route: `authed("DELETE /v1/mail/threads/{id}/snooze", s.handleUnsnoozeThread)`. Add to `fakeMailService` in `harness_test.go`:

```go
func (f *fakeMailService) UnsnoozeThread(_ context.Context, userID, threadID string) (domain.Thread, error) {
	f.lastUserID, f.lastThreadID = userID, threadID
	return f.threadRet, f.err
}
```

  (Reuse the fake's existing recording/return fields; if it has none with these names, add `lastUserID, lastThreadID string`, `threadRet domain.Thread`, `err error` fields following its existing conventions.)
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ ./internal/adapter/in/httpapi/` — expected: both `ok`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): unsnooze endpoint for client-side snooze undo"`

---

### Task 4: Bulk thread actions endpoint

**Files:**
- Modify: `backend/internal/port/driving.go`, `backend/internal/service/mail.go` (extract `applyAction` from `ActOnThread`, add `BulkActOnThreads`), `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/httpapi.go`, `backend/internal/adapter/in/httpapi/harness_test.go`
- Test: Modify `backend/internal/service/mail_test.go`; create `backend/internal/adapter/in/httpapi/bulk_handlers_test.go`

**Interfaces:**
- Produces (in `port/driving.go`):

```go
// BulkActionResult reports a bulk mutation: mutated threads plus the ids
// that failed (not found / foreign / provider error) — never a partial 500.
type BulkActionResult struct {
	Threads   []domain.Thread `json:"threads"`
	FailedIDs []string        `json:"failedIds"`
}
```

  `port.MailService` gains `BulkActOnThreads(ctx context.Context, userID string, threadIDs []string, action domain.ThreadAction) (BulkActionResult, error)`. Route: `POST /v1/mail/threads/bulk-actions` with body `{"threadIds": [...], "action": "archive", "labelId": "..."}` (labelId used by Task 6). Cap: 200 ids (`maxBulkThreads`).

**Steps:**

- [ ] Write the failing service test (append to `mail_test.go`):

```go
func TestBulkActOnThreadsArchivesAndReportsFailures(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	f.seedThread(t, "t1", "a1", nil)
	f.seedThread(t, "t2", "a1", nil)

	res, err := f.svc.BulkActOnThreads(ctx, "u1", []string{"t1", "ghost", "t2"}, domain.ThreadActionArchive)
	if err != nil {
		t.Fatalf("BulkActOnThreads: %v", err)
	}
	if len(res.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2", len(res.Threads))
	}
	for _, th := range res.Threads {
		if th.InInbox {
			t.Fatalf("thread %s still InInbox after bulk archive", th.ID)
		}
	}
	if !reflect.DeepEqual(res.FailedIDs, []string{"ghost"}) {
		t.Fatalf("FailedIDs = %v, want [ghost]", res.FailedIDs)
	}
	if f.provider.modifyLabelsCalls != 2 {
		t.Fatalf("provider write-throughs = %d, want 2", f.provider.modifyLabelsCalls)
	}
}

func TestBulkActOnThreadsValidatesInput(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	if _, err := f.svc.BulkActOnThreads(ctx, "u1", nil, domain.ThreadActionArchive); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty ids err = %v, want ErrValidation", err)
	}
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = "t"
	}
	if _, err := f.svc.BulkActOnThreads(ctx, "u1", tooMany, domain.ThreadActionArchive); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("201 ids err = %v, want ErrValidation", err)
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestBulkActOnThreads` — expected: compile error `f.svc.BulkActOnThreads undefined`.
- [ ] Implement. In `service/mail.go`, refactor the body of `ActOnThread` (the `switch` + `threads.Update` + provider `ModifyLabels`) into:

```go
// applyAction mutates one owned thread locally and writes through to the
// provider. Shared by ActOnThread, BulkActOnThreads, and ArchiveOlderThan.
func (s *MailService) applyAction(ctx context.Context, t domain.Thread, acct domain.ConnectedAccount, action domain.ThreadAction) (domain.Thread, error) {
	var add, remove []string
	switch action {
	case domain.ThreadActionArchive:
		remove = []string{port.LabelKeyInbox}
		t.InInbox = false
	case domain.ThreadActionTrash:
		add, remove = []string{port.LabelKeyTrash}, []string{port.LabelKeyInbox}
		t.InInbox = false
	case domain.ThreadActionStar:
		add = []string{port.LabelKeyStarred}
		t.Starred = true
	case domain.ThreadActionUnstar:
		remove = []string{port.LabelKeyStarred}
		t.Starred = false
	case domain.ThreadActionRead:
		remove = []string{port.LabelKeyUnread}
		t.Unread = false
	case domain.ThreadActionUnread:
		add = []string{port.LabelKeyUnread}
		t.Unread = true
	case domain.ThreadActionSpam:
		add, remove = []string{port.LabelKeySpam}, []string{port.LabelKeyInbox}
		t.InInbox = false
	case domain.ThreadActionMoveToInbox:
		add, remove = []string{port.LabelKeyInbox}, []string{port.LabelKeyTrash, port.LabelKeySpam}
		t.InInbox = true
	default:
		return domain.Thread{}, fmt.Errorf("%w: unknown thread action %q", domain.ErrValidation, action)
	}
	if err := s.threads.Update(ctx, t); err != nil {
		return domain.Thread{}, err
	}
	if provider, ok := s.mail[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return t, err
		}
		if err := provider.ModifyLabels(ctx, token, t.ProviderThreadID, add, remove); err != nil {
			return t, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return t, nil
}
```

  `ActOnThread` becomes `ent.require` + `ownedThread` + `return s.applyAction(...)` (behavior unchanged — the existing `TestActOnThreadMirrorsAndWritesThrough` table must stay green). Then add:

```go
const maxBulkThreads = 200

func (s *MailService) BulkActOnThreads(ctx context.Context, userID string, threadIDs []string, action domain.ThreadAction) (port.BulkActionResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.BulkActionResult{}, err
	}
	if len(threadIDs) == 0 {
		return port.BulkActionResult{}, fmt.Errorf("%w: threadIds is required", domain.ErrValidation)
	}
	if len(threadIDs) > maxBulkThreads {
		return port.BulkActionResult{}, fmt.Errorf("%w: at most %d threads per bulk action", domain.ErrValidation, maxBulkThreads)
	}
	res := port.BulkActionResult{Threads: []domain.Thread{}, FailedIDs: []string{}}
	for _, id := range threadIDs {
		t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, id)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		updated, err := s.applyAction(ctx, t, acct, action)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		res.Threads = append(res.Threads, updated)
	}
	return res, nil
}
```

- [ ] Add the handler (in `httpapi/mail.go`) and route. The handler also dispatches label actions (wired for Task 6 — until then `BulkSetLabel` doesn't exist, so add only the default branch now and extend in Task 6):

```go
func (s *server) handleBulkThreadActions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ThreadIDs []string `json:"threadIds"`
		Action    string   `json:"action"`
		LabelID   string   `json:"labelId"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	action, err := domain.ParseThreadAction(in.Action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.deps.Mail.BulkActOnThreads(r.Context(), userFrom(r).ID, in.ThreadIDs, action)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
```

  Route: `authed("POST /v1/mail/threads/bulk-actions", s.handleBulkThreadActions)` (register BEFORE nothing special — ServeMux pattern precedence puts the literal `bulk-actions` segment above `{id}` wildcards automatically). Add the `fakeMailService` stub in `harness_test.go`:

```go
func (f *fakeMailService) BulkActOnThreads(_ context.Context, userID string, threadIDs []string, action domain.ThreadAction) (port.BulkActionResult, error) {
	f.bulkUserID, f.bulkThreadIDs, f.bulkAction = userID, threadIDs, action
	return f.bulkResult, f.err
}
```

  with new fields `bulkUserID string`, `bulkThreadIDs []string`, `bulkAction domain.ThreadAction`, `bulkResult port.BulkActionResult` on the fake.
- [ ] Write the failing handler test `backend/internal/adapter/in/httpapi/bulk_handlers_test.go` (uses only `newHarness`, `defaultToken`, stdlib):

```go
package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestBulkThreadActionsEndpoint(t *testing.T) {
	h := newHarness(t)
	h.mail.bulkResult = port.BulkActionResult{
		Threads:   []domain.Thread{{ID: "t1"}},
		FailedIDs: []string{"ghost"},
	}
	srv := httptest.NewServer(New(h.deps))
	defer srv.Close()

	body := bytes.NewBufferString(`{"threadIds":["t1","ghost"],"action":"archive"}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/mail/threads/bulk-actions", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+defaultToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var got port.BulkActionResult
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 1 || !reflect.DeepEqual(got.FailedIDs, []string{"ghost"}) {
		t.Fatalf("body = %+v", got)
	}
	if !reflect.DeepEqual(h.mail.bulkThreadIDs, []string{"t1", "ghost"}) || h.mail.bulkAction != domain.ThreadActionArchive {
		t.Fatalf("service got ids=%v action=%q", h.mail.bulkThreadIDs, h.mail.bulkAction)
	}
}
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ ./internal/adapter/in/httpapi/` — expected: both `ok` (including the pre-existing `TestActOnThreadMirrorsAndWritesThrough`).
- [ ] Commit: `git add -A && git commit -m "feat(backend): bulk thread actions endpoint with per-id failure reporting"`

---

### Task 5: Get Me To Zero endpoint

**Files:**
- Modify: `backend/internal/port/driven.go` (ThreadRepo), `backend/internal/port/driving.go` (MailService), `backend/internal/service/mail.go`, `backend/internal/service/fakes_test.go` (fakeThreadRepo), `backend/internal/adapter/out/postgres/mail.go`, `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/httpapi.go`, `backend/internal/adapter/in/httpapi/harness_test.go`
- Test: Modify `backend/internal/service/mail_test.go`, `backend/internal/adapter/out/postgres/thread_triage_test.go`

**Interfaces:**
- `port.ThreadRepo` gains:

```go
	// ListInboxBefore returns inbox threads (in_inbox, not snoozed) with
	// last_message_at strictly before the cutoff, oldest first.
	ListInboxBefore(ctx context.Context, userID string, before time.Time, limit int) ([]domain.Thread, error)
```

- `port.MailService` gains `ArchiveOlderThan(ctx context.Context, userID string, olderThan time.Time) (archived int, err error)`.
- Route: `POST /v1/mail/threads/zero` body `{"olderThan": "2026-07-10T00:00:00Z"}` → `{"archivedCount": 42}`.

**Steps:**

- [ ] Write the failing service test (append to `mail_test.go`):

```go
func TestArchiveOlderThanArchivesInBatches(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	cutoff := f.clock.Now().Add(-7 * 24 * time.Hour)
	old1 := f.seedThread(t, "t-old-1", "a1", func(th *domain.Thread) { th.LastMessageAt = cutoff.Add(-time.Hour) })
	old2 := f.seedThread(t, "t-old-2", "a1", func(th *domain.Thread) { th.LastMessageAt = cutoff.Add(-2 * time.Hour) })
	f.seedThread(t, "t-new", "a1", func(th *domain.Thread) { th.LastMessageAt = f.clock.Now() })

	// fakeThreadRepo serves ListInboxBefore from byID (implemented in this
	// task): first call returns the two old threads, after archiving both
	// leave in_inbox and the second call returns none.
	archived, err := f.svc.ArchiveOlderThan(ctx, "u1", cutoff)
	if err != nil {
		t.Fatalf("ArchiveOlderThan: %v", err)
	}
	if archived != 2 {
		t.Fatalf("archived = %d, want 2", archived)
	}
	for _, id := range []string{old1.ID, old2.ID} {
		got, err := f.threads.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.InInbox {
			t.Fatalf("thread %s still InInbox", id)
		}
	}
	gotNew, err := f.threads.GetByID(ctx, "t-new")
	if err != nil {
		t.Fatal(err)
	}
	if !gotNew.InInbox {
		t.Fatal("recent thread was archived by Get Me To Zero")
	}
	if f.provider.modifyLabelsCalls != 2 {
		t.Fatalf("provider write-throughs = %d, want 2", f.provider.modifyLabelsCalls)
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestArchiveOlderThanArchivesInBatches` — expected: compile error `f.svc.ArchiveOlderThan undefined`.
- [ ] Implement:
  - Port additions above. `fakeThreadRepo` in `fakes_test.go` implements `ListInboxBefore` for real (it drives the test): iterate `byID`, keep threads whose account belongs to the user — the fake has no account join, so filter only on `t.InInbox && t.SnoozedUntil == nil && t.LastMessageAt.Before(before)`, sort ascending by `LastMessageAt`, cap at `limit`.
  - `service/mail.go`:

```go
// ArchiveOlderThan is Get Me To Zero: archive every inbox thread older than
// the cutoff, paging until none remain. Archiving clears in_inbox, so each
// page shrinks and the loop terminates. Returns how many were archived.
func (s *MailService) ArchiveOlderThan(ctx context.Context, userID string, olderThan time.Time) (int, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return 0, err
	}
	archived := 0
	for {
		batch, err := s.threads.ListInboxBefore(ctx, userID, olderThan, maxBulkThreads)
		if err != nil {
			return archived, err
		}
		if len(batch) == 0 {
			return archived, nil
		}
		for _, t := range batch {
			acct, err := s.accounts.GetByID(ctx, t.AccountID)
			if err != nil {
				return archived, err
			}
			if _, err := s.applyAction(ctx, t, acct, domain.ThreadActionArchive); err != nil {
				return archived, err
			}
			archived++
		}
	}
}
```

  - Postgres `threadRepo.ListInboxBefore` in `postgres/mail.go`:

```go
func (r threadRepo) ListInboxBefore(ctx context.Context, userID string, before time.Time, limit int) ([]domain.Thread, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1 AND t.in_inbox AND t.snoozed_until IS NULL
		  AND t.last_message_at < $2
		ORDER BY t.last_message_at ASC, t.id LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var threads []domain.Thread
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}
```

  - Add a repo test `TestListInboxBefore` to `thread_triage_test.go` seeding three threads (one old, one snoozed-old, one new) and asserting only the old unsnoozed one returns, oldest first.
  - Handler + route + fake stub:

```go
func (s *server) handleGetMeToZero(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OlderThan time.Time `json:"olderThan"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.OlderThan.IsZero() {
		s.writeError(w, r, fmt.Errorf("%w: olderThan is required (RFC 3339)", domain.ErrValidation))
		return
	}
	count, err := s.deps.Mail.ArchiveOlderThan(r.Context(), userFrom(r).ID, in.OlderThan)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"archivedCount": count})
}
```

  Route: `authed("POST /v1/mail/threads/zero", s.handleGetMeToZero)`. Fake stub on `fakeMailService`: record `zeroOlderThan time.Time`, return `zeroCount int, err`.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./...` — expected: all `ok` (postgres package needs Docker).
- [ ] Commit: `git add -A && git commit -m "feat(backend): Get Me To Zero bulk-archive endpoint"`

---

### Task 6: Label listing + keyboard label actions (single + bulk)

**Files:**
- Modify: `backend/internal/port/driven.go` (LabelRepo), `backend/internal/port/driving.go` (MailService), `backend/internal/service/mail.go` (+ `MailServiceDeps.Labels`), `backend/internal/service/fakes_test.go` (fakeLabelRepo), `backend/internal/adapter/out/postgres/mail.go` (labelRepo), `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/httpapi.go`, `backend/internal/adapter/in/httpapi/harness_test.go`, `backend/cmd/api/main.go` (wire `Labels: store.Labels()`)
- Test: Modify `backend/internal/service/mail_test.go`, `backend/internal/adapter/out/postgres/snippet_label_test.go`

**Interfaces:**
- `port.LabelRepo` gains `GetByID(ctx context.Context, id string) (domain.Label, error)` and `ListByUser(ctx context.Context, userID string) ([]domain.Label, error)`.
- `port.MailService` gains:

```go
	ListLabels(ctx context.Context, userID string) ([]domain.Label, error)
	// SetThreadLabel adds (add=true) or removes a user/system label on a
	// thread, writing through to the provider with the label's provider id.
	SetThreadLabel(ctx context.Context, userID, threadID, labelID string, add bool) (domain.Thread, error)
	BulkSetLabel(ctx context.Context, userID string, threadIDs []string, labelID string, add bool) (BulkActionResult, error)
```

- Routes: `GET /v1/mail/labels` → `[]Label`; `POST /v1/mail/threads/{id}/labels` body `{"labelId": "...", "add": true}` → Thread. The bulk handler from Task 4 gains a `label`/`unlabel` dispatch (with `labelId`).
- `MailServiceDeps` gains `Labels port.LabelRepo`.

**Steps:**

- [ ] Write the failing service tests (append to `mail_test.go`; also add `labels: newLabelRepo()` to `mailFixture` + `MailServiceDeps` in `newMailFixture`):

```go
func TestSetThreadLabelAddsRemovesAndWritesThrough(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	f.seedThread(t, "t1", "a1", nil)
	if _, err := f.labels.Upsert(ctx, domain.Label{
		ID: "lbl1", AccountID: "a1", ProviderLabelID: "PL_1", Name: "Follow up", Kind: domain.LabelKindUser,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.SetThreadLabel(ctx, "u1", "t1", "lbl1", true)
	if err != nil {
		t.Fatalf("SetThreadLabel(add): %v", err)
	}
	if !reflect.DeepEqual(got.LabelIDs, []string{"lbl1"}) {
		t.Fatalf("LabelIDs = %v, want [lbl1]", got.LabelIDs)
	}
	if !reflect.DeepEqual(f.provider.lastModifyAdd, []string{"PL_1"}) || f.provider.lastModifyRemove != nil {
		t.Fatalf("provider add=%v remove=%v, want add=[PL_1]", f.provider.lastModifyAdd, f.provider.lastModifyRemove)
	}

	got, err = f.svc.SetThreadLabel(ctx, "u1", "t1", "lbl1", false)
	if err != nil {
		t.Fatalf("SetThreadLabel(remove): %v", err)
	}
	if len(got.LabelIDs) != 0 {
		t.Fatalf("LabelIDs = %v, want empty", got.LabelIDs)
	}
	if !reflect.DeepEqual(f.provider.lastModifyRemove, []string{"PL_1"}) {
		t.Fatalf("provider remove = %v, want [PL_1]", f.provider.lastModifyRemove)
	}
}

func TestSetThreadLabelRejectsCrossAccountLabel(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	f.seedAccount(t, "a2", "u2")
	f.seedThread(t, "t1", "a1", nil)
	if _, err := f.labels.Upsert(ctx, domain.Label{ID: "lbl2", AccountID: "a2", ProviderLabelID: "PL_2", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetThreadLabel(ctx, "u1", "t1", "lbl2", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-account label err = %v, want ErrNotFound", err)
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestSetThreadLabel` — expected: compile error (`f.labels` / `SetThreadLabel` undefined).
- [ ] Implement:
  - `fakeLabelRepo` gains `GetByID` (map lookup, `domain.ErrNotFound` when absent) and `ListByUser` (needs account ownership — give the fake an `accounts *fakeAccountRepo` field wired in `newMailFixture`, filtering labels whose account belongs to the user; keep `newLabelRepo()` zero-arg for existing callers by adding a setter field assignment in the fixture).
  - Postgres `labelRepo`:

```go
func (r labelRepo) GetByID(ctx context.Context, id string) (domain.Label, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+labelCols+` FROM labels WHERE id = $1`, id)
	return scanLabel(row)
}

func (r labelRepo) ListByUser(ctx context.Context, userID string) ([]domain.Label, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+labelCols+` FROM labels
		 WHERE account_id IN (SELECT id FROM connected_accounts WHERE user_id = $1)
		 ORDER BY name`, userID)
	// iterate exactly like ListByAccount
```

  (match `scanLabel`'s existing ErrNotFound mapping for no rows; add a `TestLabelListByUserAndGetByID` case to `snippet_label_test.go` seeding two users' accounts and asserting scoping).
  - `service/mail.go`: add `labels port.LabelRepo` field (from `MailServiceDeps.Labels`), then:

```go
func (s *MailService) ListLabels(ctx context.Context, userID string) ([]domain.Label, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	ls, err := s.labels.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ls == nil {
		ls = []domain.Label{}
	}
	return ls, nil
}

func (s *MailService) SetThreadLabel(ctx context.Context, userID, threadID, labelID string, add bool) (domain.Thread, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Thread{}, err
	}
	t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return domain.Thread{}, err
	}
	return s.setLabelOwned(ctx, t, acct, labelID, add)
}

// setLabelOwned applies a label mutation to an already-ownership-checked
// thread; shared by SetThreadLabel and BulkSetLabel.
func (s *MailService) setLabelOwned(ctx context.Context, t domain.Thread, acct domain.ConnectedAccount, labelID string, add bool) (domain.Thread, error) {
	label, err := s.labels.GetByID(ctx, labelID)
	if err != nil {
		return domain.Thread{}, err
	}
	if label.AccountID != t.AccountID {
		return domain.Thread{}, domain.ErrNotFound // labels never cross accounts
	}
	next := make([]string, 0, len(t.LabelIDs)+1)
	for _, id := range t.LabelIDs {
		if id != labelID {
			next = append(next, id)
		}
	}
	if add {
		next = append(next, labelID)
	}
	t.LabelIDs = next
	if err := s.threads.SetLabels(ctx, t.ID, next); err != nil {
		return domain.Thread{}, err
	}
	if provider, ok := s.mail[acct.Provider]; ok {
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return t, err
		}
		var addKeys, removeKeys []string
		if add {
			addKeys = []string{label.ProviderLabelID}
		} else {
			removeKeys = []string{label.ProviderLabelID}
		}
		if err := provider.ModifyLabels(ctx, token, t.ProviderThreadID, addKeys, removeKeys); err != nil {
			return t, fmt.Errorf("provider write-through failed: %w", err)
		}
	}
	return t, nil
}

func (s *MailService) BulkSetLabel(ctx context.Context, userID string, threadIDs []string, labelID string, add bool) (port.BulkActionResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.BulkActionResult{}, err
	}
	if len(threadIDs) == 0 || len(threadIDs) > maxBulkThreads {
		return port.BulkActionResult{}, fmt.Errorf("%w: between 1 and %d threadIds required", domain.ErrValidation, maxBulkThreads)
	}
	res := port.BulkActionResult{Threads: []domain.Thread{}, FailedIDs: []string{}}
	for _, id := range threadIDs {
		t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, id)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		updated, err := s.setLabelOwned(ctx, t, acct, labelID, add)
		if err != nil {
			res.FailedIDs = append(res.FailedIDs, id)
			continue
		}
		res.Threads = append(res.Threads, updated)
	}
	return res, nil
}
```

  - Handlers + routes (`httpapi/mail.go`, `httpapi.go`):

```go
func (s *server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.deps.Mail.ListLabels(r.Context(), userFrom(r).ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, labels)
}

func (s *server) handleSetThreadLabel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LabelID string `json:"labelId"`
		Add     bool   `json:"add"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.LabelID == "" {
		s.writeError(w, r, fmt.Errorf("%w: labelId is required", domain.ErrValidation))
		return
	}
	thread, err := s.deps.Mail.SetThreadLabel(r.Context(), userFrom(r).ID, r.PathValue("id"), in.LabelID, in.Add)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}
```

  Routes: `authed("GET /v1/mail/labels", s.handleListLabels)`, `authed("POST /v1/mail/threads/{id}/labels", s.handleSetThreadLabel)`. Extend `handleBulkThreadActions` (before `ParseThreadAction`):

```go
	if in.Action == "label" || in.Action == "unlabel" {
		if in.LabelID == "" {
			s.writeError(w, r, fmt.Errorf("%w: labelId is required for label actions", domain.ErrValidation))
			return
		}
		res, err := s.deps.Mail.BulkSetLabel(r.Context(), userFrom(r).ID, in.ThreadIDs, in.LabelID, in.Action == "label")
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
```

  - `fakeMailService` stubs for the three new methods (record args, programmable returns). Wire `Labels: store.Labels()` into `service.NewMailService(...)` in `cmd/api/main.go` (and in `cmd/worker/main.go` if it constructs a MailService — it does not today).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./...` — expected: all `ok`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): label listing and single/bulk thread label endpoints"`

---

### Task 7: Unsubscribe execution endpoint (RFC 8058 one-click + mailto + link)

**Files:**
- Create: `backend/internal/adapter/out/unsubscribe/client.go`, `backend/internal/adapter/out/unsubscribe/client_test.go`
- Modify: `backend/internal/port/driven.go` (UnsubscribeGateway), `backend/internal/port/driving.go` (UnsubscribeResult + MailService method), `backend/internal/service/mail.go` (+ `MailServiceDeps.Unsubscriber`), `backend/internal/service/fakes_test.go` (fakeUnsubscriber), `backend/internal/adapter/in/httpapi/mail.go`, `backend/internal/adapter/in/httpapi/httpapi.go`, `backend/internal/adapter/in/httpapi/harness_test.go`, `backend/cmd/api/main.go`
- Test: Modify `backend/internal/service/mail_test.go`

**Interfaces:**
- `port` (driven): `type UnsubscribeGateway interface { PostOneClick(ctx context.Context, url string) error }`
- `port` (driving):

```go
// UnsubscribeResult reports how an unsubscribe was (or must be) performed:
// "one_click" and "mailto" completed server-side; "link" returns the URL the
// client must open in a browser.
type UnsubscribeResult struct {
	Method string `json:"method"` // "one_click" | "mailto" | "link"
	URL    string `json:"url,omitempty"`
}
```

  `port.MailService` gains `UnsubscribeThread(ctx context.Context, userID, threadID string) (UnsubscribeResult, error)`.
- Route: `POST /v1/mail/threads/{id}/unsubscribe` → 200 UnsubscribeResult; 400 (`ErrValidation`) when the thread has no unsubscribe info.

**Steps:**

- [ ] Write the failing adapter test `backend/internal/adapter/out/unsubscribe/client_test.go`:

```go
package unsubscribe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostOneClickSendsRFC8058Body(t *testing.T) {
	var gotBody, gotContentType, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotContentType, gotMethod = string(b), r.Header.Get("Content-Type"), r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New().PostOneClick(context.Background(), srv.URL+"/u?id=1"); err != nil {
		t.Fatalf("PostOneClick: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotBody != "List-Unsubscribe=One-Click" {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestPostOneClickRejectsBadTargets(t *testing.T) {
	if err := New().PostOneClick(context.Background(), "mailto:x@y.z"); err == nil {
		t.Fatal("non-http url accepted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if err := New().PostOneClick(context.Background(), srv.URL); err == nil {
		t.Fatal("non-2xx status accepted")
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/adapter/out/unsubscribe/` — expected: `no Go files` / package-not-found failure.
- [ ] Implement `backend/internal/adapter/out/unsubscribe/client.go`:

```go
// Package unsubscribe implements port.UnsubscribeGateway: an RFC 8058
// one-click list-unsubscribe POST using only the standard library.
package unsubscribe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client POSTs "List-Unsubscribe=One-Click" to the sender's unsubscribe URL.
type Client struct {
	HTTP *http.Client
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) PostOneClick(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid one-click unsubscribe url %q", target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target,
		strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("one-click unsubscribe returned status %d", res.StatusCode)
	}
	return nil
}
```

  Declare `var _ port.UnsubscribeGateway = (*Client)(nil)` after adding the port interface (import `calendium/backend/internal/port`).
- [ ] Write the failing service tests (append to `mail_test.go`; add `unsub *fakeUnsubscriber` to the fixture and `Unsubscriber: f.unsub` to `MailServiceDeps`; define in `fakes_test.go`):

```go
type fakeUnsubscriber struct {
	calls []string
	err   error
}

func (u *fakeUnsubscriber) PostOneClick(_ context.Context, url string) error {
	u.calls = append(u.calls, url)
	return u.err
}

var _ port.UnsubscribeGateway = (*fakeUnsubscriber)(nil)
```

```go
func TestUnsubscribeThreadPrefersOneClick(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	link := "https://news.example/u?id=1"
	mailto := "mailto:unsub@news.example?subject=Unsubscribe"
	f.seedThread(t, "t1", "a1", func(th *domain.Thread) {
		th.UnsubscribeURL = &link
		th.UnsubscribeMailto = &mailto
		th.UnsubscribeOneClick = true
	})

	res, err := f.svc.UnsubscribeThread(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("UnsubscribeThread: %v", err)
	}
	if res.Method != "one_click" {
		t.Fatalf("Method = %q, want one_click", res.Method)
	}
	if !reflect.DeepEqual(f.unsub.calls, []string{link}) {
		t.Fatalf("gateway calls = %v", f.unsub.calls)
	}
	if len(f.provider.sent) != 0 {
		t.Fatal("one-click path must not send mail")
	}
}

func TestUnsubscribeThreadFallsBackToMailto(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	mailto := "mailto:unsub@news.example?subject=Please%20unsubscribe"
	f.seedThread(t, "t1", "a1", func(th *domain.Thread) { th.UnsubscribeMailto = &mailto })

	res, err := f.svc.UnsubscribeThread(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("UnsubscribeThread: %v", err)
	}
	if res.Method != "mailto" {
		t.Fatalf("Method = %q, want mailto", res.Method)
	}
	if len(f.provider.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(f.provider.sent))
	}
	msg := f.provider.sent[0]
	if msg.To[0].Email != "unsub@news.example" || msg.Subject != "Please unsubscribe" {
		t.Fatalf("sent to=%q subject=%q", msg.To[0].Email, msg.Subject)
	}
}

func TestUnsubscribeThreadLinkAndNone(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	link := "https://news.example/manual"
	f.seedThread(t, "t-link", "a1", func(th *domain.Thread) { th.UnsubscribeURL = &link })
	f.seedThread(t, "t-none", "a1", nil)

	res, err := f.svc.UnsubscribeThread(ctx, "u1", "t-link")
	if err != nil || res.Method != "link" || res.URL != link {
		t.Fatalf("link path = %+v, %v", res, err)
	}
	if _, err := f.svc.UnsubscribeThread(ctx, "u1", "t-none"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("no-info err = %v, want ErrValidation", err)
	}
}
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestUnsubscribeThread` — expected: compile error `UnsubscribeThread undefined`.
- [ ] Implement in `service/mail.go` (fields `unsubscriber port.UnsubscribeGateway` from `MailServiceDeps.Unsubscriber`; import `net/url`):

```go
func (s *MailService) UnsubscribeThread(ctx context.Context, userID, threadID string) (port.UnsubscribeResult, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return port.UnsubscribeResult{}, err
	}
	t, acct, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return port.UnsubscribeResult{}, err
	}
	switch {
	case t.UnsubscribeOneClick && t.UnsubscribeURL != nil && s.unsubscriber != nil:
		if err := s.unsubscriber.PostOneClick(ctx, *t.UnsubscribeURL); err != nil {
			return port.UnsubscribeResult{}, fmt.Errorf("one-click unsubscribe failed: %w", err)
		}
		return port.UnsubscribeResult{Method: "one_click"}, nil
	case t.UnsubscribeMailto != nil:
		provider, ok := s.mail[acct.Provider]
		if !ok {
			return port.UnsubscribeResult{}, fmt.Errorf("%w: no mail provider for account", domain.ErrValidation)
		}
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			return port.UnsubscribeResult{}, err
		}
		to, subject := parseUnsubscribeMailto(*t.UnsubscribeMailto)
		if _, err := provider.Send(ctx, token, port.OutgoingMessage{
			From:     domain.EmailAddress{Email: acct.Email},
			To:       []domain.EmailAddress{{Email: to}},
			Subject:  subject,
			BodyText: "unsubscribe",
		}); err != nil {
			return port.UnsubscribeResult{}, fmt.Errorf("mailto unsubscribe failed: %w", err)
		}
		return port.UnsubscribeResult{Method: "mailto"}, nil
	case t.UnsubscribeURL != nil:
		return port.UnsubscribeResult{Method: "link", URL: *t.UnsubscribeURL}, nil
	}
	return port.UnsubscribeResult{}, fmt.Errorf("%w: thread has no unsubscribe information", domain.ErrValidation)
}

// parseUnsubscribeMailto splits "mailto:addr?subject=…" into recipient and
// subject (defaulting to "unsubscribe").
func parseUnsubscribeMailto(raw string) (to, subject string) {
	subject = "unsubscribe"
	rest := strings.TrimPrefix(raw, "mailto:")
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		if q, err := url.ParseQuery(rest[i+1:]); err == nil && q.Get("subject") != "" {
			subject = q.Get("subject")
		}
		rest = rest[:i]
	}
	return rest, subject
}
```

  Handler `handleUnsubscribeThread` (same shape as `handleUnsnoozeThread`, calling `UnsubscribeThread`, writing the result), route `authed("POST /v1/mail/threads/{id}/unsubscribe", s.handleUnsubscribeThread)`, `fakeMailService` stub, and wiring in `cmd/api/main.go`: `Unsubscriber: unsubscribe.New()` (import `calendium/backend/internal/adapter/out/unsubscribe`).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./...` — expected: all `ok`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): unsubscribe execution endpoint with RFC 8058 one-click gateway"`

---

### Task 8: User prefs endpoint (reorderable splits)

**Files:**
- Create: `backend/internal/domain/prefs.go`, `backend/internal/service/prefs.go`, `backend/internal/adapter/out/postgres/prefs.go`, `backend/internal/adapter/in/httpapi/prefs.go`
- Modify: `backend/internal/port/driven.go` (PrefsRepo), `backend/internal/port/driving.go` (PrefsService), `backend/internal/adapter/out/postgres/store.go` (accessor `Prefs()`), `backend/internal/adapter/in/httpapi/httpapi.go` (+ `Deps.Prefs`), `backend/internal/adapter/in/httpapi/harness_test.go` (fakePrefsService), `backend/cmd/api/main.go`
- Test: Create `backend/internal/service/prefs_test.go`, `backend/internal/adapter/out/postgres/prefs_test.go`

**Interfaces:**
- `domain`:

```go
// UserPrefs is per-user client preferences persisted server-side so every
// device sees the same layout. SplitOrder is the split-tab order; empty
// means "default order". Splits missing from the list render after it.
type UserPrefs struct {
	SplitOrder []InboxSplit `json:"splitOrder"`
}

func (p UserPrefs) Validate() error
```

- `port` (driven): `type PrefsRepo interface { Get(ctx context.Context, userID string) (domain.UserPrefs, error) /* zero-value when absent, never ErrNotFound */; Save(ctx context.Context, userID string, p domain.UserPrefs) error }`
- `port` (driving): `type PrefsService interface { GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error); UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error) }`
- Routes: `GET /v1/prefs` → UserPrefs; `PUT /v1/prefs` body UserPrefs → UserPrefs (400 on invalid/duplicate splits).

**Steps:**

- [ ] Write the failing service test `backend/internal/service/prefs_test.go`:

```go
package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"calendium/backend/internal/domain"
)

func TestPrefsRoundTripAndValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewPrefsService(newPrefsRepo())

	// Absent prefs come back as the zero value, not an error.
	got, err := svc.GetPrefs(ctx, "u1")
	if err != nil {
		t.Fatalf("GetPrefs(empty): %v", err)
	}
	if len(got.SplitOrder) != 0 {
		t.Fatalf("SplitOrder = %v, want empty", got.SplitOrder)
	}

	want := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitVIP, domain.SplitImportant, domain.SplitOther}}
	saved, err := svc.UpdatePrefs(ctx, "u1", want)
	if err != nil {
		t.Fatalf("UpdatePrefs: %v", err)
	}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved = %+v, want %+v", saved, want)
	}
	got, err = svc.GetPrefs(ctx, "u1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("GetPrefs = %+v, %v", got, err)
	}

	// Unknown and duplicate splits are validation errors.
	if _, err := svc.UpdatePrefs(ctx, "u1", domain.UserPrefs{SplitOrder: []domain.InboxSplit{"bogus"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown split err = %v, want ErrValidation", err)
	}
	dup := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitVIP, domain.SplitVIP}}
	if _, err := svc.UpdatePrefs(ctx, "u1", dup); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("duplicate split err = %v, want ErrValidation", err)
	}
}
```

  Add `fakePrefsRepo` to `fakes_test.go`:

```go
type fakePrefsRepo struct{ byUser map[string]domain.UserPrefs }

func newPrefsRepo() *fakePrefsRepo { return &fakePrefsRepo{byUser: map[string]domain.UserPrefs{}} }

func (r *fakePrefsRepo) Get(_ context.Context, userID string) (domain.UserPrefs, error) {
	return r.byUser[userID], nil
}

func (r *fakePrefsRepo) Save(_ context.Context, userID string, p domain.UserPrefs) error {
	r.byUser[userID] = p
	return nil
}

var _ port.PrefsRepo = (*fakePrefsRepo)(nil)
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./internal/service/ -run TestPrefsRoundTrip` — expected: compile error `undefined: NewPrefsService`.
- [ ] Implement:
  - `domain/prefs.go`:

```go
package domain

import "fmt"

type UserPrefs struct {
	SplitOrder []InboxSplit `json:"splitOrder"`
}

// Validate rejects unknown or duplicate splits; an empty order is valid
// ("use the default").
func (p UserPrefs) Validate() error {
	seen := make(map[InboxSplit]struct{}, len(p.SplitOrder))
	for _, s := range p.SplitOrder {
		if _, err := ParseInboxSplit(string(s)); err != nil {
			return err
		}
		if _, dup := seen[s]; dup {
			return fmt.Errorf("%w: duplicate split %q in splitOrder", ErrValidation, s)
		}
		seen[s] = struct{}{}
	}
	return nil
}
```

  - `service/prefs.go`:

```go
package service

import (
	"context"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// PrefsService implements port.PrefsService: cheap per-user client
// preferences. No paywall — prefs are layout, not product value.
type PrefsService struct {
	prefs port.PrefsRepo
}

var _ port.PrefsService = (*PrefsService)(nil)

func NewPrefsService(prefs port.PrefsRepo) *PrefsService {
	return &PrefsService{prefs: prefs}
}

func (s *PrefsService) GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error) {
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	return p, nil
}

func (s *PrefsService) UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error) {
	if err := p.Validate(); err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	if err := s.prefs.Save(ctx, userID, p); err != nil {
		return domain.UserPrefs{}, err
	}
	return p, nil
}
```

  - `postgres/prefs.go` (jsonb round-trip via `encoding/json`, matching the repo's existing `jsonArray` helper conventions):

```go
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

type prefsRepo struct{ base }

var _ port.PrefsRepo = prefsRepo{}

func (r prefsRepo) Get(ctx context.Context, userID string) (domain.UserPrefs, error) {
	var raw []byte
	err := r.q(ctx).QueryRowContext(ctx,
		`SELECT split_order FROM user_prefs WHERE user_id = $1`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UserPrefs{}, nil
	}
	if err != nil {
		return domain.UserPrefs{}, err
	}
	var p domain.UserPrefs
	if err := json.Unmarshal(raw, &p.SplitOrder); err != nil {
		return domain.UserPrefs{}, err
	}
	return p, nil
}

func (r prefsRepo) Save(ctx context.Context, userID string, p domain.UserPrefs) error {
	raw, err := json.Marshal(p.SplitOrder)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).ExecContext(ctx, `
		INSERT INTO user_prefs (user_id, split_order, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (user_id) DO UPDATE SET split_order = EXCLUDED.split_order, updated_at = now()`,
		userID, raw)
	return err
}
```

  (If the existing repos don't embed a `base` type, mirror however `snippetRepo` is constructed — same receiver/`q(ctx)` pattern — and add a `Prefs() port.PrefsRepo` accessor to `store.go`.) Repo test `prefs_test.go`: seed a user with `newTestStore`, assert Get-empty → Save → Get → Save-overwrite → Get.
  - `httpapi/prefs.go` handlers `handleGetPrefs` / `handleUpdatePrefs` (decode `domain.UserPrefs`, call `s.deps.Prefs`), routes `authed("GET /v1/prefs", …)` / `authed("PUT /v1/prefs", …)`, `Deps.Prefs port.PrefsService`, `fakePrefsService` in `harness_test.go`, wire `Prefs: service.NewPrefsService(store.Prefs())` in `cmd/api/main.go` and `Prefs: prefs` into `httpapi.Deps`.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/backend && go test ./...` — expected: all `ok`.
- [ ] Commit: `git add -A && git commit -m "feat(backend): user prefs endpoint persisting split order"`

---

### Task 9: Shared types + ApiClient methods

**Files:**
- Modify: `packages/shared/src/types.ts`, `packages/shared/src/client.ts`
- Test: Modify `packages/shared/src/client.test.ts`

**Interfaces:**
- `types.ts` additions:

```ts
// Thread gains (after remindAt):
  /** Parsed List-Unsubscribe targets from the newest message (RFC 2369/8058). */
  unsubscribeMailto: string | null;
  unsubscribeUrl: string | null;
  unsubscribeOneClick: boolean;

export type BulkAction = ThreadAction | 'label' | 'unlabel';

export interface BulkActionResult {
  threads: Thread[];
  failedIds: string[];
}

export type UnsubscribeMethod = 'one_click' | 'mailto' | 'link';

/** "one_click"/"mailto" completed server-side; "link" = open url in a browser. */
export interface UnsubscribeResult {
  method: UnsubscribeMethod;
  url?: string;
}

/** Per-user layout preferences, shared across devices (GET/PUT /v1/prefs). */
export interface UserPrefs {
  splitOrder: InboxSplit[];
}
```

- `client.ts` additions (inside `ApiClient`):

```ts
  listLabels() {
    return this.request<Label[]>('GET', '/v1/mail/labels');
  }
  /** Adds (add=true) or removes a label on a thread. */
  setThreadLabel(threadId: string, labelId: string, add: boolean) {
    return this.request<Thread>('POST', `/v1/mail/threads/${threadId}/labels`, { labelId, add });
  }
  /** Bulk archive/read/label… up to 200 threads; label actions need labelId. */
  bulkThreadAction(input: { threadIds: string[]; action: BulkAction; labelId?: string }) {
    return this.request<BulkActionResult>('POST', '/v1/mail/threads/bulk-actions', input);
  }
  /** Clears a pending snooze (undo of snooze). */
  unsnoozeThread(threadId: string) {
    return this.request<Thread>('DELETE', `/v1/mail/threads/${threadId}/snooze`);
  }
  /** Executes unsubscribe server-side; method "link" returns a URL to open. */
  unsubscribeThread(threadId: string) {
    return this.request<UnsubscribeResult>('POST', `/v1/mail/threads/${threadId}/unsubscribe`);
  }
  /** Get Me To Zero: archives inbox mail older than the RFC 3339 cutoff. */
  archiveOlderThan(olderThan: string) {
    return this.request<{ archivedCount: number }>('POST', '/v1/mail/threads/zero', { olderThan });
  }
  getPrefs() {
    return this.request<UserPrefs>('GET', '/v1/prefs');
  }
  updatePrefs(prefs: UserPrefs) {
    return this.request<UserPrefs>('PUT', '/v1/prefs', prefs);
  }
```

  (extend the type-only import list with `BulkAction`, `BulkActionResult`, `Label`, `UnsubscribeResult`, `UserPrefs`).

**Steps:**

- [ ] Write the failing tests (append to `packages/shared/src/client.test.ts`, following its existing fetch-mock pattern — the suite stubs `fetch` and asserts method/path/body/auth):

```ts
describe('triage power endpoints', () => {
  it('bulkThreadAction POSTs ids + action to /v1/mail/threads/bulk-actions', async () => {
    const { client, calls } = makeClient({
      json: { threads: [], failedIds: ['ghost'] },
    });
    const res = await client.bulkThreadAction({ threadIds: ['t1', 'ghost'], action: 'archive' });
    expect(res.failedIds).toEqual(['ghost']);
    expect(calls[0]!.url).toBe('https://api.test/v1/mail/threads/bulk-actions');
    expect(calls[0]!.init?.method).toBe('POST');
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({
      threadIds: ['t1', 'ghost'],
      action: 'archive',
    });
  });

  it('setThreadLabel POSTs labelId+add to the thread labels route', async () => {
    const { client, calls } = makeClient({ json: { id: 't1' } });
    await client.setThreadLabel('t1', 'lbl1', true);
    expect(calls[0]!.url).toBe('https://api.test/v1/mail/threads/t1/labels');
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ labelId: 'lbl1', add: true });
  });

  it('unsnoozeThread DELETEs the snooze', async () => {
    const { client, calls } = makeClient({ json: { id: 't1' } });
    await client.unsnoozeThread('t1');
    expect(calls[0]!.url).toBe('https://api.test/v1/mail/threads/t1/snooze');
    expect(calls[0]!.init?.method).toBe('DELETE');
  });

  it('unsubscribeThread POSTs and returns the method', async () => {
    const { client, calls } = makeClient({ json: { method: 'one_click' } });
    const res = await client.unsubscribeThread('t1');
    expect(res.method).toBe('one_click');
    expect(calls[0]!.url).toBe('https://api.test/v1/mail/threads/t1/unsubscribe');
  });

  it('archiveOlderThan POSTs the cutoff to /zero', async () => {
    const { client, calls } = makeClient({ json: { archivedCount: 7 } });
    const res = await client.archiveOlderThan('2026-07-10T00:00:00Z');
    expect(res.archivedCount).toBe(7);
    expect(calls[0]!.url).toBe('https://api.test/v1/mail/threads/zero');
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ olderThan: '2026-07-10T00:00:00Z' });
  });

  it('prefs round-trip GET/PUT /v1/prefs', async () => {
    const { client, calls } = makeClient({ json: { splitOrder: ['vip', 'important'] } });
    await client.getPrefs();
    await client.updatePrefs({ splitOrder: ['vip', 'important'] });
    expect(calls[0]!.url).toBe('https://api.test/v1/prefs');
    expect(calls[1]!.init?.method).toBe('PUT');
  });
});
```

  (If `client.test.ts` names its helper differently from `makeClient`, reuse its actual helper — the assertions stay the same.)
- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared` — expected: type/compile failures (`bulkThreadAction` does not exist).
- [ ] Implement the types and client methods exactly as in **Interfaces** above. Update `apps/web/lib/mail-mock.ts` thread construction ONLY as far as making the shared `Thread` type compile (add `unsubscribeMailto: null, unsubscribeUrl: null, unsubscribeOneClick: false` defaults in `build()`; Task 11 adds real demo values) and any other mock builders that construct `Thread` literals (`apps/desktop/frontend/src/lib/mock.ts`, mobile mocks) — run `bun run typecheck` to find them all.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared && bun run typecheck` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(shared): client methods and types for triage power endpoints"`

---

### Task 10: Shared triage core — range-selection model + undo stack

**Files:**
- Create: `packages/shared/src/triage.ts`
- Modify: `packages/shared/src/index.ts` (add `export * from './triage';`)
- Test: Create `packages/shared/src/triage.test.ts`

**Interfaces:**

```ts
export interface Selection {
  readonly anchorId: string | null;
  readonly ids: ReadonlySet<string>;
}
export const EMPTY_SELECTION: Selection;
export function toggleSelected(sel: Selection, id: string): Selection; // `x`
export function extendSelection(sel: Selection, orderedIds: readonly string[], cursorId: string): Selection; // shift+j/k
export function clearSelection(): Selection;
export function nextAfterRemoval(orderedIds: readonly string[], removedId: string): string | null; // auto-advance

export interface UndoEntry { label: string; undo: () => void | Promise<void>; }
export class UndoStack {
  push(entry: UndoEntry): void; // capped at 50, oldest dropped
  pop(): UndoEntry | undefined;
  clear(): void;
  get size(): number;
}
```

**Steps:**

- [ ] Write the failing tests `packages/shared/src/triage.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
  EMPTY_SELECTION,
  UndoStack,
  clearSelection,
  extendSelection,
  nextAfterRemoval,
  toggleSelected,
} from './triage';

const order = ['a', 'b', 'c', 'd', 'e'];

describe('selection model', () => {
  it('toggleSelected adds, then removes, and moves the anchor', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'b');
    expect([...sel.ids]).toEqual(['b']);
    expect(sel.anchorId).toBe('b');
    sel = toggleSelected(sel, 'b');
    expect(sel.ids.size).toBe(0);
    expect(sel.anchorId).toBe('b');
  });

  it('extendSelection selects the whole range from the anchor, both directions', () => {
    let sel = toggleSelected(EMPTY_SELECTION, 'b');
    sel = extendSelection(sel, order, 'd');
    expect([...sel.ids].sort()).toEqual(['b', 'c', 'd']);
    expect(sel.anchorId).toBe('b');
    sel = extendSelection(sel, order, 'a'); // reverse direction unions
    expect([...sel.ids].sort()).toEqual(['a', 'b', 'c', 'd']);
  });

  it('extendSelection with no anchor behaves like toggle', () => {
    const sel = extendSelection(EMPTY_SELECTION, order, 'c');
    expect([...sel.ids]).toEqual(['c']);
  });

  it('extendSelection tolerates ids missing from the current order', () => {
    const sel = extendSelection({ anchorId: 'zz', ids: new Set(['zz']) }, order, 'b');
    expect(sel.ids.has('b')).toBe(true);
  });

  it('clearSelection empties everything', () => {
    expect(clearSelection()).toEqual(EMPTY_SELECTION);
  });
});

describe('nextAfterRemoval (auto-advance)', () => {
  it('prefers the next item below', () => {
    expect(nextAfterRemoval(order, 'b')).toBe('c');
  });
  it('falls back to the previous item at the end of the list', () => {
    expect(nextAfterRemoval(order, 'e')).toBe('d');
  });
  it('returns null for a single-item list or unknown id', () => {
    expect(nextAfterRemoval(['only'], 'only')).toBeNull();
    expect(nextAfterRemoval(order, 'zz')).toBeNull();
  });
});

describe('UndoStack', () => {
  it('pops LIFO and reports size', () => {
    const stack = new UndoStack();
    stack.push({ label: 'one', undo: () => {} });
    stack.push({ label: 'two', undo: () => {} });
    expect(stack.size).toBe(2);
    expect(stack.pop()?.label).toBe('two');
    expect(stack.pop()?.label).toBe('one');
    expect(stack.pop()).toBeUndefined();
  });

  it('caps at 50 entries, dropping the oldest', () => {
    const stack = new UndoStack();
    for (let i = 0; i < 55; i++) stack.push({ label: `e${i}`, undo: () => {} });
    expect(stack.size).toBe(50);
    let last: string | undefined;
    while (stack.size > 0) last = stack.pop()?.label;
    expect(last).toBe('e5');
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared` — expected: `Cannot find module './triage'`.
- [ ] Implement `packages/shared/src/triage.ts`:

```ts
/**
 * Framework-agnostic triage primitives shared by web, desktop, and mobile:
 * the bulk range-selection model (x / shift+j / shift+k), the auto-advance
 * rule, and the undo-anything stack. Pure data + functions — no React.
 */

export interface Selection {
  readonly anchorId: string | null;
  readonly ids: ReadonlySet<string>;
}

export const EMPTY_SELECTION: Selection = { anchorId: null, ids: new Set() };

/** Toggle one conversation (the `x` key); the toggled row becomes the anchor. */
export function toggleSelected(sel: Selection, id: string): Selection {
  const ids = new Set(sel.ids);
  if (ids.has(id)) ids.delete(id);
  else ids.add(id);
  return { anchorId: id, ids };
}

/**
 * Extend the selection from the anchor to the cursor (shift+j / shift+k),
 * unioning with what is already selected (Superhuman range semantics). An
 * unknown anchor or cursor degrades to a plain toggle so keyboard flow never
 * dead-ends after a list refetch.
 */
export function extendSelection(
  sel: Selection,
  orderedIds: readonly string[],
  cursorId: string
): Selection {
  const anchor = sel.anchorId ?? cursorId;
  const a = orderedIds.indexOf(anchor);
  const c = orderedIds.indexOf(cursorId);
  if (a < 0 || c < 0) return toggleSelected(sel, cursorId);
  const [lo, hi] = a <= c ? [a, c] : [c, a];
  const ids = new Set(sel.ids);
  for (let i = lo; i <= hi; i++) ids.add(orderedIds[i]!);
  return { anchorId: anchor, ids };
}

export function clearSelection(): Selection {
  return EMPTY_SELECTION;
}

/**
 * Auto-advance: the conversation to open after `removedId` leaves the list —
 * the one below it, else the one above, else null (list emptied).
 */
export function nextAfterRemoval(
  orderedIds: readonly string[],
  removedId: string
): string | null {
  const i = orderedIds.indexOf(removedId);
  if (i < 0) return null;
  return orderedIds[i + 1] ?? orderedIds[i - 1] ?? null;
}

export interface UndoEntry {
  label: string;
  undo: () => void | Promise<void>;
}

const UNDO_CAP = 50;

/** LIFO stack behind "undo anything" (Z). Capped so it cannot leak forever. */
export class UndoStack {
  private entries: UndoEntry[] = [];

  push(entry: UndoEntry): void {
    this.entries.push(entry);
    if (this.entries.length > UNDO_CAP) this.entries.shift();
  }

  pop(): UndoEntry | undefined {
    return this.entries.pop();
  }

  clear(): void {
    this.entries = [];
  }

  get size(): number {
    return this.entries.length;
  }
}
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:shared` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(shared): triage core with range selection, auto-advance, and undo stack"`

---

### Task 11: Web data layer — triage hooks + demo-mode mocks

**Files:**
- Modify: `apps/web/lib/use-mail.ts`, `apps/web/lib/mail-mock.ts`
- Create: `apps/web/lib/prefs-data.ts`
- Test: Create `apps/web/lib/mail-mock.test.ts`

**Interfaces:**
- `mail-mock.ts` additions (all demo-only, reached solely through the `DEMO_MODE` fallbacks in hooks):

```ts
export function getMockLabels(): Label[]; // seeded: lbl_updates "Updates", lbl_receipts "Receipts", lbl_travel "Travel" on acc_demo
export function applyMockLabel(threadId: string, labelId: string, add: boolean): void;
export function mockBulkAction(threadIds: string[], action: ThreadAction): void;
export function mockUnsnoozeThread(threadId: string): void;
export function mockUnsubscribe(threadId: string): UnsubscribeResult; // one_click for threads seeded with oneClick, else mailto/link
export function mockArchiveOlderThan(olderThanIso: string): number;
```

  `ThreadSpec` gains `unsubscribe?: { mailto?: string; url?: string; oneClick?: boolean }`; `build()` maps it onto the new Thread fields. Seed it on the News threads (The Batch, Stratechery, Changelog, Product Hunt: `{ mailto: 'mailto:unsub@…', url: 'https://…/unsubscribe', oneClick: true }` for two of them, `{ url: '…' }` only for one, `{ mailto: '…' }` only for one — all three methods reachable in demo).
- `use-mail.ts` additions:

```ts
export const mailUndo = new UndoStack(); // module singleton
export function useLabels(): UseQueryResult<{ labels: Label[]; source: DataSource }>;
// useMailActions() return value gains:
//   act(threadId, action, opts?: { undoable?: boolean })  // now pushes inverses
//   bulkAct(threadIds: string[], action: BulkAction, labelId?: string): Promise<void>
//   unsnooze(threadId: string): Promise<void>
//   setLabel(threadId: string, labelId: string, add: boolean): Promise<void>
//   unsubscribe(threadId: string): Promise<UnsubscribeResult>
//   getMeToZero(olderThanIso: string): Promise<number>
//   undoLast(): Promise<boolean>  // pops mailUndo; false when empty
```

- `prefs-data.ts`:

```ts
export function usePrefs(): UseQueryResult<{ prefs: UserPrefs; source: DataSource }>;
export function useUpdatePrefs(): (prefs: UserPrefs) => Promise<void>; // optimistic ['prefs'] cache write
```

**Steps:**

- [ ] Write the failing mock-store tests `apps/web/lib/mail-mock.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
  applyMockLabel,
  getMockLabels,
  getMockThread,
  getMockThreads,
  mockArchiveOlderThan,
  mockBulkAction,
  mockUnsubscribe,
} from './mail-mock';

describe('mail-mock triage extensions', () => {
  it('seeds demo labels on the demo account', () => {
    const labels = getMockLabels();
    expect(labels.length).toBeGreaterThanOrEqual(3);
    expect(labels.every((l) => l.accountId === 'acc_demo')).toBe(true);
  });

  it('applyMockLabel toggles labelIds on a thread', () => {
    const anyThread = getMockThreads({}).items[0]!;
    const label = getMockLabels()[0]!;
    applyMockLabel(anyThread.id, label.id, true);
    expect(getMockThread(anyThread.id)!.thread.labelIds).toContain(label.id);
    applyMockLabel(anyThread.id, label.id, false);
    expect(getMockThread(anyThread.id)!.thread.labelIds).not.toContain(label.id);
  });

  it('mockBulkAction archives every id given', () => {
    const items = getMockThreads({ split: 'news' }).items;
    expect(items.length).toBeGreaterThanOrEqual(2);
    const ids = items.slice(0, 2).map((t) => t.id);
    mockBulkAction(ids, 'archive');
    const after = getMockThreads({ split: 'news' }).items.map((t) => t.id);
    for (const id of ids) expect(after).not.toContain(id);
    mockBulkAction(ids, 'move_to_inbox'); // restore for other tests
  });

  it('mockUnsubscribe reports the strongest available method', () => {
    const news = getMockThreads({ split: 'news' }).items;
    const oneClick = news.find((t) => t.unsubscribeOneClick);
    expect(oneClick).toBeDefined();
    expect(mockUnsubscribe(oneClick!.id).method).toBe('one_click');
  });

  it('mockArchiveOlderThan only archives strictly older inbox threads', () => {
    const before = getMockThreads({}).items.length;
    const count = mockArchiveOlderThan(new Date(Date.now() - 365 * 24 * 3_600_000).toISOString());
    expect(count).toBe(0); // nothing in the demo set is a year old
    expect(getMockThreads({}).items.length).toBe(before);
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/mail-mock.test.ts` — expected: import errors for the new exports.
- [ ] Implement the mock extensions in `apps/web/lib/mail-mock.ts`:

```ts
const MOCK_LABELS: Label[] = [
  { id: 'lbl_updates', accountId: ACCOUNT_ID, name: 'Updates', kind: 'user', color: null },
  { id: 'lbl_receipts', accountId: ACCOUNT_ID, name: 'Receipts', kind: 'user', color: null },
  { id: 'lbl_travel', accountId: ACCOUNT_ID, name: 'Travel', kind: 'user', color: null },
];

export function getMockLabels(): Label[] {
  return MOCK_LABELS.map((l) => ({ ...l }));
}

export function applyMockLabel(threadId: string, labelId: string, add: boolean): void {
  const record = store.get(threadId);
  if (!record) return;
  const ids = record.thread.labelIds.filter((id) => id !== labelId);
  if (add) ids.push(labelId);
  record.thread.labelIds = ids;
}

export function mockBulkAction(threadIds: string[], action: ThreadAction): void {
  for (const id of threadIds) applyMockAction(id, action);
}

export function mockUnsnoozeThread(threadId: string): void {
  const record = store.get(threadId);
  if (record) record.thread.snoozedUntil = null;
}

export function mockUnsubscribe(threadId: string): UnsubscribeResult {
  const record = store.get(threadId);
  if (!record) return { method: 'link', url: 'https://example.com/unsubscribe' };
  const t = record.thread;
  if (t.unsubscribeOneClick && t.unsubscribeUrl) return { method: 'one_click' };
  if (t.unsubscribeMailto) return { method: 'mailto' };
  return { method: 'link', url: t.unsubscribeUrl ?? 'https://example.com/unsubscribe' };
}

export function mockArchiveOlderThan(olderThanIso: string): number {
  const cutoff = Date.parse(olderThanIso);
  let archived = 0;
  for (const record of store.values()) {
    const t = record.thread;
    const inInbox = !t.snoozedUntil; // demo approximation of inbox membership
    if (inInbox && Date.parse(t.lastMessageAt) < cutoff) {
      applyMockAction(t.id, 'archive');
      archived++;
    }
  }
  return archived;
}
```

  plus the `ThreadSpec.unsubscribe` field, its mapping in `build()`, and the seeded values on the four News specs. NOTE: `applyMockAction('archive')` must actually remove the thread from `getMockThreads({})` results — it already does in the current store (archived threads drop out of split listings); keep that contract.
- [ ] Implement the hooks. In `use-mail.ts`: import `UndoStack`, `nextAfterRemoval` not needed here (page-level), add `export const mailUndo = new UndoStack();`, extend `useMailActions`:

```ts
const ACTION_INVERSE: Partial<Record<ThreadAction, ThreadAction>> = {
  archive: 'move_to_inbox',
  trash: 'move_to_inbox',
  spam: 'move_to_inbox',
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  move_to_inbox: 'archive',
};

const ACTION_UNDO_LABEL: Partial<Record<ThreadAction, string>> = {
  archive: 'Archive',
  trash: 'Delete',
  spam: 'Report spam',
  star: 'Star',
  unstar: 'Unstar',
  read: 'Mark read',
  unread: 'Mark unread',
  move_to_inbox: 'Move to inbox',
};
```

  `act(threadId, action, opts?: { undoable?: boolean })`: unchanged optimistic flow, but when `opts?.undoable !== false` and `ACTION_INVERSE[action]` exists, push `{ label: ACTION_UNDO_LABEL[action] ?? action, undo: () => act(threadId, ACTION_INVERSE[action]!, { undoable: false }).then(() => { void queryClient.invalidateQueries({ queryKey: ['threads'] }); }) }` onto `mailUndo`. `snooze()` pushes `{ label: 'Snooze', undo: async () => { await unsnooze(threadId); } }`. New functions:

```ts
  async function bulkAct(threadIds: string[], action: BulkAction, labelId?: string): Promise<void> {
    const previousLists = queryClient.getQueriesData<ThreadListResult | undefined>({ queryKey: ['threads'] });
    const removes = action === 'archive' || action === 'trash' || action === 'spam';
    const idSet = new Set(threadIds);
    queryClient.setQueriesData<ThreadListResult | undefined>({ queryKey: ['threads'] }, (data) => {
      if (!data) return data;
      const items = removes
        ? data.page.items.filter((t) => !idSet.has(t.id))
        : data.page.items.map((t) =>
            idSet.has(t.id) && action !== 'label' && action !== 'unlabel'
              ? applyActionToThread(t, action as ThreadAction)
              : t
          );
      return { ...data, page: { ...data.page, items } };
    });
    if (DEMO_MODE) {
      if (action === 'label' || action === 'unlabel') {
        for (const id of threadIds) applyMockLabel(id, labelId ?? '', action === 'label');
      } else {
        mockBulkAction(threadIds, action);
      }
    }
    try {
      await getApiClient().bulkThreadAction({ threadIds, action, labelId });
      if (action !== 'label' && action !== 'unlabel') {
        const inverse = ACTION_INVERSE[action];
        if (inverse) {
          mailUndo.push({
            label: `${ACTION_UNDO_LABEL[action] ?? action} ${threadIds.length} conversations`,
            undo: async () => {
              await getApiClient().bulkThreadAction({ threadIds, action: inverse }).catch(() => {
                if (!DEMO_MODE) throw new Error('undo failed');
                mockBulkAction(threadIds, inverse);
              });
              void queryClient.invalidateQueries({ queryKey: ['threads'] });
            },
          });
        }
      }
    } catch {
      if (DEMO_MODE) return; // mock already applied; demo keeps the optimistic state
      for (const [key, data] of previousLists) queryClient.setQueryData(key, data);
      toast.error('Could not update the selected conversations.');
    }
  }

  async function unsnooze(threadId: string): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => ({ ...t, snoozedUntil: null }),
      false,
      () => getApiClient().unsnoozeThread(threadId),
      () => mockUnsnoozeThread(threadId),
      'Could not cancel the snooze.'
    );
  }

  async function setLabel(threadId: string, labelId: string, add: boolean): Promise<void> {
    await runOptimistic(
      threadId,
      (t) => ({
        ...t,
        labelIds: add ? [...t.labelIds.filter((id) => id !== labelId), labelId] : t.labelIds.filter((id) => id !== labelId),
      }),
      false,
      () => getApiClient().setThreadLabel(threadId, labelId, add),
      () => applyMockLabel(threadId, labelId, add),
      'Could not update the label.'
    );
    mailUndo.push({ label: add ? 'Label' : 'Remove label', undo: () => setLabel(threadId, labelId, !add) });
  }

  async function unsubscribe(threadId: string): Promise<UnsubscribeResult> {
    try {
      return await getApiClient().unsubscribeThread(threadId);
    } catch (err) {
      if (DEMO_MODE) return mockUnsubscribe(threadId);
      throw err;
    }
  }

  async function getMeToZero(olderThanIso: string): Promise<number> {
    try {
      const res = await getApiClient().archiveOlderThan(olderThanIso);
      void queryClient.invalidateQueries({ queryKey: ['threads'] });
      return res.archivedCount;
    } catch (err) {
      if (DEMO_MODE) {
        const count = mockArchiveOlderThan(olderThanIso);
        void queryClient.invalidateQueries({ queryKey: ['threads'] });
        return count;
      }
      throw err;
    }
  }

  async function undoLast(): Promise<boolean> {
    const entry = mailUndo.pop();
    if (!entry) return false;
    await entry.undo();
    return true;
  }
```

  and `useLabels`:

```ts
export function useLabels() {
  return useQuery({
    queryKey: ['labels'],
    staleTime: 5 * 60_000,
    queryFn: async (): Promise<{ labels: Label[]; source: DataSource }> => {
      try {
        return { labels: await getApiClient().listLabels(), source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { labels: getMockLabels(), source: 'demo' };
        throw err;
      }
    },
  });
}
```

- [ ] Implement `apps/web/lib/prefs-data.ts`:

```ts
'use client';

import type { UserPrefs } from '@calendium/shared';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';

import { getApiClient } from '@/lib/api';
import { DEMO_MODE } from '@/lib/demo';
import type { DataSource } from '@/lib/use-mail';

// Demo-only in-memory prefs store (honesty policy: reached only when
// DEMO_MODE is on and the real API is unreachable).
let demoPrefs: UserPrefs = { splitOrder: [] };

export interface PrefsResult {
  prefs: UserPrefs;
  source: DataSource;
}

export function usePrefs() {
  return useQuery({
    queryKey: ['prefs'],
    staleTime: 60_000,
    queryFn: async (): Promise<PrefsResult> => {
      try {
        return { prefs: await getApiClient().getPrefs(), source: 'api' };
      } catch (err) {
        if (DEMO_MODE) return { prefs: demoPrefs, source: 'demo' };
        throw err;
      }
    },
  });
}

export function useUpdatePrefs() {
  const queryClient = useQueryClient();
  return async function updatePrefs(prefs: UserPrefs): Promise<void> {
    const previous = queryClient.getQueryData<PrefsResult>(['prefs']);
    queryClient.setQueryData<PrefsResult | undefined>(['prefs'], (data) =>
      data ? { ...data, prefs } : { prefs, source: 'api' }
    );
    if (DEMO_MODE) demoPrefs = prefs;
    try {
      await getApiClient().updatePrefs(prefs);
    } catch (err) {
      if (DEMO_MODE) return;
      if (previous) queryClient.setQueryData(['prefs'], previous);
      toast.error('Could not save your preferences.');
      throw err;
    }
  };
}
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:web && bun run typecheck` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): triage data layer with undoable mutations, labels, prefs, and demo mocks"`

---

### Task 12: Undo-anything (Z) with snooze rebound to H

Superhuman key map: `E` archive, `H` snooze, `Z` undo. Today the app has `z` = snooze and `h` = reminder, so this task rebinds before `Z` can mean undo.

**Files:**
- Modify: `apps/web/app/(app)/mail/page.tsx` (shortcuts, hints row, EmptyState copy), `apps/web/lib/mail-utils.ts` (MailCommand `'undo'`), `apps/web/components/app/command-palette.tsx` (Undo entry + updated Kbd hints)
- Test: Modify `apps/web/e2e/mail-triage.spec.ts`

**Interfaces:**
- Consumes: `mailUndo`, `useMailActions().undoLast` (Task 11).
- Produces: key map — `h` snooze dialog, `shift+h` reminder dialog, `z` undo last triage action; `MailCommand` union gains `'undo'`.

**Steps:**

- [ ] Write the failing e2e (append to `apps/web/e2e/mail-triage.spec.ts`):

```ts
test('Z undoes an archive, restoring the thread to the list', async ({ page }) => {
  await page.goto('/mail');
  const target = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
  await expect(target).toBeVisible();
  await target.hover(); // hover selects the row (onMouseEnter)
  await page.keyboard.press('e');
  await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toHaveCount(0);

  await page.keyboard.press('z');
  await expect(page.getByText('Undone', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
});

test('H opens the snooze picker (rebound from Z)', async ({ page }) => {
  await page.goto('/mail');
  await expect(page.getByRole('button', { name: /Renewal terms for FY27/ }).first()).toBeVisible();
  await page.keyboard.press('h');
  await expect(page.getByText('Snooze until…')).toBeVisible();
  await page.keyboard.press('Escape');
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/mail-triage.spec.ts` — expected: the two new tests fail (`z` opens the snooze dialog instead of undoing; no `Undone` toast).
- [ ] Implement in `apps/web/app/(app)/mail/page.tsx`:
  - Pull `undoLast` from `useMailActions()` (destructure `const { act, snooze, remind, unsnooze, undoLast } = useMailActions();` — extend as later tasks need more).
  - Add the undo handler + rebind the shortcut rows:

```tsx
  const undo = React.useCallback(() => {
    void undoLast().then((did) => {
      if (did) toast.success('Undone');
      else toast.message('Nothing to undo');
    });
  }, [undoLast]);
```

```tsx
    { keys: 'h', description: 'Snooze', handler: () => selectedThread && setSnoozeOpen(true) },
    { keys: 'shift+h', description: 'Follow-up reminder', handler: () => selectedThread && setRemindOpen(true) },
    { keys: 'z', description: 'Undo last action', handler: undo },
```

    (replacing the old `z`-snooze and `h`-reminder rows).
  - Hints row: change `<Kbd size="sm">Z</Kbd> snooze` to `<Kbd size="sm">H</Kbd> snooze` and append `<span className="flex items-center gap-1"><Kbd size="sm">Z</Kbd> undo</span>`.
  - EmptyState snoozed copy: `'Press Z to snooze a conversation until later.'` → `'Press H to snooze a conversation until later.'`
  - The archive toast's inline Undo button must stay consistent with the stack: change `archiveSelected`'s toast action to `onClick: () => void undoLast()` so both paths pop the same entry.
  - `mail-utils.ts`: add `'undo'` to the `MailCommand` union. `mail/page.tsx` `runCommand`: `else if (command === 'undo') undo();`.
  - `command-palette.tsx`: add an `Undo last action` CommandItem in the mail group dispatching `'undo'` (same `dispatchMailCommand`/`queueMailCommand` pattern as Archive), showing `<Kbd>Z</Kbd>`; update the palette's snooze row hint from `Z` to `H` and reminder to `⇧H`.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/mail-triage.spec.ts && bunx vitest run` — expected: all green (fix any palette component-test snapshots that referenced the old key hints in `command-palette.test.tsx`).
- [ ] Commit: `git add -A && git commit -m "feat(web): undo-anything on Z with snooze rebound to H"`

---

### Task 13: Bulk range selection + bulk action bar

**Files:**
- Create: `apps/web/components/app/bulk-bar.tsx`
- Modify: `apps/web/app/(app)/mail/page.tsx` (selection state, `x`/`shift+j`/`shift+k`/`escape`, bulk-aware `e`/`shift+i`, row checkbox accent)
- Test: Create `apps/web/components/app/bulk-bar.test.tsx`, `apps/web/e2e/bulk-triage.spec.ts`

**Interfaces:**
- `BulkBar` component:

```tsx
export interface BulkBarProps {
  count: number;
  onArchive: () => void;
  onMarkRead: () => void;
  onLabel: () => void;       // opens the Task 14 picker
  onUnsubscribe: () => void; // enabled by Task 17; pass a no-op until then
  onClear: () => void;
}
export function BulkBar(props: BulkBarProps): React.JSX.Element | null; // null when count === 0
```

- Consumes: `EMPTY_SELECTION`, `toggleSelected`, `extendSelection`, `clearSelection` from `@calendium/shared`; `bulkAct` from Task 11.

**Steps:**

- [ ] Write the failing component test `apps/web/components/app/bulk-bar.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { BulkBar } from './bulk-bar';

function makeProps() {
  return {
    count: 3,
    onArchive: vi.fn(),
    onMarkRead: vi.fn(),
    onLabel: vi.fn(),
    onUnsubscribe: vi.fn(),
    onClear: vi.fn(),
  };
}

describe('BulkBar', () => {
  it('renders nothing when the selection is empty', () => {
    const { container } = render(<BulkBar {...makeProps()} count={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the count and fires the action callbacks', async () => {
    const props = makeProps();
    render(<BulkBar {...props} />);
    expect(screen.getByText('3 selected')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /Archive/ }));
    expect(props.onArchive).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Mark read/ }));
    expect(props.onMarkRead).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Label/ }));
    expect(props.onLabel).toHaveBeenCalledOnce();
    await userEvent.click(screen.getByRole('button', { name: /Clear/ }));
    expect(props.onClear).toHaveBeenCalledOnce();
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run components/app/bulk-bar.test.tsx` — expected: `Cannot find module './bulk-bar'`.
- [ ] Implement `apps/web/components/app/bulk-bar.tsx`:

```tsx
'use client';

import * as React from 'react';
import { Archive, MailCheck, MailX, Tag, X } from 'lucide-react';

import { Kbd } from '@/components/ui/kbd';
import { cn } from '@/lib/utils';

export interface BulkBarProps {
  count: number;
  onArchive: () => void;
  onMarkRead: () => void;
  onLabel: () => void;
  onUnsubscribe: () => void;
  onClear: () => void;
}

/** Floating action bar shown while a bulk range selection is active. */
export function BulkBar({ count, onArchive, onMarkRead, onLabel, onUnsubscribe, onClear }: BulkBarProps) {
  if (count === 0) return null;
  return (
    <div
      role="toolbar"
      aria-label="Bulk actions"
      className={cn(
        'bg-background absolute bottom-10 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1',
        'rounded-lg border px-2 py-1.5 shadow-lg'
      )}
    >
      <span className="px-1.5 text-xs font-medium">{count} selected</span>
      <BulkButton icon={<Archive className="size-3.5" />} label="Archive" kbd="E" onClick={onArchive} />
      <BulkButton icon={<MailCheck className="size-3.5" />} label="Mark read" kbd="⇧I" onClick={onMarkRead} />
      <BulkButton icon={<Tag className="size-3.5" />} label="Label" kbd="L" onClick={onLabel} />
      <BulkButton icon={<MailX className="size-3.5" />} label="Unsubscribe" onClick={onUnsubscribe} />
      <BulkButton icon={<X className="size-3.5" />} label="Clear" kbd="Esc" onClick={onClear} />
    </div>
  );
}

function BulkButton({
  icon,
  label,
  kbd,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  kbd?: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="hover:bg-accent flex items-center gap-1.5 rounded-md px-2 py-1 text-xs"
    >
      {icon}
      {label}
      {kbd && <Kbd size="sm">{kbd}</Kbd>}
    </button>
  );
}
```

- [ ] Wire selection into `apps/web/app/(app)/mail/page.tsx`:
  - State + derived ids:

```tsx
  const [selection, setSelection] = React.useState(EMPTY_SELECTION);
  const orderedIds = React.useMemo(() => threads.map((t) => t.id), [threads]);
  const selectedIds = React.useMemo(
    () => orderedIds.filter((id) => selection.ids.has(id)),
    [orderedIds, selection]
  );
```

  - Shortcuts (added to the existing `useShortcuts` array; `escape` handler extended to clear the selection first):

```tsx
    {
      keys: 'x',
      description: 'Select conversation',
      handler: () => selectedThread && setSelection((s) => toggleSelected(s, selectedThread.id)),
    },
    {
      keys: 'shift+j',
      description: 'Extend selection down',
      handler: () => {
        if (threads.length === 0) return;
        const index = selectedIndex === -1 ? 0 : Math.min(selectedIndex + 1, threads.length - 1);
        const next = threads[index]!;
        setSelectedId(next.id);
        setSelection((s) => extendSelection(s.ids.size === 0 && selectedThread ? toggleSelected(s, selectedThread.id) : s, orderedIds, next.id));
      },
    },
    {
      keys: 'shift+k',
      description: 'Extend selection up',
      handler: () => {
        if (threads.length === 0) return;
        const index = selectedIndex === -1 ? 0 : Math.max(selectedIndex - 1, 0);
        const prev = threads[index]!;
        setSelectedId(prev.id);
        setSelection((s) => extendSelection(s.ids.size === 0 && selectedThread ? toggleSelected(s, selectedThread.id) : s, orderedIds, prev.id));
      },
    },
```

    `escape`: `if (selection.ids.size > 0) setSelection(clearSelection()); else if (openThreadId) closeThread(); else if (q) setQ('');`
  - Bulk-aware actions: `archiveSelected` and `markRead` check the selection first:

```tsx
  const bulkArchive = React.useCallback(() => {
    if (selectedIds.length === 0) return;
    const ids = selectedIds;
    setSelection(clearSelection());
    void bulkAct(ids, 'archive');
    toast.success(`Archived ${ids.length} conversations`, {
      action: { label: 'Undo', onClick: () => void undoLast() },
    });
  }, [selectedIds, bulkAct, undoLast]);

  const bulkMarkRead = React.useCallback(() => {
    if (selectedIds.length === 0) return;
    const ids = selectedIds;
    setSelection(clearSelection());
    void bulkAct(ids, 'read');
    toast.success(`Marked ${ids.length} read`);
  }, [selectedIds, bulkAct]);
```

    In the `e` handler: `selection.ids.size > 0 ? bulkArchive() : archiveSelected()`; in `shift+i`: `selection.ids.size > 0 ? bulkMarkRead() : markRead()`.
  - Clear stale selections when the thread list changes (reuse the existing `threads` effect): drop selected ids no longer present.
  - Render: selected rows get a visible state — pass `bulkSelected={selection.ids.has(thread.id)}` into `ThreadRow` and render a `bg-primary/10` row tint plus a left checkbox dot; mount `<BulkBar count={selection.ids.size} onArchive={bulkArchive} onMarkRead={bulkMarkRead} onLabel={() => setLabelPickerOpen(true)} onUnsubscribe={() => {}} onClear={() => setSelection(clearSelection())} />` inside the list `<section>` (which needs `relative`). (`setLabelPickerOpen` arrives in Task 14 — until that task, wire `onLabel` to a no-op and update in Task 14.)
- [ ] Write the failing e2e `apps/web/e2e/bulk-triage.spec.ts`:

```ts
import { expect, test } from './fixtures';

test.describe('Bulk triage', () => {
  test('x selects, shift+j extends, e bulk-archives, z undoes', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await expect(first).toBeVisible();
    await first.hover();

    await page.keyboard.press('x');
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.keyboard.press('Shift+j');
    await expect(page.getByText('2 selected')).toBeVisible();

    await page.keyboard.press('e');
    await expect(page.getByText(/Archived 2 conversations/)).toBeVisible();
    await expect(page.getByText(/selected/)).toHaveCount(0);

    await page.keyboard.press('z');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();
  });

  test('escape clears the selection without closing anything', async ({ page }) => {
    await page.goto('/mail');
    const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
    await first.hover();
    await page.keyboard.press('x');
    await expect(page.getByText('1 selected')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByText(/selected/)).toHaveCount(0);
  });
});
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run components/app/bulk-bar.test.tsx && bunx playwright test e2e/bulk-triage.spec.ts e2e/mail-triage.spec.ts` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): bulk range selection with x/shift+j/k and bulk action bar"`

---

### Task 14: Keyboard label picker (L) + palette integration

**Files:**
- Create: `apps/web/components/app/label-picker.tsx`
- Modify: `apps/web/app/(app)/mail/page.tsx` (`l` shortcut, picker mount, BulkBar onLabel), `apps/web/lib/mail-utils.ts` (MailCommand `'label'`), `apps/web/components/app/command-palette.tsx` (Label entry)
- Test: Create `apps/web/components/app/label-picker.test.tsx`; extend `apps/web/e2e/bulk-triage.spec.ts`

**Interfaces:**

```tsx
export interface LabelPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  labels: Label[];
  /** Label ids already on the (single) target thread; enables toggle display. */
  activeLabelIds: ReadonlySet<string>;
  /** Called with the chosen label and whether to add or remove it. */
  onPick: (label: Label, add: boolean) => void;
}
export function LabelPicker(props: LabelPickerProps): React.JSX.Element;
```

- Consumes: `useLabels()`, `setLabel`, `bulkAct` (Task 11), shadcn `CommandDialog` primitives (same imports as `command-palette.tsx`).

**Steps:**

- [ ] Write the failing component test `apps/web/components/app/label-picker.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { Label } from '@calendium/shared';

import { LabelPicker } from './label-picker';

const labels: Label[] = [
  { id: 'lbl_updates', accountId: 'acc', name: 'Updates', kind: 'user', color: null },
  { id: 'lbl_travel', accountId: 'acc', name: 'Travel', kind: 'user', color: null },
];

describe('LabelPicker', () => {
  it('lists labels and picks one with add=true when not active', async () => {
    const onPick = vi.fn();
    render(
      <LabelPicker open onOpenChange={() => {}} labels={labels} activeLabelIds={new Set()} onPick={onPick} />
    );
    await userEvent.click(screen.getByText('Updates'));
    expect(onPick).toHaveBeenCalledWith(labels[0], true);
  });

  it('picks with add=false when the label is already active', async () => {
    const onPick = vi.fn();
    render(
      <LabelPicker
        open
        onOpenChange={() => {}}
        labels={labels}
        activeLabelIds={new Set(['lbl_travel'])}
        onPick={onPick}
      />
    );
    await userEvent.click(screen.getByText('Travel'));
    expect(onPick).toHaveBeenCalledWith(labels[1], false);
  });

  it('filters labels as you type', async () => {
    render(
      <LabelPicker open onOpenChange={() => {}} labels={labels} activeLabelIds={new Set()} onPick={() => {}} />
    );
    await userEvent.type(screen.getByPlaceholderText('Label as…'), 'trav');
    expect(screen.queryByText('Updates')).not.toBeInTheDocument();
    expect(screen.getByText('Travel')).toBeInTheDocument();
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run components/app/label-picker.test.tsx` — expected: `Cannot find module './label-picker'`.
- [ ] Implement `apps/web/components/app/label-picker.tsx`:

```tsx
'use client';

import * as React from 'react';
import type { Label } from '@calendium/shared';
import { Check, Tag } from 'lucide-react';

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';

export interface LabelPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  labels: Label[];
  activeLabelIds: ReadonlySet<string>;
  onPick: (label: Label, add: boolean) => void;
}

/** `L` — keyboard-first label picker for the selected thread or bulk range. */
export function LabelPicker({ open, onOpenChange, labels, activeLabelIds, onPick }: LabelPickerProps) {
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder="Label as…" />
      <CommandList>
        <CommandEmpty>No labels found.</CommandEmpty>
        <CommandGroup heading="Labels">
          {labels.map((label) => {
            const active = activeLabelIds.has(label.id);
            return (
              <CommandItem
                key={label.id}
                value={label.name}
                onSelect={() => {
                  onPick(label, !active);
                  onOpenChange(false);
                }}
              >
                <Tag className="size-4" />
                <span>{label.name}</span>
                {active && <Check className="ml-auto size-4" aria-label="Applied" />}
              </CommandItem>
            );
          })}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
```

- [ ] Wire into `mail/page.tsx`:

```tsx
  const [labelPickerOpen, setLabelPickerOpen] = React.useState(false);
  const labelsQuery = useLabels();
  const activeLabelIds = React.useMemo(
    () => new Set(selectedIds.length === 0 ? (selectedThread?.labelIds ?? []) : []),
    [selectedIds, selectedThread]
  );

  const pickLabel = React.useCallback(
    (label: Label, add: boolean) => {
      if (selectedIds.length > 0) {
        const ids = selectedIds;
        setSelection(clearSelection());
        void bulkAct(ids, add ? 'label' : 'unlabel', label.id);
        toast.success(`${add ? 'Labeled' : 'Unlabeled'} ${ids.length} conversations “${label.name}”`);
      } else if (selectedThread) {
        void setLabel(selectedThread.id, label.id, add);
        toast.success(add ? `Labeled “${label.name}”` : `Removed “${label.name}”`);
      }
    },
    [selectedIds, selectedThread, bulkAct, setLabel]
  );
```

  Shortcut `{ keys: 'l', description: 'Label', handler: () => (selectedThread || selectedIds.length > 0) && setLabelPickerOpen(true) }`; mount `<LabelPicker open={labelPickerOpen} onOpenChange={setLabelPickerOpen} labels={labelsQuery.data?.labels ?? []} activeLabelIds={activeLabelIds} onPick={pickLabel} />`; BulkBar `onLabel={() => setLabelPickerOpen(true)}`. Also bind `s` star to bulk when a selection exists (`selection.ids.size > 0 ? bulkAct(selectedIds,'star') : toggleStar()` — keeps stars keyboard-driven for ranges too). `mail-utils.ts`: add `'label'` to `MailCommand`; page `runCommand` opens the picker; palette gains a `Label conversation… <Kbd>L</Kbd>` item dispatching `'label'`.
- [ ] Extend `apps/web/e2e/bulk-triage.spec.ts`:

```ts
test('L labels the hovered conversation from the picker', async ({ page }) => {
  await page.goto('/mail');
  const first = page.getByRole('button', { name: /Postmortem: checkout latency spike/ });
  await first.hover();
  await page.keyboard.press('l');
  await expect(page.getByPlaceholder('Label as…')).toBeVisible();
  await page.getByText('Updates', { exact: true }).click();
  await expect(page.getByText(/Labeled “Updates”/)).toBeVisible();
});
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run && bunx playwright test e2e/bulk-triage.spec.ts` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): keyboard label picker on L for single and bulk targets"`

---

### Task 15: Auto-advance after archive / snooze / trash

**Files:**
- Modify: `apps/web/app/(app)/mail/page.tsx`, `apps/web/components/app/thread-view.tsx` (archive path lifted to the page)
- Test: Modify `apps/web/e2e/mail-triage.spec.ts`

**Interfaces:**
- `ThreadView` gains `onArchive?: () => void` — when provided, its Archive button calls it INSTEAD of running its own `act(threadId,'archive') + onClose`, so the page owns advance + toast + undo for every archive path (key, palette, and mouse in the open-thread view).
- Consumes: `nextAfterRemoval` from `@calendium/shared`.

**Steps:**

- [ ] Update the e2e expectation FIRST (this is the failing test). In `apps/web/e2e/mail-triage.spec.ts`, replace the tail of `'loads the inbox and archives a thread from the thread view'`:

```ts
    await page.getByRole('button', { name: 'Archive' }).click();

    await expect(page.getByText('Archived', { exact: true })).toBeVisible();
    // Auto-advance: the archived thread leaves the list AND the next
    // conversation opens in the thread pane instead of returning to the list.
    await expect(page.getByRole('button', { name: /Renewal terms for FY27/ })).toHaveCount(0);
    await expect(page.getByRole('heading', { level: 1 })).not.toHaveText(/Renewal terms for FY27/);
    await expect(page).toHaveURL(/t=/); // a thread is still open
```

  and add:

```ts
  test('snooze auto-advances to the next conversation', async ({ page }) => {
    await page.goto('/mail');
    const target = page.getByRole('button', { name: /Renewal terms for FY27/ });
    await target.hover();
    await page.keyboard.press('h');
    await page.getByText('Tomorrow morning').click();
    await expect(page.getByText(/Snoozed until/)).toBeVisible();
    await expect(page.getByRole('button', { name: /Renewal terms for FY27/ })).toHaveCount(0);
  });
```

  (Use the actual first option label rendered by `snoozeOptions()` — `'Tomorrow morning'` exists in `lib/mail-utils.ts`; verify with `bunx vitest run lib/mail-utils.test.ts` if unsure.)
- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/mail-triage.spec.ts` — expected: the updated archive test fails on the `t=` URL assertion (current behavior closes the pane when archiving the open thread … or passes for the keyboard path — the mouse path in ThreadView closes; the assertion pins the new contract).
- [ ] Implement:
  - In `mail/page.tsx`, generalize the advance logic with the shared helper:

```tsx
  const advancePastRemoved = React.useCallback(
    (removedId: string) => {
      const nextId = nextAfterRemoval(orderedIds, removedId);
      setSelectedId(nextId);
      if (openThreadId === removedId) navigate({ t: nextId });
    },
    [orderedIds, openThreadId, navigate]
  );
```

    `archiveSelected` uses it (replacing its inline next/prev logic). The snooze `TimePickerDialog.onPick` becomes:

```tsx
        onPick={(when) => {
          if (!selectedThread) return;
          const id = selectedThread.id;
          advancePastRemoved(id);
          void snooze(id, when.toISOString());
          toast.success(`Snoozed until ${formatOptionTime(when)}`, {
            action: { label: 'Undo', onClick: () => void undoLast() },
          });
        }}
```

  - Add a trash shortcut with the same advance semantics: `{ keys: '#', description: 'Trash', handler: () => { if (!selectedThread) return; const id = selectedThread.id; advancePastRemoved(id); void act(id, 'trash'); toast.success('Deleted', { action: { label: 'Undo', onClick: () => void undoLast() } }); } }`.
  - `ThreadView`: add the `onArchive?: () => void` prop; its Archive button becomes `onClick={() => (onArchive ? onArchive() : /* existing self-archive fallback */ selfArchive())}`. The page passes `onArchive={archiveSelected}` (the open thread IS the selected thread — the existing selection-follows-open effect guarantees it; keep the fallback for other callers).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/ && bunx vitest run` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): auto-advance to the next conversation after archive, snooze, and trash"`

---

### Task 16: Get Me To Zero flow

**Files:**
- Create: `apps/web/components/app/get-me-to-zero.tsx`
- Modify: `apps/web/app/(app)/mail/page.tsx` (mount + command), `apps/web/lib/mail-utils.ts` (MailCommand `'get-me-to-zero'` + `zeroCutoffOptions`), `apps/web/components/app/command-palette.tsx` (palette entry)
- Test: Modify `apps/web/lib/mail-utils.test.ts`; create `apps/web/e2e/get-me-to-zero.spec.ts`

**Interfaces:**
- `mail-utils.ts`:

```ts
/** Get Me To Zero cutoffs: archive inbox mail older than these periods. */
export function zeroCutoffOptions(now: Date = new Date()): TimeOption[];
// ids/labels: 'week' "1 week", 'two-weeks' "2 weeks", 'month' "1 month", 'quarter' "3 months";
// each `when` is now minus the period (date-fns subDays/subMonths).
```

- Component:

```tsx
export interface GetMeToZeroProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone?: (archivedCount: number) => void;
}
export function GetMeToZero(props: GetMeToZeroProps): React.JSX.Element;
```

  Uses `TimePickerDialog`-style option list (reuse `TimePickerDialog` from `snooze-menu.tsx` with `zeroCutoffOptions()` and title `"Archive everything older than…"`), calling `getMeToZero(option.when.toISOString())` from `useMailActions()` and toasting `Archived N conversations — welcome to zero`.

**Steps:**

- [ ] Write the failing util test (append to `apps/web/lib/mail-utils.test.ts`):

```ts
describe('zeroCutoffOptions', () => {
  it('returns week/two-weeks/month/quarter cutoffs in the past', () => {
    const now = new Date('2026-07-17T12:00:00Z');
    const options = zeroCutoffOptions(now);
    expect(options.map((o) => o.id)).toEqual(['week', 'two-weeks', 'month', 'quarter']);
    for (const option of options) {
      expect(option.when.getTime()).toBeLessThan(now.getTime());
    }
    expect(options[0]!.when.toISOString()).toBe('2026-07-10T12:00:00.000Z');
  });
});
```

  (add `zeroCutoffOptions` to the file's imports).
- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/mail-utils.test.ts` — expected: import error.
- [ ] Implement `zeroCutoffOptions` in `mail-utils.ts` (`import { subDays, subMonths } from 'date-fns'`):

```ts
export function zeroCutoffOptions(now: Date = new Date()): TimeOption[] {
  return [
    { id: 'week', label: '1 week', when: subDays(now, 7) },
    { id: 'two-weeks', label: '2 weeks', when: subDays(now, 14) },
    { id: 'month', label: '1 month', when: subMonths(now, 1) },
    { id: 'quarter', label: '3 months', when: subMonths(now, 3) },
  ];
}
```

  Add `'get-me-to-zero'` to `MailCommand`.
- [ ] Implement `apps/web/components/app/get-me-to-zero.tsx`:

```tsx
'use client';

import * as React from 'react';
import { toast } from 'sonner';

import { TimePickerDialog } from '@/components/app/snooze-menu';
import { zeroCutoffOptions } from '@/lib/mail-utils';
import { useMailActions } from '@/lib/use-mail';

export interface GetMeToZeroProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone?: (archivedCount: number) => void;
}

/** "Get Me To Zero": one command bulk-archives inbox mail older than a period. */
export function GetMeToZero({ open, onOpenChange, onDone }: GetMeToZeroProps) {
  const { getMeToZero } = useMailActions();
  return (
    <TimePickerDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Archive everything older than…"
      options={zeroCutoffOptions()}
      onPick={(when) => {
        void getMeToZero(when.toISOString())
          .then((count) => {
            toast.success(
              count === 0
                ? 'Nothing that old — you were already close to zero'
                : `Archived ${count} conversation${count === 1 ? '' : 's'} — welcome to zero`
            );
            onDone?.(count);
          })
          .catch(() => toast.error('Could not run Get Me To Zero.'));
      }}
    />
  );
}
```

  (If `TimePickerDialog`'s `onPick` receives the option's `when: Date` — it does on the mail page — keep this signature; adjust to its exact prop shape when wiring.) Mount in `mail/page.tsx` with `const [zeroOpen, setZeroOpen] = React.useState(false);`, handle `runCommand` case `'get-me-to-zero'` → `setZeroOpen(true)`, and add the palette item `Get Me To Zero` (icon `Inbox`) dispatching/queueing `'get-me-to-zero'`.
- [ ] Write the failing e2e `apps/web/e2e/get-me-to-zero.spec.ts`:

```ts
import { expect, openCommandPalette, test } from './fixtures';

test.describe('Get Me To Zero', () => {
  test('runs from the palette and reports the archived count', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();

    await openCommandPalette(page);
    await page.getByPlaceholder('Type a command or search…').fill('zero');
    await page.getByText('Get Me To Zero').click();

    await expect(page.getByText('Archive everything older than…')).toBeVisible();
    await page.getByText('1 week', { exact: true }).click();
    // Demo threads are hours-to-days old; a 1-week cutoff archives none, and
    // the honest empty-result copy shows. The flow itself is what's asserted.
    await expect(page.getByText(/already close to zero|welcome to zero/)).toBeVisible();
  });
});
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/mail-utils.test.ts && bunx playwright test e2e/get-me-to-zero.spec.ts` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): Get Me To Zero bulk-archive flow with period picker"`

---

### Task 17: One-click + bulk unsubscribe UI

**Files:**
- Modify: `apps/web/components/app/thread-view.tsx` (Unsubscribe button), `apps/web/app/(app)/mail/page.tsx` (BulkBar onUnsubscribe)
- Test: Extend `apps/web/e2e/bulk-triage.spec.ts`

**Interfaces:**
- Consumes: `Thread.unsubscribeMailto/unsubscribeUrl/unsubscribeOneClick` (Task 9), `useMailActions().unsubscribe` (Task 11), `bulkAct`.
- Produces: in `thread-view.tsx` an `Unsubscribe` header button rendered only when `thread.unsubscribeMailto || thread.unsubscribeUrl`; on click:

```tsx
  const handleUnsubscribe = React.useCallback(async () => {
    try {
      const res = await unsubscribe(thread.id);
      if (res.method === 'link' && res.url) {
        window.open(res.url, '_blank', 'noopener,noreferrer');
        toast.message('Opened the unsubscribe page in a new tab');
      } else {
        toast.success('Unsubscribed — the sender has been asked to stop');
      }
    } catch {
      toast.error('Could not unsubscribe.');
    }
  }, [thread.id, unsubscribe]);
```

- Bulk path on the mail page: `BulkBar.onUnsubscribe` iterates the selected threads that carry unsubscribe info, awaits `unsubscribe(id)` for each (best-effort, `Promise.allSettled`), then bulk-archives those same ids (`bulkAct(ids, 'archive')` — Superhuman semantics: unsubscribe + clear the backlog in one gesture) and toasts `Unsubscribed from N senders` with an Undo action for the archive part (`undoLast`).

**Steps:**

- [ ] Write the failing e2e (append to `apps/web/e2e/bulk-triage.spec.ts`):

```ts
test('unsubscribe button appears on newsletter threads and confirms', async ({ page }) => {
  await page.goto('/mail?split=news');
  const newsletter = page.getByRole('button', { name: /The Batch|Stratechery/ }).first();
  await expect(newsletter).toBeVisible();
  await newsletter.click();
  const unsub = page.getByRole('button', { name: 'Unsubscribe' });
  await expect(unsub).toBeVisible();
  await unsub.click();
  await expect(page.getByText(/Unsubscribed|unsubscribe page/)).toBeVisible();
});

test('bulk unsubscribe archives the selected newsletters', async ({ page }) => {
  await page.goto('/mail?split=news');
  const first = page.getByRole('button', { name: /The Batch/ });
  await first.hover();
  await page.keyboard.press('x');
  await page.keyboard.press('Shift+j');
  await page.getByRole('button', { name: 'Unsubscribe' }).click();
  await expect(page.getByText(/Unsubscribed from 2 senders/)).toBeVisible();
  await expect(page.getByRole('button', { name: /The Batch/ })).toHaveCount(0);
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/bulk-triage.spec.ts` — expected: the two new tests fail (no Unsubscribe button exists).
- [ ] Implement the ThreadView button (place it in the header action row next to Archive, `MailX` icon, `variant="ghost"` sizing consistent with siblings) and the page-level bulk handler:

```tsx
  const bulkUnsubscribe = React.useCallback(async () => {
    const targets = threads.filter(
      (t) => selection.ids.has(t.id) && (t.unsubscribeMailto || t.unsubscribeUrl)
    );
    if (targets.length === 0) {
      toast.message('No unsubscribe links in the selection');
      return;
    }
    const ids = targets.map((t) => t.id);
    setSelection(clearSelection());
    await Promise.allSettled(ids.map((id) => unsubscribe(id)));
    void bulkAct(ids, 'archive');
    toast.success(`Unsubscribed from ${ids.length} sender${ids.length === 1 ? '' : 's'}`, {
      action: { label: 'Undo', onClick: () => void undoLast() },
    });
  }, [threads, selection, unsubscribe, bulkAct, undoLast]);
```

  wired as `onUnsubscribe={() => void bulkUnsubscribe()}` on `BulkBar` (replacing the Task 13 no-op).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/bulk-triage.spec.ts && bunx vitest run` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): one-click and bulk unsubscribe with archive-backlog semantics"`

---

### Task 18: Reorderable splits

**Files:**
- Modify: `apps/web/lib/mail-utils.ts` (`orderSplits`), `apps/web/app/(app)/mail/page.tsx` (ordered tabs), `apps/web/app/(app)/settings/page.tsx` (Split order section)
- Test: Modify `apps/web/lib/mail-utils.test.ts`; extend `apps/web/e2e/settings.spec.ts`

**Interfaces:**

```ts
// mail-utils.ts
export interface SplitTab { value: InboxSplit; label: string }
/**
 * Orders split tabs by the user's saved preference; splits missing from the
 * preference keep their default relative order after the preferred ones.
 * An empty preference returns the default order unchanged.
 */
export function orderSplits(defaults: readonly SplitTab[], preferred: readonly InboxSplit[]): SplitTab[];
```

- Consumes: `usePrefs` / `useUpdatePrefs` (Task 11).
- Settings UI: a "Split order" card listing the seven splits with per-row Up/Down buttons (`aria-label="Move Important up"` etc.) that call `useUpdatePrefs` with the new full order — keyboard-accessible, no drag dependency.

**Steps:**

- [ ] Write the failing util test (append to `mail-utils.test.ts`):

```ts
describe('orderSplits', () => {
  const defaults = [
    { value: 'important', label: 'Important' },
    { value: 'vip', label: 'VIP' },
    { value: 'team', label: 'Team' },
  ] as const;

  it('returns defaults for an empty preference', () => {
    expect(orderSplits(defaults, []).map((s) => s.value)).toEqual(['important', 'vip', 'team']);
  });

  it('puts preferred splits first, in preference order', () => {
    expect(orderSplits(defaults, ['team', 'important']).map((s) => s.value)).toEqual([
      'team',
      'important',
      'vip',
    ]);
  });

  it('ignores unknown splits in the preference', () => {
    expect(orderSplits(defaults, ['calendar', 'vip'] as never).map((s) => s.value)).toEqual([
      'vip',
      'important',
      'team',
    ]);
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/mail-utils.test.ts` — expected: import error for `orderSplits`.
- [ ] Implement in `mail-utils.ts`:

```ts
export interface SplitTab {
  value: InboxSplit;
  label: string;
}

export function orderSplits(
  defaults: readonly SplitTab[],
  preferred: readonly InboxSplit[]
): SplitTab[] {
  const byValue = new Map(defaults.map((s) => [s.value, s]));
  const head = preferred.map((v) => byValue.get(v)).filter((s): s is SplitTab => s !== undefined);
  const headSet = new Set(head.map((s) => s.value));
  return [...head, ...defaults.filter((s) => !headSet.has(s.value))];
}
```

  In `mail/page.tsx`: `const prefs = usePrefs(); const splits = React.useMemo(() => orderSplits(SPLITS, prefs.data?.prefs.splitOrder ?? []), [prefs.data]);` and render `splits.map(...)` in the `TabsList` instead of `SPLITS.map(...)`.
- [ ] Implement the settings section (inside the existing settings page structure, as a new card titled `Split order` with description `Reorder your inbox splits — the first split is your landing tab.`): local state seeded from `usePrefs`, rows rendered from `orderSplits(SPLITS, order)`, Up/Down `Button size="icon" variant="ghost"` handlers that swap adjacent entries and call `updatePrefs({ splitOrder: nextOrder })`. Import `SPLITS`-equivalent list locally (export the `SPLITS` array from `mail-utils.ts` as `DEFAULT_SPLITS` and reuse it in both files to avoid duplication; update `mail/page.tsx` to import it).
- [ ] Extend `apps/web/e2e/settings.spec.ts`:

```ts
test('split order can be reordered and survives navigation to mail', async ({ page }) => {
  await page.goto('/settings');
  await expect(page.getByText('Split order')).toBeVisible();
  await page.getByRole('button', { name: 'Move VIP up' }).click();
  await page.goto('/mail');
  const tabs = page.getByRole('tab');
  await expect(tabs.first()).toHaveText('VIP');
});
```

- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/mail-utils.test.ts && bunx playwright test e2e/settings.spec.ts` — expected: all green.
- [ ] Commit: `git add -A && git commit -m "feat(web): reorderable splits persisted in user prefs"`

---

### Task 19: Inbox-zero celebration

**Files:**
- Create: `apps/web/components/app/inbox-zero.tsx`
- Modify: `apps/web/app/(app)/mail/page.tsx` (EmptyState default branch renders the celebration)
- Test: Create `apps/web/components/app/inbox-zero.test.tsx`

**Interfaces:**

```tsx
export interface ZeroScene {
  id: string;
  headline: string;
  sub: string;
  /** Tailwind gradient classes, e.g. 'from-sky-200 via-indigo-100 to-rose-100'. */
  gradient: string;
}
export const ZERO_SCENES: readonly ZeroScene[]; // >= 5 scenes
/** Deterministic pick: rotates by day-of-year so the artwork changes daily. */
export function sceneForDate(date: Date): ZeroScene;
export function InboxZero({ date }: { date?: Date }): React.JSX.Element;
```

**Steps:**

- [ ] Write the failing test `apps/web/components/app/inbox-zero.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { InboxZero, ZERO_SCENES, sceneForDate } from './inbox-zero';

describe('inbox zero celebration', () => {
  it('has at least five scenes with copy and a gradient', () => {
    expect(ZERO_SCENES.length).toBeGreaterThanOrEqual(5);
    for (const scene of ZERO_SCENES) {
      expect(scene.headline).toBeTruthy();
      expect(scene.gradient).toMatch(/from-/);
    }
  });

  it('rotates deterministically by day of year', () => {
    const a = sceneForDate(new Date('2026-07-17T10:00:00Z'));
    const b = sceneForDate(new Date('2026-07-17T23:00:00Z'));
    const c = sceneForDate(new Date('2026-07-18T10:00:00Z'));
    expect(a.id).toBe(b.id); // same day, same scene
    expect(c.id).not.toBe(a.id); // consecutive days differ (scenes >= 2)
  });

  it('renders the scene headline and the inbox-zero badge', () => {
    const date = new Date('2026-07-17T10:00:00Z');
    render(<InboxZero date={date} />);
    expect(screen.getByText(sceneForDate(date).headline)).toBeInTheDocument();
    expect(screen.getByText("You're at Inbox Zero")).toBeInTheDocument();
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run components/app/inbox-zero.test.tsx` — expected: `Cannot find module './inbox-zero'`.
- [ ] Implement `apps/web/components/app/inbox-zero.tsx`:

```tsx
'use client';

import * as React from 'react';
import { differenceInCalendarDays, startOfYear } from 'date-fns';

import { cn } from '@/lib/utils';

export interface ZeroScene {
  id: string;
  headline: string;
  sub: string;
  gradient: string;
}

/** Rotating end-of-triage artwork — pure CSS gradients, no image payloads. */
export const ZERO_SCENES: readonly ZeroScene[] = [
  { id: 'dawn', headline: 'Clear skies ahead', sub: 'Every conversation handled. Go make something.', gradient: 'from-sky-200 via-indigo-100 to-rose-100 dark:from-sky-950 dark:via-indigo-950 dark:to-rose-950' },
  { id: 'dunes', headline: 'Nothing but calm', sub: 'Your inbox is a quiet desert. Enjoy it.', gradient: 'from-amber-100 via-orange-100 to-rose-100 dark:from-amber-950 dark:via-orange-950 dark:to-rose-950' },
  { id: 'sea', headline: 'Smooth sailing', sub: 'Zero unhandled mail. The horizon is yours.', gradient: 'from-cyan-100 via-teal-100 to-emerald-100 dark:from-cyan-950 dark:via-teal-950 dark:to-emerald-950' },
  { id: 'alpine', headline: 'Peak performance', sub: 'You cleared the whole climb. Breathe it in.', gradient: 'from-slate-100 via-sky-100 to-violet-100 dark:from-slate-900 dark:via-sky-950 dark:to-violet-950' },
  { id: 'aurora', headline: 'Lights out, inbox down', sub: 'All quiet. See you when something matters.', gradient: 'from-emerald-100 via-cyan-100 to-fuchsia-100 dark:from-emerald-950 dark:via-cyan-950 dark:to-fuchsia-950' },
];

export function sceneForDate(date: Date): ZeroScene {
  const day = differenceInCalendarDays(date, startOfYear(date));
  return ZERO_SCENES[day % ZERO_SCENES.length]!;
}

/** Full-pane celebration shown when a split hits zero (no query, no view). */
export function InboxZero({ date }: { date?: Date }) {
  const scene = sceneForDate(date ?? new Date());
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
      <div
        aria-hidden
        className={cn('h-40 w-64 rounded-2xl bg-gradient-to-br shadow-inner', scene.gradient)}
      />
      <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
        You&apos;re at Inbox Zero
      </p>
      <p className="text-lg font-semibold">{scene.headline}</p>
      <p className="text-muted-foreground max-w-xs text-sm text-balance">{scene.sub}</p>
    </div>
  );
}
```

  In `mail/page.tsx`'s `EmptyState`, return `<InboxZero />` for the default branch (no `q`, no `view`) instead of the current generic copy; keep the existing copy for search/starred/snoozed/sent/drafts branches.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run components/app/inbox-zero.test.tsx && bunx playwright test e2e/` — expected: all green (no existing e2e asserts the old default empty-state copy; if `mail-triage.spec.ts` does after Task 15 edits, update it to expect `You're at Inbox Zero`).
- [ ] Commit: `git add -A && git commit -m "feat(web): rotating inbox-zero celebration scenes"`

---

### Task 20: Shortcut-teaching toasts

**Files:**
- Create: `apps/web/lib/shortcut-hints.ts`
- Modify: `apps/web/components/app/command-palette.tsx` (teach on mail-command selection), `apps/web/components/app/thread-view.tsx` (teach on mouse Archive), `apps/web/components/app/bulk-bar.tsx` (teach on mouse bulk actions)
- Test: Create `apps/web/lib/shortcut-hints.test.ts`

**Interfaces:**

```ts
/**
 * Superhuman-style shortcut teaching: when an action is invoked via mouse or
 * palette, show a one-line toast naming its keyboard shortcut. Each hint
 * fires at most once per session (module-level memory) so it teaches without
 * nagging. `resetShortcutHints` exists for tests.
 */
export function teachShortcut(actionId: string, keys: string, actionLabel: string): void;
export function resetShortcutHints(): void;
```

- Produces toast copy: `Tip: press E to Archive` (via `toast.message` — `sonner`, already the app's toaster).

**Steps:**

- [ ] Write the failing test `apps/web/lib/shortcut-hints.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest';

const message = vi.fn();
vi.mock('sonner', () => ({ toast: { message: (...args: unknown[]) => message(...args) } }));

import { resetShortcutHints, teachShortcut } from './shortcut-hints';

describe('teachShortcut', () => {
  beforeEach(() => {
    message.mockClear();
    resetShortcutHints();
  });

  it('toasts the shortcut for the action', () => {
    teachShortcut('archive', 'E', 'Archive');
    expect(message).toHaveBeenCalledTimes(1);
    expect(String(message.mock.calls[0]![0])).toContain('E');
    expect(String(message.mock.calls[0]![0])).toContain('Archive');
  });

  it('fires at most once per action per session', () => {
    teachShortcut('archive', 'E', 'Archive');
    teachShortcut('archive', 'E', 'Archive');
    expect(message).toHaveBeenCalledTimes(1);
    teachShortcut('label', 'L', 'Label');
    expect(message).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run lib/shortcut-hints.test.ts` — expected: `Cannot find module './shortcut-hints'`.
- [ ] Implement `apps/web/lib/shortcut-hints.ts`:

```ts
import { toast } from 'sonner';

const shown = new Set<string>();

export function teachShortcut(actionId: string, keys: string, actionLabel: string): void {
  if (shown.has(actionId)) return;
  shown.add(actionId);
  toast.message(`Tip: press ${keys} to ${actionLabel}`);
}

export function resetShortcutHints(): void {
  shown.clear();
}
```

- [ ] Wire the mouse/palette paths (keyboard paths never call it):
  - `command-palette.tsx`: when a mail-command item is selected, call the matching hint before dispatch — `teachShortcut('archive', 'E', 'Archive')`, `('snooze', 'H', 'Snooze')`, `('star', 'S', 'Star')`, `('mark-read', '⇧I', 'Mark read')`, `('label', 'L', 'Label')`, `('undo', 'Z', 'Undo')`, `('search', '/', 'Search')`.
  - `thread-view.tsx`: the Archive button's click handler calls `teachShortcut('archive', 'E', 'Archive')`; the Unsubscribe button needs no hint (no shortcut).
  - `bulk-bar.tsx`: Archive → `('archive', 'E', 'Archive')`, Mark read → `('mark-read', '⇧I', 'Mark read')`, Label → `('label', 'L', 'Label')`.
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx vitest run && bunx playwright test e2e/` — expected: all green (existing e2e that clicks ThreadView Archive still passes — `toast.message` renders alongside the `Archived` success toast; if a strict-text assertion collides, scope it with `{ exact: true }` as the current spec already does).
- [ ] Commit: `git add -A && git commit -m "feat(web): shortcut-teaching toasts for mouse and palette actions"`

---

### Task 21: Sub-100ms perf harness in the CI e2e suite

**Files:**
- Create: `apps/web/e2e/perf.spec.ts`
- Modify: `apps/web/playwright.config.ts` (production server in CI)
- Test: the spec IS the test; it runs in the existing `.github/workflows/test.yml` e2e job (`bun run test:e2e`) with no workflow changes.

**Interfaces:**
- Budget contract: p95 of list-navigation keydown→paint samples < 100ms; p95 of archive keydown→paint < 100ms. Samples measured in-page: dispatch a real `KeyboardEvent`, then await a double `requestAnimationFrame` (commit + paint) and record `performance.now()` deltas.
- `playwright.config.ts` change: dev-mode Next is unoptimized and would make the budget meaningless, so CI builds and serves production:

```ts
  webServer: {
    command: process.env.CI
      ? `node_modules/.bin/next build && node_modules/.bin/next start -p ${PORT}`
      : `node_modules/.bin/next dev -p ${PORT}`,
    // (url/timeout/env unchanged — NEXT_PUBLIC_* are inlined at build time and
    // the env block applies to the build command too, so demo mode holds.)
```

  Raise `timeout` to `240_000` to cover the CI build.

**Steps:**

- [ ] Write the failing-until-measured spec `apps/web/e2e/perf.spec.ts`:

```ts
import { expect, test } from './fixtures';

/**
 * Sub-100ms interaction budget (feature map: Speed). Measures real keydown →
 * next-paint latency inside the page: dispatch a KeyboardEvent, then await a
 * double requestAnimationFrame so React has committed and the frame painted.
 * Asserts on p95 so one GC pause can't flake the suite, with warmup discarded.
 */

function p95(samples: number[]): number {
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * 0.95))]!;
}

async function measureKeys(
  page: import('@playwright/test').Page,
  keys: string[],
  rounds: number
): Promise<number[]> {
  return page.evaluate(
    async ({ keys, rounds }) => {
      const samples: number[] = [];
      const frame = () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
        );
      for (let i = 0; i < rounds; i++) {
        const key = keys[i % keys.length]!;
        const start = performance.now();
        window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
        await frame();
        samples.push(performance.now() - start);
      }
      return samples;
    },
    { keys, rounds }
  );
}

test.describe('interaction budget', () => {
  test('j/k list navigation p95 stays under 100ms', async ({ page }) => {
    await page.goto('/mail');
    await expect(page.getByRole('button', { name: /Postmortem: checkout latency spike/ })).toBeVisible();

    await measureKeys(page, ['j', 'k'], 10); // warmup, discarded
    const samples = await measureKeys(page, ['j', 'k'], 40);
    const budget = p95(samples);
    test.info().annotations.push({ type: 'perf', description: `j/k p95=${budget.toFixed(1)}ms` });
    expect(budget).toBeLessThan(100);
  });

  test('archive (e) p95 stays under 100ms', async ({ page }) => {
    await page.goto('/mail?split=news');
    await expect(page.getByRole('button', { name: /The Batch/ })).toBeVisible();

    // Archive then undo, repeatedly, so the list never runs dry mid-measure.
    const samples: number[] = [];
    for (let i = 0; i < 12; i++) {
      const [archive] = await measureKeys(page, ['e'], 1);
      samples.push(archive!);
      await measureKeys(page, ['z'], 1); // undo restores the row (not measured)
    }
    const budget = p95(samples.slice(2)); // first two are warmup
    test.info().annotations.push({ type: 'perf', description: `archive p95=${budget.toFixed(1)}ms` });
    expect(budget).toBeLessThan(100);
  });
});
```

- [ ] Run against the DEV server first to validate the harness mechanics: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && bunx playwright test e2e/perf.spec.ts` — expected: tests execute and report p95 annotations; they MAY fail the 100ms assertion under `next dev` (unoptimized) — that is the "verify it fails / measures" step.
- [ ] Apply the `playwright.config.ts` CI production-server change above, then verify the production path locally: `cd /Users/guilherme/Dev/pessoal/calendium/apps/web && CI=1 bunx playwright test e2e/perf.spec.ts` — expected: build runs, both budget tests pass with p95 well under 100ms.
- [ ] Run the whole suite the way CI will: `cd /Users/guilherme/Dev/pessoal/calendium && CI=1 bun run test:e2e` — expected: every spec (including all suites added in Tasks 12–19) passes against the production build; fix any dev-only assumptions this exposes (e.g. compile-latency timeouts in `command-palette.spec.ts` can stay — they are upper bounds).
- [ ] Commit: `git add -A && git commit -m "test(web): enforce sub-100ms triage interaction budget in Playwright e2e"`

---

### Task 22: Desktop parity — bulk selection, undo, auto-advance, zero state

**Files:**
- Create: `apps/desktop/frontend/src/lib/triage.ts`, `apps/desktop/frontend/src/lib/triage.test.ts`
- Modify: `apps/desktop/frontend/src/views/InboxView.tsx`, `apps/desktop/frontend/src/views/ThreadPane.tsx` (archive path calls the view's advance handler)

**Interfaces:**
- `apps/desktop/frontend/src/lib/triage.ts` — thin desktop faca over the shared core plus the undo singleton and inverse map (desktop has no TanStack Query; it mutates its local state arrays):

```ts
import {
  EMPTY_SELECTION,
  UndoStack,
  clearSelection,
  extendSelection,
  nextAfterRemoval,
  toggleSelected,
  type Selection,
  type ThreadAction,
} from '@calendium/shared';

export { EMPTY_SELECTION, clearSelection, extendSelection, nextAfterRemoval, toggleSelected };
export type { Selection };

export const inboxUndo = new UndoStack();

export const ACTION_INVERSE: Partial<Record<ThreadAction, ThreadAction>> = {
  archive: 'move_to_inbox',
  trash: 'move_to_inbox',
  spam: 'move_to_inbox',
  star: 'unstar',
  unstar: 'star',
  read: 'unread',
  unread: 'read',
  move_to_inbox: 'archive',
};
```

- `InboxView.tsx` behavior additions (matching web key map): `x` toggle select, `shift+j`/`shift+k` extend, `e` archives selection or current with auto-advance (`nextAfterRemoval`), `shift+i` bulk/single mark-read via `client.bulkThreadAction` / `client.actOnThread`, `z` pops `inboxUndo`, empty inbox renders a `You're at Inbox Zero` panel (text + gradient div, mirroring web's scenes at reduced fidelity). Every triage mutation pushes its inverse (`client.actOnThread(id, ACTION_INVERSE[action])` or inverse `bulkThreadAction`) onto `inboxUndo` and refreshes the local thread list on undo. Demo fallback: only when the view is already running on `lib/mock.ts` data (its existing demo contract) do mutations mutate the mock arrays instead.

**Steps:**

- [ ] Write the failing test `apps/desktop/frontend/src/lib/triage.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
  ACTION_INVERSE,
  EMPTY_SELECTION,
  extendSelection,
  inboxUndo,
  nextAfterRemoval,
  toggleSelected,
} from './triage';

describe('desktop triage facade', () => {
  it('re-exports a working selection model', () => {
    const sel = extendSelection(toggleSelected(EMPTY_SELECTION, 'a'), ['a', 'b', 'c'], 'c');
    expect([...sel.ids].sort()).toEqual(['a', 'b', 'c']);
  });

  it('advances past a removed row', () => {
    expect(nextAfterRemoval(['a', 'b'], 'a')).toBe('b');
  });

  it('maps every destructive action to an inverse', () => {
    expect(ACTION_INVERSE.archive).toBe('move_to_inbox');
    expect(ACTION_INVERSE.read).toBe('unread');
  });

  it('exposes a shared undo stack singleton', () => {
    inboxUndo.clear();
    inboxUndo.push({ label: 'x', undo: () => {} });
    expect(inboxUndo.size).toBe(1);
    inboxUndo.clear();
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:desktop` — expected: `Cannot find module './triage'`.
- [ ] Implement `lib/triage.ts` exactly as in **Interfaces**, then wire `InboxView.tsx`:
  - Selection state `const [selection, setSelection] = useState(EMPTY_SELECTION);` beside its existing cursor index; key handlers added to the view's existing keydown listener (it already implements `j/k/e` — extend that switch): `x`, `J` (shift), `K` (shift), `z`, `shift+I`.
  - Archive path: compute `const nextId = nextAfterRemoval(ids, targetId)` before mutating, move the cursor/open-thread to `nextId`, call the API (or mock mutator in demo), push the inverse onto `inboxUndo`, show the existing toast helper (`lib/toast.ts`) with an `Undo` affordance where supported or plain text `Archived — Z to undo`.
  - Bulk: when `selection.ids.size > 0`, `e`/`shift+i` call `getClient().bulkThreadAction({ threadIds, action })`, clear the selection, refresh the list, and push the inverse bulk entry. Render a `{n} selected — E archive · ⇧I read · Esc clear` status strip above the list when active.
  - Empty state: when the loaded thread list is empty and no filter/search is active, render the zero panel (`You're at Inbox Zero` + `The desk is clear.` + gradient block).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:desktop && bun run typecheck` — expected: green (the desktop frontend compiles under its own tsconfig via vitest; also run `cd apps/desktop && wails build` if the toolchain is present to confirm the app still builds — optional locally, CI covers Go lint).
- [ ] Commit: `git add -A && git commit -m "feat(desktop): bulk triage, undo, auto-advance, and inbox-zero parity"`

---

### Task 23: Mobile parity — Get Me To Zero, unsubscribe, inbox-zero, auto-advance

**Files:**
- Create: `apps/mobile/lib/triage.ts`, `apps/mobile/lib/triage.test.ts`
- Modify: `apps/mobile/app/(tabs)/inbox.tsx` (header overflow menu: Get Me To Zero; inbox-zero empty state), `apps/mobile/app/thread/[id].tsx` (Unsubscribe row; archive auto-advances to the next thread id passed via route params or falls back to router.back())

**Interfaces:**
- `apps/mobile/lib/triage.ts`:

```ts
import { nextAfterRemoval, type Thread, type UnsubscribeResult } from '@calendium/shared';

export { nextAfterRemoval };

/** Get Me To Zero cutoffs for the mobile action sheet. */
export function zeroCutoffs(now: Date = new Date()): { id: string; label: string; iso: string }[] {
  const day = 24 * 3_600_000;
  return [
    { id: 'week', label: 'Older than 1 week', iso: new Date(now.getTime() - 7 * day).toISOString() },
    { id: 'two-weeks', label: 'Older than 2 weeks', iso: new Date(now.getTime() - 14 * day).toISOString() },
    { id: 'month', label: 'Older than 1 month', iso: new Date(now.getTime() - 30 * day).toISOString() },
    { id: 'quarter', label: 'Older than 3 months', iso: new Date(now.getTime() - 90 * day).toISOString() },
  ];
}

export function canUnsubscribe(thread: Pick<Thread, 'unsubscribeMailto' | 'unsubscribeUrl'>): boolean {
  return Boolean(thread.unsubscribeMailto || thread.unsubscribeUrl);
}

/** Human copy for an unsubscribe result, shared by the confirmation toast. */
export function unsubscribeMessage(res: UnsubscribeResult): string {
  return res.method === 'link'
    ? 'Opening the unsubscribe page…'
    : 'Unsubscribed — the sender has been asked to stop.';
}
```

- UI wiring: inbox header gains an overflow (`⋯`) menu item `Get Me To Zero` → native action sheet of `zeroCutoffs()` → `getApiClient-equivalent (mobile's shared client instance).archiveOlderThan(iso)` → refresh list + `Archived N conversations` toast (mobile's existing toast/snackbar util). Thread screen: when `canUnsubscribe(thread)`, render an `Unsubscribe` row; on tap call `client.unsubscribeThread(id)`; `method === 'link'` opens `res.url` with `Linking.openURL`; then offer `Archive all from this sender?` → archive current thread. After any archive on the thread screen, navigate to the next thread when the inbox screen provided `nextId` in params, else `router.back()`. Empty inbox renders `You're at Inbox Zero` + subline (reuse the tab's existing empty-state component slot).

**Steps:**

- [ ] Write the failing test `apps/mobile/lib/triage.test.ts`:

```ts
import { canUnsubscribe, nextAfterRemoval, unsubscribeMessage, zeroCutoffs } from './triage';

describe('mobile triage helpers', () => {
  it('zeroCutoffs produces four descending cutoffs in ISO form', () => {
    const now = new Date('2026-07-17T12:00:00Z');
    const cutoffs = zeroCutoffs(now);
    expect(cutoffs.map((c) => c.id)).toEqual(['week', 'two-weeks', 'month', 'quarter']);
    expect(cutoffs[0]!.iso).toBe('2026-07-10T12:00:00.000Z');
    for (let i = 1; i < cutoffs.length; i++) {
      expect(Date.parse(cutoffs[i]!.iso)).toBeLessThan(Date.parse(cutoffs[i - 1]!.iso));
    }
  });

  it('canUnsubscribe requires a mailto or url', () => {
    expect(canUnsubscribe({ unsubscribeMailto: null, unsubscribeUrl: null })).toBe(false);
    expect(canUnsubscribe({ unsubscribeMailto: 'mailto:u@x.y', unsubscribeUrl: null })).toBe(true);
    expect(canUnsubscribe({ unsubscribeMailto: null, unsubscribeUrl: 'https://x.y/u' })).toBe(true);
  });

  it('unsubscribeMessage distinguishes the link method', () => {
    expect(unsubscribeMessage({ method: 'link', url: 'https://x.y/u' })).toContain('Opening');
    expect(unsubscribeMessage({ method: 'one_click' })).toContain('Unsubscribed');
  });

  it('re-exports auto-advance', () => {
    expect(nextAfterRemoval(['a', 'b'], 'a')).toBe('b');
  });
});
```

- [ ] Run to verify fail: `cd /Users/guilherme/Dev/pessoal/calendium/apps/mobile && bunx jest lib/triage.test.ts` — expected: `Cannot find module './triage'`.
- [ ] Implement `lib/triage.ts` as specified, then wire the three UI touchpoints in `inbox.tsx` / `thread/[id].tsx` per **Interfaces** (follow each screen's existing client/toast/demo-fallback patterns — mutations only touch mock data on the screens' existing demo path, per the global constraint).
- [ ] Run to verify pass: `cd /Users/guilherme/Dev/pessoal/calendium && bun run test:mobile && bun run typecheck` — expected: green.
- [ ] Commit: `git add -A && git commit -m "feat(mobile): Get Me To Zero, unsubscribe, inbox-zero state, and auto-advance"`

---

### Task 24: Full gates + feature-map/status updates

**Files:**
- Modify: `docs/feature-map.md` (status column), `docs/state-and-gaps.md` (if present in the repo root docs — note M2.1 shipped)
- Test: the full monorepo gate run is the verification.

**Steps:**

- [ ] Run every gate exactly as lefthook/CI will (Docker running):
  - `cd /Users/guilherme/Dev/pessoal/calendium && bun run lint:js` — expected: no diagnostics.
  - `bun run lint:go` — expected: clean for `backend` and `apps/desktop`.
  - `bun run test` — expected: api, shared, web, mobile, desktop all pass.
  - `CI=1 bun run test:e2e` — expected: full Playwright suite incl. the perf budget passes against the production build.
- [ ] Update `docs/feature-map.md`: flip the Status column from `planned` to `scaffolded` (the legend's "exists in this codebase" state — these now run on live data) for: Get Me To Zero, Bulk triage actions, One-click and bulk unsubscribe, Inbox zero celebration design, Auto-advance, Reorderable splits, Stars and labels via shortcuts, Undo anything (Z), Shortcut teaching UX; and update the Sub-100ms interactions row's note to mention the enforced Playwright budget.
- [ ] Update `docs/state-and-gaps.md` with a one-paragraph M2.1 completion note (features shipped, new endpoints, migration 0005, perf budget enforced).
- [ ] Commit: `git add -A && git commit -m "docs: mark M2.1 triage power features shipped in feature map"`

---

## Task dependency order

1 → 2 (ingest needs the columns) → 3/4/5/6/7 in any order (all need 1; 5 and 6 also touch Task 4's handler file) → 8 independent after 1 → 9 needs 3–8 → 10 independent → 11 needs 9+10 → 12–18 need 11 (12 before 13–17 because they reuse `undoLast`; 14 after 13 for the BulkBar hook; 15–18 independent of each other) → 19 independent after 11 → 20 after 13/14/17 (buttons exist) → 21 after 12–19 (measures the final page) → 22/23 after 9+10 (parallelizable) → 24 last.

## Verification summary (phase exit criteria)

- All new endpoints exercised by Go tests (service + handler) and mirrored in `packages/shared` with client tests.
- Web: every feature has a component/unit test and a demo-mode e2e; `Z` reverses archive/snooze/bulk/label actions; perf budget asserted at p95 < 100ms on a production build in CI.
- Desktop/mobile parity subsets shipped with their platform test suites green.
- `docs/feature-map.md` reflects reality; lefthook pre-push passes end to end.
