# M2.5 — Compose Extras & Contact Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **Migration numbering:** the eight M2 phase plans were authored in parallel, so migration filenames here are provisional — at execution time use the next free number in `backend/migrations/` and update references in the affected task.

**Goal**

Ship the M2.5 phase of the M2 roadmap (`docs/superpowers/specs/2026-07-17-m2-roadmap-design.md`): Smart Send, Instant Intro, Recent Opens feed, per-account rich signatures, attachment quick access with inline PDF preview, Auto Bcc, emoji reactions, and a contact pane — on live data, web first, then desktop/mobile parity per `docs/feature-map.md`.

**Architecture**

- Backend stays hexagonal: new use-cases enter through `port.MailService` / `port.AccountService` (driving), persistence through `port.MessageRepo` / `port.AccountRepo` / a new `port.ReactionRepo` (driven), provider I/O through `port.MailProvider`. HTTP handlers in `internal/adapter/in/httpapi` carry zero business logic.
- **Decision — signature & auto-BCC storage:** columns on `connected_accounts`, not a settings table. Both are per-account scalars exactly like the existing `vip_senders` column; a settings table would add a join + a second repo for two fields and buy nothing until settings become dynamic. Follow the `SetVipSenders` precedent end to end (repo `Update`, service setter, `PUT /v1/accounts/{id}/...` route).
- **Decision — attachment metadata indexing:** query-time from the existing `attachments` table. Sync ingest already persists attachment metadata per message (`messageRepo.Upsert` → `replaceAttachments` in `backend/internal/adapter/out/postgres/mail.go`), so there is no new ingest pipeline — only new indexes (filename trigram) plus a `provider_attachment_id` column so bodies can be fetched on demand for previews.
- **Decision — opens feed pagination:** keyset (cursor) pagination on `(opened_at DESC, id DESC)` with an opaque `base64url(unixMicro:id)` cursor, returned in the existing `domain.Page[T]` envelope — identical UX to thread listing, immune to feed inserts between pages.
- **Decision — emoji reactions encoding:** reactions are stored locally in a new `message_reactions` table (instant, offline-friendly, never lossy) and *optionally* delivered to external senders as a tiny threaded reply (`<p>{emoji}</p>`, `Re:` subject, `In-Reply-To` threading) created through the existing draft → scheduled-send pipeline, so undo-send (Z) works on a reaction exactly like any send. The API returns the created `draftId` so clients can offer undo.
- Smart Send infers a recipient's active hours and UTC offset purely from historical `messages.opened_at` of mail the user sent them (heuristic, no external data). Contact pane aggregates entirely from the local mirror; avatars (Gravatar SHA-256 → domain favicon → initials) resolve client-side.
- Instant Intro is a pure compose transform in `@calendium/shared` (no backend surface) reused by web and desktop.

**Tech Stack**

- Backend: Go stdlib only (`net/http`, `database/sql`), Postgres (pgx stdlib driver already in use), migrations in `backend/migrations/` (next: `0005`), testcontainers-backed repo tests.
- Contracts: `@calendium/shared` TypeScript types + `ApiClient` methods mirroring every new route.
- Web: Next.js 15 `(app)` route group, shadcn new-york components in `apps/web/components/ui`, TanStack Query, vitest + Testing Library, Playwright e2e in demo mode.
- Desktop: Wails v2 React frontend (`apps/desktop/frontend`), vitest. Mobile: Expo + jest-expo.

**Global Constraints**

- Mocks ONLY behind demo flags: web `DEMO_MODE` (`apps/web/lib/demo.ts`, mocks in `mail-mock.ts` / `settings-mock.ts`), desktop `lib/mock.ts`. Live code paths never import mocks.
- Backend remains stdlib-only — no new Go dependencies. No new JS runtime dependencies without need (PDF preview uses the browser's native PDF viewer via blob URL + `<iframe>`, NOT pdf.js).
- Every task is TDD: failing test first, then implementation. Gates before any commit: `bun run lint:js`, `bun run lint:go`, and the touched suites (`bun run test:api` / `test:shared` / `test:web` / `test:desktop` / `test:mobile`). Docker must be running for `test:api` (testcontainers).
- Conventional commits (`feat(backend): …`, `feat(web): …`, `test(desktop): …`), one commit per task.
- All new endpoints require auth + active entitlement (`entitlement.require`) like existing mail routes.

---

### Task 1: Migration 0005 + domain types + port extensions

**Files:**
- `backend/migrations/0005_compose_contact.sql` (new)
- `backend/internal/domain/mail.go` (add `Reaction`, `OpenEvent`, `AttachmentHit`, `SendSuggestion`, `ContactSummary`; add `ProviderAttachmentID` to `Attachment`, `Reactions` to `Message`)
- `backend/internal/domain/account.go` (add `SignatureHTML`, `AutoBcc` to `ConnectedAccount`)
- `backend/internal/port/driven.go` (extend `MessageRepo`, `MailProvider`; add `ReactionRepo`, `OpensQuery`, `AttachmentQuery`)
- `backend/internal/port/driving.go` (extend `MailService`, `AccountService`)
- `backend/internal/domain/domain_test.go` (serialization tests for new types)

**Interfaces:**

Migration (exact contents of `0005_compose_contact.sql`):

```sql
-- 0005_compose_contact.sql — M2.5: signatures, auto-BCC, reactions,
-- attachment quick access, recent-opens feed.

-- Per-account rich signature + auto-BCC, following the vip_senders
-- precedent (per-account scalar prefs live on connected_accounts).
ALTER TABLE connected_accounts ADD COLUMN signature_html text NOT NULL DEFAULT '';
ALTER TABLE connected_accounts ADD COLUMN auto_bcc text[] NOT NULL DEFAULT '{}';

-- Provider-native attachment id so bodies can be fetched on demand
-- (Gmail body.attachmentId / Graph attachment id). Backfilled empty;
-- sync ingest populates it for new/updated messages.
ALTER TABLE attachments ADD COLUMN provider_attachment_id text NOT NULL DEFAULT '';

-- Attachment quick access: filename substring search.
CREATE INDEX attachments_filename_trgm_idx ON attachments USING gin (filename gin_trgm_ops);

-- Recent Opens feed: keyset scan of opened sent mail per account.
CREATE INDEX messages_opened_feed_idx ON messages (account_id, opened_at DESC, id DESC)
    WHERE opened_at IS NOT NULL;

-- Emoji reactions: stored locally; optionally also delivered as a tiny reply.
CREATE TABLE message_reactions (
    id         text PRIMARY KEY,
    message_id text NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      text NOT NULL,
    delivery   text NOT NULL DEFAULT 'local', -- 'local' | 'sent'
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, user_id, emoji)
);
CREATE INDEX message_reactions_message_idx ON message_reactions (message_id);
```

Domain additions (`domain/mail.go`):

```go
// Reaction is a lightweight emoji reaction on a message. Stored locally;
// Delivery records whether it was also sent as a tiny reply.
type Reaction struct {
	ID        string    `json:"id"`
	MessageID string    `json:"messageId"`
	UserID    string    `json:"-"`
	Emoji     string    `json:"emoji"`
	Delivery  string    `json:"delivery"` // "local" | "sent"
	CreatedAt time.Time `json:"createdAt"`
}

// OpenEvent is one row of the Recent Opens feed: a sent message a
// recipient has opened, newest first.
type OpenEvent struct {
	MessageID  string         `json:"messageId"`
	ThreadID   string         `json:"threadId"`
	AccountID  string         `json:"accountId"`
	Subject    string         `json:"subject"`
	Recipients []EmailAddress `json:"recipients"`
	OpenedAt   time.Time      `json:"openedAt"`
	SentAt     time.Time      `json:"sentAt"`
}

// AttachmentHit is an attachment search result with message context.
type AttachmentHit struct {
	Attachment
	MessageID     string       `json:"messageId"`
	ThreadID      string       `json:"threadId"`
	ThreadSubject string       `json:"threadSubject"`
	From          EmailAddress `json:"from"`
	SentAt        time.Time    `json:"sentAt"`
}

// SendSuggestion is the Smart Send recommendation for one recipient,
// inferred from their historical open times.
type SendSuggestion struct {
	Email          string    `json:"email"`
	SuggestedAt    time.Time `json:"suggestedAt"`
	UTCOffsetHours int       `json:"utcOffsetHours"` // inferred, [-12, 13]
	Confidence     float64   `json:"confidence"`     // 0..1 share of opens near the peak
	SampleSize     int       `json:"sampleSize"`
}

// ContactSummary aggregates everything the local mirror knows about a sender.
type ContactSummary struct {
	Email         string     `json:"email"`
	Name          *string    `json:"name"` // from the most recent message
	Domain        string     `json:"domain"`
	ThreadCount   int        `json:"threadCount"`
	MessageCount  int        `json:"messageCount"`
	LastMessageAt *time.Time `json:"lastMessageAt"`
	RecentThreads []Thread   `json:"recentThreads"` // newest 5
}
```

`Attachment` gains `ProviderAttachmentID string \`json:"-"\``; `Message` gains `Reactions []Reaction \`json:"reactions"\`` (populated by the service on `GetThread`, empty slice otherwise). `ConnectedAccount` gains `SignatureHTML string \`json:"signatureHtml"\`` and `AutoBcc []string \`json:"autoBcc"\``.

Port additions (`port/driven.go`):

```go
// OpensQuery pages the Recent Opens feed (keyset on opened_at DESC, id DESC).
type OpensQuery struct {
	UserID string
	Cursor string
	Limit  int
}

// AttachmentQuery filters the attachment quick-access search.
type AttachmentQuery struct {
	UserID   string
	Query    string // filename substring (ILIKE, trigram-backed)
	Contact  string // restrict to messages involving this email
	ThreadID string
	Cursor   string
	Limit    int
}

// MessageRepo additions:
	// ListOpens returns opened sent messages, newest open first.
	ListOpens(ctx context.Context, q OpensQuery) (domain.Page[domain.OpenEvent], error)
	// OpenHourHistogram buckets opens of mail the user sent to
	// recipientEmail by UTC hour of opened_at.
	OpenHourHistogram(ctx context.Context, userID, recipientEmail string) ([24]int, error)
	// SearchAttachments searches mirrored attachment metadata.
	SearchAttachments(ctx context.Context, q AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
	// GetAttachment returns one attachment plus its owning message id.
	GetAttachment(ctx context.Context, attachmentID string) (domain.Attachment, string, error)
	// ContactSummary aggregates sender info from the local mirror.
	ContactSummary(ctx context.Context, userID, email string) (domain.ContactSummary, error)

// ReactionRepo persists emoji reactions.
type ReactionRepo interface {
	// Create is idempotent on (message_id, user_id, emoji): re-reacting
	// returns the existing row.
	Create(ctx context.Context, r domain.Reaction) (domain.Reaction, error)
	ListByMessages(ctx context.Context, messageIDs []string) (map[string][]domain.Reaction, error)
	// DeleteByEmoji removes the user's reaction; ErrNotFound when absent.
	DeleteByEmoji(ctx context.Context, messageID, userID, emoji string) error
}

// MailProvider addition:
	// FetchAttachment downloads one attachment body on demand.
	FetchAttachment(ctx context.Context, accessToken, providerMessageID, providerAttachmentID string) (data []byte, mimeType string, err error)
```

Port additions (`port/driving.go`):

```go
// AccountService additions:
	// SetSignature replaces the account's rich signature (sanitized HTML).
	SetSignature(ctx context.Context, userID, accountID, signatureHTML string) (domain.ConnectedAccount, error)
	// SetAutoBcc replaces the account's auto-BCC list applied at send.
	SetAutoBcc(ctx context.Context, userID, accountID string, autoBcc []string) (domain.ConnectedAccount, error)

// ReactionResult is a stored reaction plus the tiny-reply draft id when
// the reaction was also queued for delivery (undo-send capable).
type ReactionResult struct {
	Reaction domain.Reaction `json:"reaction"`
	DraftID  *string         `json:"draftId"`
}

// MailService additions:
	ListOpens(ctx context.Context, userID, cursor string, limit int) (domain.Page[domain.OpenEvent], error)
	// SuggestSendTime returns domain.ErrNotFound when history is too thin
	// (fewer than 5 recorded opens for the recipient).
	SuggestSendTime(ctx context.Context, userID, recipientEmail string) (domain.SendSuggestion, error)
	SearchAttachments(ctx context.Context, userID string, q AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
	// GetAttachmentContent fetches an attachment body from the provider.
	GetAttachmentContent(ctx context.Context, userID, attachmentID string) (data []byte, mimeType, filename string, err error)
	GetContact(ctx context.Context, userID, email string) (domain.ContactSummary, error)
	// ReactToMessage stores the reaction; when sendReply is true it also
	// queues a tiny threaded reply through the scheduled-send pipeline.
	ReactToMessage(ctx context.Context, userID, messageID, emoji string, sendReply bool) (ReactionResult, error)
	RemoveReaction(ctx context.Context, userID, messageID, emoji string) error
```

**Steps:**
- [ ] Write `backend/migrations/0005_compose_contact.sql` exactly as above; confirm `internal/migrate` picks it up lexically (embed.go globs the dir).
- [ ] Add failing JSON round-trip tests in `domain/domain_test.go` for `Reaction`, `OpenEvent`, `AttachmentHit`, `SendSuggestion`, `ContactSummary`, and the new `ConnectedAccount` fields (assert `signatureHtml`/`autoBcc` serialize, `ProviderAttachmentID` does not).
- [ ] Add the domain types and port methods; update `internal/service/fakes_test.go` fakes to satisfy the widened interfaces (stub implementations returning zero values so the package compiles).
- [ ] `cd backend && go build ./... && go test ./internal/domain/...` — green.
- [ ] `bun run lint:go` — green. Commit: `feat(backend): M2.5 schema, domain types, and port contracts`.

### Task 2: Postgres — signature & auto-BCC columns on connected_accounts

**Files:**
- `backend/internal/adapter/out/postgres/account.go`
- `backend/internal/adapter/out/postgres/account_test.go`

**Interfaces:** no new methods — `accountRepo.Create/GetByID/ListByUser/ListSyncable/Update` gain the two columns in their column lists and scans, mirroring how `vip_senders` flows today.

Real code — the column plumbing (adjust to the file's existing helpers):

```go
// column list fragment used by every SELECT:
//   ..., vip_senders, signature_html, auto_bcc, ...
// Update gains:
//   signature_html = $X, auto_bcc = $Y,
// Scan gains (using the file's existing pq-style array helper for text[]):
//   &a.SignatureHTML, textArray{&a.AutoBcc},
```

**Steps:**
- [ ] Extend `account_test.go` (testcontainers harness in `postgres_test.go`): create an account, `Update` it with `SignatureHTML: "<p>— Gui</p>"` and `AutoBcc: []string{"crm@log.example"}`, re-`GetByID`, assert both round-trip and default to `""`/`[]` for old rows.
- [ ] Run `cd backend && go test ./internal/adapter/out/postgres/ -run TestAccount` — red.
- [ ] Implement column plumbing in `account.go` (INSERT, both SELECT paths, UPDATE, scans).
- [ ] Re-run — green. Commit: `feat(backend): persist per-account signature and auto-bcc`.

### Task 3: Postgres — Recent Opens feed query with keyset pagination

**Files:**
- `backend/internal/adapter/out/postgres/mail.go` (messageRepo)
- `backend/internal/adapter/out/postgres/message_test.go`
- `backend/internal/adapter/out/postgres/helpers.go` (cursor encode/decode helpers if not present)

**Interfaces:** `messageRepo.ListOpens(ctx, q port.OpensQuery) (domain.Page[domain.OpenEvent], error)`.

Real code — the query and cursor:

```go
// opensCursor encodes/decodes the keyset cursor: base64url("unixMicro:id").
func encodeOpensCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", t.UnixMicro(), id)))
}

func decodeOpensCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	return time.UnixMicro(micros).UTC(), parts[1], nil
}

func (r messageRepo) ListOpens(ctx context.Context, q port.OpensQuery) (domain.Page[domain.OpenEvent], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	args := []any{q.UserID}
	cursorPred := ""
	if q.Cursor != "" {
		at, id, err := decodeOpensCursor(q.Cursor)
		if err != nil {
			return domain.Page[domain.OpenEvent]{}, err
		}
		args = append(args, at, id)
		cursorPred = "AND (m.opened_at, m.id) < ($2, $3)"
	}
	args = append(args, limit+1)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT m.id, m.thread_id, m.account_id, m.subject, m.to_addrs,
		       m.opened_at, m.sent_at
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND m.opened_at IS NOT NULL
		  AND lower(m.from_addr->>'email') = lower(ca.email)
		  %s
		ORDER BY m.opened_at DESC, m.id DESC
		LIMIT $%d`, cursorPred, len(args)), args...)
	if err != nil {
		return domain.Page[domain.OpenEvent]{}, err
	}
	defer rows.Close()
	var items []domain.OpenEvent
	for rows.Next() {
		var e domain.OpenEvent
		var recipients []byte
		if err := rows.Scan(&e.MessageID, &e.ThreadID, &e.AccountID,
			&e.Subject, &recipients, &e.OpenedAt, &e.SentAt); err != nil {
			return domain.Page[domain.OpenEvent]{}, err
		}
		if err := json.Unmarshal(recipients, &e.Recipients); err != nil {
			return domain.Page[domain.OpenEvent]{}, err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.OpenEvent]{}, err
	}
	page := domain.Page[domain.OpenEvent]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeOpensCursor(last.OpenedAt, last.MessageID)
	}
	if page.Items == nil {
		page.Items = []domain.OpenEvent{}
	}
	return page, nil
}
```

(Match `domain.Page`'s actual field names in `domain/page.go` — use whatever the thread listing returns.)

**Steps:**
- [ ] Failing test in `message_test.go`: seed 3 sent+opened messages (from = account email, distinct `opened_at`), 1 sent-but-unopened, 1 received+opened (from ≠ account email, must be excluded), 1 opened message for a different user (excluded). Assert order (newest open first), exclusions, and that `limit=2` returns a `NextCursor` whose second page yields the third row and no cursor.
- [ ] Run `go test ./internal/adapter/out/postgres/ -run TestListOpens` — red.
- [ ] Implement `ListOpens` + cursor helpers as above.
- [ ] Green; run full postgres suite. Commit: `feat(backend): recent-opens feed query with keyset pagination`.

### Task 4: Postgres — open-hour histogram + contact aggregation

**Files:**
- `backend/internal/adapter/out/postgres/mail.go`
- `backend/internal/adapter/out/postgres/message_test.go`

**Interfaces:** `OpenHourHistogram(ctx, userID, recipientEmail string) ([24]int, error)` and `ContactSummary(ctx, userID, email string) (domain.ContactSummary, error)` on `messageRepo`.

Real code — the two aggregation queries:

```go
func (r messageRepo) OpenHourHistogram(ctx context.Context, userID, recipientEmail string) ([24]int, error) {
	var hist [24]int
	rows, err := r.db.QueryContext(ctx, `
		SELECT extract(hour FROM m.opened_at AT TIME ZONE 'UTC')::int AS h, count(*)
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND m.opened_at IS NOT NULL
		  AND lower(m.from_addr->>'email') = lower(ca.email)
		  AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(m.to_addrs) rcpt
			WHERE lower(rcpt->>'email') = lower($2))
		GROUP BY h`, userID, recipientEmail)
	if err != nil {
		return hist, err
	}
	defer rows.Close()
	for rows.Next() {
		var h, c int
		if err := rows.Scan(&h, &c); err != nil {
			return hist, err
		}
		if h >= 0 && h < 24 {
			hist[h] = c
		}
	}
	return hist, rows.Err()
}

func (r messageRepo) ContactSummary(ctx context.Context, userID, email string) (domain.ContactSummary, error) {
	c := domain.ContactSummary{Email: strings.ToLower(email)}
	if i := strings.LastIndex(c.Email, "@"); i >= 0 {
		c.Domain = c.Email[i+1:]
	}
	// Name from the most recent message from this contact + message count.
	err := r.db.QueryRowContext(ctx, `
		SELECT count(*),
		       max(m.sent_at),
		       (SELECT m2.from_addr->>'name'
		        FROM messages m2
		        JOIN connected_accounts ca2 ON ca2.id = m2.account_id
		        WHERE ca2.user_id = $1
		          AND lower(m2.from_addr->>'email') = lower($2)
		        ORDER BY m2.sent_at DESC LIMIT 1)
		FROM messages m
		JOIN connected_accounts ca ON ca.id = m.account_id
		WHERE ca.user_id = $1
		  AND (lower(m.from_addr->>'email') = lower($2)
		       OR EXISTS (SELECT 1 FROM jsonb_array_elements(m.to_addrs) rcpt
		                  WHERE lower(rcpt->>'email') = lower($2)))`,
		userID, email).Scan(&c.MessageCount, &c.LastMessageAt, &c.Name)
	if err != nil {
		return c, err
	}
	// Recent threads via participants containment (thread repo columns reused).
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+threadCols+`
		FROM threads t
		JOIN connected_accounts ca ON ca.id = t.account_id
		WHERE ca.user_id = $1
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(t.participants) p
		              WHERE lower(p->>'email') = lower($2))
		ORDER BY t.last_message_at DESC
		LIMIT 5`, userID, email)
	// ... scan with the file's existing scanThread helper; set c.ThreadCount
	// via a count(*) over the same predicate in the same round trip (CTE) or
	// a second QueryRow. RecentThreads defaults to []domain.Thread{}.
	_ = rows
	return c, nil
}
```

(`threadCols` and the thread scan helper already exist in `mail.go` — reuse them; if `threadCols` aliases `t.`, keep the join aliases consistent.)

**Steps:**
- [ ] Failing tests: (a) histogram counts only opens of mail sent *to* the recipient by *this* user, bucketed by UTC hour; (b) `ContactSummary` returns name from newest message, correct counts, ≤5 recent threads newest-first, empty-mirror case returns zero counts and empty slice (not nil).
- [ ] Implement both methods.
- [ ] `go test ./internal/adapter/out/postgres/` — green. Commit: `feat(backend): open-hour histogram and contact aggregation queries`.

### Task 5: Postgres — attachment search + reaction repo

**Files:**
- `backend/internal/adapter/out/postgres/mail.go` (SearchAttachments, GetAttachment; add `provider_attachment_id` to `replaceAttachments`/`attachmentsFor`)
- `backend/internal/adapter/out/postgres/reaction.go` (new)
- `backend/internal/adapter/out/postgres/store.go` (expose `Reactions()` repo accessor like the others)
- `backend/internal/adapter/out/postgres/message_test.go`, `backend/internal/adapter/out/postgres/reaction_test.go` (new)

**Interfaces:**

```go
func (r messageRepo) SearchAttachments(ctx context.Context, q port.AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
// SQL shape: attachments a JOIN messages m ON m.id = a.message_id
//            JOIN threads t ON t.id = m.thread_id
//            JOIN connected_accounts ca ON ca.id = m.account_id
// WHERE ca.user_id = $1
//   [AND a.filename ILIKE '%'||$q||'%']            -- trigram index
//   [AND t.id = $threadID]
//   [AND (lower(m.from_addr->>'email') = lower($contact) OR EXISTS(to_addrs/cc_addrs contains $contact))]
// ORDER BY m.sent_at DESC, a.id DESC  -- keyset cursor base64url("unixMicro:attID") like Task 3
func (r messageRepo) GetAttachment(ctx context.Context, attachmentID string) (domain.Attachment, string, error) // returns attachment + message_id

type reactionRepo struct{ db *sql.DB } // implements port.ReactionRepo
```

**Steps:**
- [ ] Failing tests: seed messages with attachments (`report.pdf`, `photo.png`) across two users; assert filename search matches case-insensitively, contact filter matches from/to/cc, thread filter, user isolation, pagination (limit 1 → cursor → page 2), and `GetAttachment` returns `ProviderAttachmentID` after `replaceAttachments` stores it.
- [ ] Failing reaction tests: `Create` idempotent on duplicate (returns existing row, no error), `ListByMessages` groups by message id, `DeleteByEmoji` removes only the caller's emoji and returns `domain.ErrNotFound` when absent.
- [ ] Implement: extend `replaceAttachments` INSERT + `attachmentsFor` SELECT with `provider_attachment_id`; add `SearchAttachments`/`GetAttachment`; write `reaction.go`.
- [ ] `go test ./internal/adapter/out/postgres/` — green. Commit: `feat(backend): attachment metadata search and reaction persistence`.

### Task 6: Provider gateways — FetchAttachment + attachment-id ingest

**Files:**
- `backend/internal/adapter/out/googleapi/mail.go`, `gmail_types.go`, `mail_test.go`
- `backend/internal/adapter/out/msgraph/mail.go`, `mail_test.go`

**Interfaces:**

```go
// googleapi: GET /gmail/v1/users/me/messages/{mid}/attachments/{aid}
// response {size, data} — data is base64url; mimeType comes from the stored
// metadata, so return "" and let the service use the mirrored mime_type.
func (c *Client) FetchAttachment(ctx context.Context, accessToken, providerMessageID, providerAttachmentID string) ([]byte, string, error)

// msgraph: GET /v1.0/me/messages/{mid}/attachments/{aid}/$value → raw bytes;
// Content-Type header is the mime type.
func (c *Client) FetchAttachment(ctx context.Context, accessToken, providerMessageID, providerAttachmentID string) ([]byte, string, error)
```

Ingest: in each adapter's message-parsing path, populate `domain.Attachment.ProviderAttachmentID` (Gmail: part `body.attachmentId`; Graph: attachment `id`). Both adapters already parse attachment metadata — extend, don't restructure.

**Steps:**
- [ ] Failing httptest-based tests in each adapter (existing test harness pattern): FetchAttachment hits the right URL with bearer token, decodes base64url (Gmail) / raw body + Content-Type (Graph); sync parsing sets `ProviderAttachmentID`.
- [ ] Implement both adapters.
- [ ] `go test ./internal/adapter/out/googleapi/ ./internal/adapter/out/msgraph/` — green. Commit: `feat(backend): provider attachment download + attachment-id ingest`.

### Task 7: Service — signatures, auto-BCC (server-side at send)

**Files:**
- `backend/internal/service/account.go`, `account_test.go`
- `backend/internal/service/sync.go`, `sync_test.go`
- `backend/internal/service/mail.go` (SendDraft provisional message reflects auto-BCC), `mail_test.go`

**Interfaces:**

```go
// AccountService (mirror SetVipSenders exactly: ownership check, normalize,
// repo Update, return updated account):
func (s *AccountService) SetSignature(ctx context.Context, userID, accountID, signatureHTML string) (domain.ConnectedAccount, error)
func (s *AccountService) SetAutoBcc(ctx context.Context, userID, accountID string, autoBcc []string) (domain.ConnectedAccount, error)
// Validation: each autoBcc entry must parse as an address (reuse the vip
// normalization: trim, lowercase, reject empties/dupes). signatureHTML max
// 100 KB; stored as-is (clients render it inside their own sanitizer).
```

Real code — auto-BCC merge at delivery (`sync.go`, inside `deliverDraft` where `out := port.OutgoingMessage{...}` is built):

```go
// Auto-BCC: merge the account's configured addresses into the outgoing
// BCC, skipping any address already present on the message.
if len(acct.AutoBcc) > 0 {
	seen := make(map[string]bool, len(d.To)+len(d.Cc)+len(d.Bcc))
	for _, lists := range [][]domain.EmailAddress{d.To, d.Cc, d.Bcc} {
		for _, a := range lists {
			seen[strings.ToLower(a.Email)] = true
		}
	}
	for _, addr := range acct.AutoBcc {
		if !seen[strings.ToLower(addr)] {
			out.Bcc = append(out.Bcc, domain.EmailAddress{Email: addr})
		}
	}
}
```

Note: the mirrored `domain.Message` written after delivery keeps the draft's original Bcc (auto-BCC is a delivery concern, not thread content) — assert this in tests.

**Steps:**
- [ ] Failing service tests: `SetSignature`/`SetAutoBcc` ownership (other user's account → `ErrNotFound`), validation (bad email in autoBcc → `ErrValidation`, >100 KB signature → `ErrValidation`), persistence via fake repo.
- [ ] Failing sync test: account with `AutoBcc: ["crm@log.example", "dup@x.com"]`, draft already BCCs `dup@x.com` → provider fake receives exactly one added BCC (`crm@log.example`), and the stored mirror message's Bcc equals the draft's original.
- [ ] Implement; update `fakes_test.go` provider fake to capture `OutgoingMessage`.
- [ ] `go test ./internal/service/` — green. Commit: `feat(backend): per-account signatures and server-side auto-bcc at send`.

### Task 8: Service — Smart Send heuristic + opens feed

**Files:**
- `backend/internal/service/mail.go`, `backend/internal/service/smartsend.go` (new), `mail_test.go`, `smartsend_test.go` (new)

**Interfaces:** `MailService.ListOpens` (entitlement check → `messages.ListOpens`, clamp limit to 100) and `MailService.SuggestSendTime`.

Real code — the heuristic (`smartsend.go`):

```go
package service

import (
	"time"

	"calendium/backend/internal/domain"
)

const (
	// smartSendMinOpens is the minimum recorded opens before a suggestion
	// is offered; below it SuggestSendTime returns domain.ErrNotFound.
	smartSendMinOpens = 5
	// assumedLocalPeakHour anchors timezone inference: people's open-rate
	// peak is assumed to sit around 10:00 local (mid-morning email block).
	assumedLocalPeakHour = 10
	// smartSendLeadTime is the minimum future distance of a suggestion.
	smartSendLeadTime = 5 * time.Minute
)

// suggestFromHistogram converts a UTC open-hour histogram into a Smart Send
// suggestion. Deterministic and pure for testability.
func suggestFromHistogram(hist [24]int, email string, now time.Time) (domain.SendSuggestion, bool) {
	total := 0
	for _, c := range hist {
		total += c
	}
	if total < smartSendMinOpens {
		return domain.SendSuggestion{}, false
	}
	// Circularly smoothed peak: score(h) = hist[h-1] + 2*hist[h] + hist[h+1].
	peak, best := 0, -1
	for h := 0; h < 24; h++ {
		score := hist[(h+23)%24] + 2*hist[h] + hist[(h+1)%24]
		if score > best {
			peak, best = h, score
		}
	}
	window := hist[(peak+23)%24] + hist[peak] + hist[(peak+1)%24]
	confidence := float64(window) / float64(total)

	// Inferred offset: peak UTC hour ≈ 10:00 local ⇒ offset = peak - 10,
	// normalized into [-12, 13].
	offset := peak - assumedLocalPeakHour
	for offset < -12 {
		offset += 24
	}
	for offset > 13 {
		offset -= 24
	}

	// Next occurrence of the peak UTC hour, at least smartSendLeadTime out.
	next := time.Date(now.Year(), now.Month(), now.Day(), peak, 0, 0, 0, time.UTC)
	if !next.After(now.Add(smartSendLeadTime)) {
		next = next.Add(24 * time.Hour)
	}
	return domain.SendSuggestion{
		Email:          email,
		SuggestedAt:    next,
		UTCOffsetHours: offset,
		Confidence:     confidence,
		SampleSize:     total,
	}, true
}
```

`SuggestSendTime` = entitlement check → `messages.OpenHourHistogram` → `suggestFromHistogram(hist, email, s.clock.Now().UTC())`; `false` → `domain.ErrNotFound`.

**Steps:**
- [ ] Failing table-driven tests for `suggestFromHistogram`: (a) all opens at UTC 14 → peak 14, offset +4, suggestion at next 14:00 UTC; (b) opens spread 13–15 → confidence = 1.0; (c) 4 opens → not ok; (d) `now` = 13:58 UTC with peak 14 → suggestion tomorrow (lead time); (e) peak 2 → offset −8 (normalization); (f) uniform histogram → confidence = 3/24 (still ok, UI thresholds decide).
- [ ] Failing `MailService` tests: `ListOpens` entitlement + passthrough; `SuggestSendTime` maps thin history to `ErrNotFound`.
- [ ] Implement both.
- [ ] `go test ./internal/service/` — green. Commit: `feat(backend): smart-send suggestion heuristic and opens-feed service`.

### Task 9: Service — attachment quick access + contact summary

**Files:**
- `backend/internal/service/mail.go`, `mail_test.go`

**Interfaces:**

```go
func (s *MailService) SearchAttachments(ctx context.Context, userID string, q port.AttachmentQuery) (domain.Page[domain.AttachmentHit], error)
// entitlement → q.UserID = userID → clamp Limit (default 50, max 100) → repo.

func (s *MailService) GetAttachmentContent(ctx context.Context, userID, attachmentID string) ([]byte, string, string, error)
// repo.GetAttachment → message → ownership via account.UserID == userID
// (else ErrNotFound) → provider FetchAttachment(token, msg.ProviderMessageID,
// att.ProviderAttachmentID); empty ProviderAttachmentID → ErrNotFound
// ("attachment content not synced"); provider mimeType == "" falls back to
// the mirrored att.MimeType. Returns (data, mimeType, filename).

func (s *MailService) GetContact(ctx context.Context, userID, email string) (domain.ContactSummary, error)
// entitlement → validate email (reuse address validation) → repo.
```

**Steps:**
- [ ] Failing tests with fakes: ownership rejection on another user's attachment, provider fetch invoked with the mirrored provider ids, mime fallback, contact email validation (`ErrValidation` for "not-an-email").
- [ ] Implement (MailService gains the provider map + token source deps it already holds for send — reuse `s.mail` / `s.tokens`).
- [ ] `go test ./internal/service/` — green. Commit: `feat(backend): attachment content fetch and contact summary services`.

### Task 10: Service — emoji reactions (store + tiny-reply delivery)

**Files:**
- `backend/internal/service/mail.go`, `mail_test.go`, `backend/internal/service/service.go` (wire ReactionRepo dep)

**Interfaces:**

```go
func (s *MailService) ReactToMessage(ctx context.Context, userID, messageID, emoji string, sendReply bool) (port.ReactionResult, error)
func (s *MailService) RemoveReaction(ctx context.Context, userID, messageID, emoji string) error
```

Behavior (encode exactly):
1. Entitlement; load message; ownership via its account's `UserID` (else `ErrNotFound`).
2. Validate emoji: non-empty, ≤16 bytes, no ASCII letters/digits (`ErrValidation` otherwise).
3. `reactions.Create` (idempotent) with `Delivery: "local"`.
4. When `sendReply` && the message is not from the account owner (never reply-react to yourself): create a `domain.Draft{AccountID, ThreadID: &msg.ThreadID, To: []{msg.From}, Subject: "Re: " + msg.Subject, BodyHTML: "<p>" + emoji + "</p>", ScheduledAt: now + undoSendGrace}` via `s.drafts.Create`, set the reaction row's `Delivery` to `"sent"`, and return the draft id — the worker delivers it through the normal pipeline and Z/unsend works unchanged.
5. `GetThread` attaches reactions: after `messages.ListByThread`, one `reactions.ListByMessages` call fills `Message.Reactions` (empty slice default).

**Steps:**
- [ ] Failing tests: validation table (accepts "👍", "❤️", rejects "", "abc", ">16 bytes"); local-only store when `sendReply=false`; tiny-reply draft created with grace-scheduled send and correct threading when `sendReply=true`; self-message never sends a reply; duplicate reaction idempotent; `GetThread` embeds reactions; `RemoveReaction` deletes.
- [ ] Implement; wire `ReactionRepo` through `service.go` deps and `cmd/api/main.go` composition.
- [ ] `go test ./internal/service/` — green. Commit: `feat(backend): emoji reactions stored locally with optional tiny-reply delivery`.

### Task 11: HTTP surface — 9 new routes + handlers

**Files:**
- `backend/internal/adapter/in/httpapi/httpapi.go` (routes)
- `backend/internal/adapter/in/httpapi/mail.go`, `accounts.go` (handlers)
- `backend/internal/adapter/in/httpapi/mail_handlers_test.go`, `accounts_handlers_test.go`, `harness_test.go` (extend the service fakes)

**Interfaces (exact routes):**

```
GET    /v1/mail/opens?cursor=&limit=                  → Page[OpenEvent]
GET    /v1/mail/send-suggestion?email=                → SendSuggestion | 404
GET    /v1/mail/attachments?q=&contact=&threadId=&cursor=&limit= → Page[AttachmentHit]
GET    /v1/mail/attachments/{id}/content              → raw bytes, Content-Type + Content-Disposition: inline; filename="…"
GET    /v1/mail/contacts/{email}                      → ContactSummary
POST   /v1/mail/messages/{id}/reactions               {emoji, sendReply?} → ReactionResult
DELETE /v1/mail/messages/{id}/reactions/{emoji}       → 204
PUT    /v1/accounts/{id}/signature                    {signatureHtml} → ConnectedAccount
PUT    /v1/accounts/{id}/auto-bcc                     {autoBcc: string[]} → ConnectedAccount
```

Handler notes: attachment content handler writes headers then body (no JSON envelope); `{emoji}` path value is URL-decoded by ServeMux; `send-suggestion` maps `ErrNotFound` → 404 via the existing `writeError` (client treats 404 as "no suggestion", not an error toast).

**Steps:**
- [ ] Extend the harness fake services with the new methods; failing handler tests per route: happy path JSON shape, auth required (401 without bearer), validation errors → 400, suggestion 404 passthrough, attachment content returns `Content-Type: application/pdf` and inline disposition.
- [ ] Register routes + implement handlers (thin: parse → service → write).
- [ ] `go test ./internal/adapter/in/httpapi/` then full `bun run test:api` — green. Commit: `feat(backend): M2.5 REST surface (opens, smart send, attachments, contacts, reactions, signature, auto-bcc)`.

### Task 12: Shared contracts — types, ApiClient, Instant Intro transform

**Files:**
- `packages/shared/src/types.ts` (Reaction, OpenEvent, AttachmentHit, SendSuggestion, ContactSummary, ReactionResult; ConnectedAccount gains `signatureHtml: string; autoBcc: string[]`; Message gains `reactions: Reaction[]`; Attachment unchanged)
- `packages/shared/src/client.ts`
- `packages/shared/src/mail-transforms.ts` (new)
- `packages/shared/src/index.ts` (export)
- `packages/shared/src/client.test.ts`, `packages/shared/src/mail-transforms.test.ts` (new)

**Interfaces:**

```ts
// client.ts additions (mirror route list from Task 11):
listOpens(params: { cursor?: string; limit?: number }): Promise<Page<OpenEvent>>;
getSendSuggestion(email: string): Promise<SendSuggestion>; // throws ApiRequestError(404) when none
searchAttachments(params: { q?: string; contact?: string; threadId?: string; cursor?: string; limit?: number }): Promise<Page<AttachmentHit>>;
/** URL for streaming/iframe use; fetch it yourself with the bearer token. */
attachmentContentPath(attachmentId: string): string; // `/v1/mail/attachments/${id}/content`
getContact(email: string): Promise<ContactSummary>;
reactToMessage(messageId: string, emoji: string, sendReply?: boolean): Promise<ReactionResult>;
removeReaction(messageId: string, emoji: string): Promise<void>;
setSignature(accountId: string, signatureHtml: string): Promise<ConnectedAccount>;
setAutoBcc(accountId: string, autoBcc: string[]): Promise<ConnectedAccount>;

// mail-transforms.ts — Instant Intro (pure, shared by web + desktop):
export interface InstantIntroDraft {
  to: EmailAddress[];   // everyone on the thread except me and the introducer
  bcc: EmailAddress[];  // the introducer, moved to BCC
  subject: string;      // "Re: …" (idempotent prefix)
  body: string;         // "Thanks <first name>! (moving you to BCC)\n\n"
  threadId: string;
}
/**
 * Builds the Instant Intro reply from an intro thread: reply-all to the
 * latest message, thank the introducer (its sender), move them to BCC.
 * Returns null when the transform doesn't apply (no messages, or the
 * latest message is from me, or there is no third participant).
 */
export function buildInstantIntro(
  thread: Thread,
  messages: Message[],
  myEmail: string
): InstantIntroDraft | null;
```

`buildInstantIntro` rules (test these exactly): introducer = `from` of the latest non-mine message; recipients = union of that message's `from`/`to`/`cc` minus me minus the introducer, deduped case-insensitively; null when that union is empty; subject prefixes `Re: ` only when not already present; first name = text before the first space of the introducer's display name, falling back to the email local part.

**Steps:**
- [ ] Failing client tests (existing fetch-mock pattern): every new method hits the right path/method/body, 404 on suggestion surfaces as `ApiRequestError`.
- [ ] Failing transform tests: classic 3-party intro, CC'd introducer, "Re:" idempotence, me-as-latest-sender → null, two-party thread → null, name fallback.
- [ ] Implement; `bun run test:shared && bun run --cwd packages/shared typecheck` — green. Commit: `feat(shared): M2.5 API contracts and instant-intro transform`.

### Task 13: Web settings — signature editor + auto-BCC per account

**Files:**
- `apps/web/app/(app)/settings/page.tsx` (new "Compose" card per connected account)
- `apps/web/lib/settings-data.ts` (mutations: `saveSignature`, `saveAutoBcc` — demo-aware)
- `apps/web/lib/settings-mock.ts` (mock accounts gain `signatureHtml`/`autoBcc`; mock mutations)
- `apps/web/app/(app)/settings/settings-compose.test.tsx` (new)

**Interfaces:** per account: a signature editor (contentEditable div with Bold/Italic/Link buttons via `document.execCommand`, storing innerHTML — consistent with the app's existing lightweight rich text; no new deps) with live preview, and an auto-BCC chip input reusing the `ChipsRow` pattern from compose (extract `ChipsRow` to `apps/web/components/app/chips-row.tsx` and re-export from compose to avoid breaking `compose.test.tsx`).

**Steps:**
- [ ] Failing component tests: renders current signature/auto-BCC from account data, save calls `setSignature`/`setAutoBcc` with edited values, invalid chip email rejected inline, demo mode round-trips through the mock store.
- [ ] Implement UI + data plumbing; `getApiClient()` methods from Task 12.
- [ ] `bun run test:web && bun run --cwd apps/web typecheck` — green. Commit: `feat(web): per-account signature and auto-bcc settings`.

### Task 14: Web compose — signature auto-apply, Smart Send nudge, Instant Intro

**Files:**
- `apps/web/components/app/compose.tsx`
- `apps/web/components/app/thread-view.tsx` (Instant Intro action)
- `apps/web/components/app/command-palette.tsx` ("Instant Intro" command, visible on a thread)
- `apps/web/lib/use-mail.ts` (`useSendSuggestion(email)` query hook, 404 → null, `staleTime: 5 min`)
- `apps/web/components/app/compose.test.tsx`, `apps/web/lib/mail-mock.ts` (mock suggestion + signature behind `DEMO_MODE`)

**Interfaces:**
- Signature: when the selected from-account has `signatureHtml` and the body doesn't already end with it, append `\n\n--\n` + `htmlToText(signatureHtml)` on account selection/switch (replace previous account's signature block if present; track the applied block in a ref). Sent HTML body appends the rich `signatureHtml`.
- Smart Send nudge: debounced (500 ms) `useSendSuggestion(firstToRecipient.email)`; when a suggestion exists with `confidence >= 0.3` and no `scheduledAt` chosen, render a dismissible chip next to the Send Later control: `Best time: {format(suggestedAt, 'EEE h:mm a')} — their {morning|afternoon|evening}` (bucket by `suggestedAt` shifted by `utcOffsetHours`); clicking sets `scheduledAt = suggestedAt` (the existing Send Later state).
- Instant Intro: thread-view keyboard/palette action calls `buildInstantIntro(thread, messages, myEmail)`; non-null → `openCompose({ to, bcc, subject, body, threadId })`; null → toast "This doesn't look like an intro thread".

**Steps:**
- [ ] Failing tests: signature appended once and swapped on account change; suggestion chip renders from mocked hook and clicking schedules; low-confidence/404 renders nothing; palette shows "Instant Intro" on a thread and dispatches a prefilled compose (assert to/bcc/subject).
- [ ] Implement.
- [ ] `bun run test:web` — green. Commit: `feat(web): signature auto-apply, smart-send nudge, instant intro`.

### Task 15: Web — Recent Opens feed panel

**Files:**
- `apps/web/components/app/opens-feed.tsx` (new)
- `apps/web/app/(app)/mail/page.tsx` (toggle: sidebar "Opens" entry + `g o` shortcut)
- `apps/web/lib/use-mail.ts` (`useOpensFeed` — `useInfiniteQuery` on `listOpens`, `getNextPageParam: p => p.nextCursor || undefined`)
- `apps/web/lib/mail-mock.ts` (demo opens), `apps/web/components/app/opens-feed.test.tsx` (new)

**Interfaces:** right-side panel (same pattern as thread-view pane): reverse-chron rows `[recipient avatar/initials] {recipient} opened "{subject}" · {formatDistanceToNow(openedAt)}`; row click navigates to `/mail/{threadId}`; "Load more" button when `nextCursor`; empty state "No opens yet — read statuses appear as recipients open your mail."

**Steps:**
- [ ] Failing tests: renders mocked pages newest-first, load-more fetches cursor page, row click routes to thread, empty state.
- [ ] Implement panel + shortcut + sidebar entry.
- [ ] `bun run test:web` — green. Commit: `feat(web): recent opens feed panel`.

### Task 16: Web — attachment quick access + inline PDF preview

**Files:**
- `apps/web/components/app/attachments-pane.tsx` (new: search input, contact/thread filter chips, result list, preview dialog)
- `apps/web/components/app/command-palette.tsx` ("Search attachments" command)
- `apps/web/components/app/thread-view.tsx` ("View all attachments from {sender}" affordance passes `contact` filter)
- `apps/web/lib/use-mail.ts` (`useAttachmentSearch(params)` infinite query; `fetchAttachmentBlob(id)` helper: authed `fetch` of `attachmentContentPath(id)` → `URL.createObjectURL`)
- `apps/web/lib/mail-mock.ts` (demo attachment hits + a tiny embedded base64 PDF), `apps/web/components/app/attachments-pane.test.tsx` (new)

**Interfaces:** results grouped by thread, each row `filename · humanSize(sizeBytes) · from · relative date` with a mime icon. Click: `mimeType === 'application/pdf'` → preview `Dialog` with `<iframe title={filename} src={blobUrl} className="h-[80vh] w-full" />` (browser-native PDF viewer; revoke blob URL on close); other types → download via temporary `<a download>`. Images (`image/*`) preview with `<img>` in the same dialog.

**Steps:**
- [ ] Failing tests: search input debounces into `searchAttachments` with `q`; contact filter applied; PDF row opens dialog with iframe (mock `URL.createObjectURL`); non-previewable row triggers download path; pagination.
- [ ] Implement pane + palette entry + thread-view affordance.
- [ ] `bun run test:web` — green. Commit: `feat(web): attachment quick access with inline pdf preview`.

### Task 17: Web — contact pane

**Files:**
- `apps/web/components/app/contact-pane.tsx` (new)
- `apps/web/components/app/thread-view.tsx` (toggle button on sender header + `o c` shortcut)
- `apps/web/lib/contact-utils.ts` (new: `gravatarUrl(email): Promise<string>` via SubtleCrypto SHA-256 with `?d=404`; `faviconUrl(domain)` = `https://icons.duckduckgo.com/ip3/{domain}.ico`; `companyFromDomain(domain)` = capitalized second-level label, `''` for freemail domains — gmail/outlook/yahoo/icloud/hotmail/proton list)
- `apps/web/lib/use-mail.ts` (`useContact(email)`), `apps/web/lib/mail-mock.ts` (demo summary)
- `apps/web/lib/contact-utils.test.ts`, `apps/web/components/app/contact-pane.test.tsx` (new)

**Interfaces:** sidebar card: avatar (gravatar → favicon → initials fallback chain via `onError`), name, email, company line (`companyFromDomain`), stats row (`{threadCount} conversations · {messageCount} messages · last {relative}`), "Recent conversations" list of the 5 threads (click → navigate), and quick actions: Compose to, Search attachments from (opens Task 16 pane with `contact` prefilled).

**Steps:**
- [ ] Failing util tests: SHA-256 gravatar hash of `gui336699@gmail.com` matches known vector (lowercase/trim first), freemail → no company, `acme.co.uk` → `Acme`.
- [ ] Failing pane tests: renders mocked summary, recent-thread click routes, compose quick-action opens compose prefilled.
- [ ] Implement.
- [ ] `bun run test:web` — green. Commit: `feat(web): contact pane with mirror-backed insights`.

### Task 18: Web — emoji reactions on messages

**Files:**
- `apps/web/components/app/thread-view.tsx` (hover reaction bar per message: 👍 ❤️ 😂 🎉 ✅; existing reactions rendered as count chips; toggle on click)
- `apps/web/lib/use-mail.ts` (`useReactToMessage` mutation with optimistic update of the thread query cache; on `ReactionResult.draftId`, show the existing undo-send toast wired to `unsendDraft(draftId)`)
- `apps/web/lib/mail-mock.ts` (demo reactions), `apps/web/components/app/thread-view.test.tsx` (extend)

**Interfaces:** reacting to a message whose `from` ≠ my account email calls `reactToMessage(id, emoji, true)` (tiny reply, undo toast); reacting to my own message calls with `sendReply=false`. Clicking an existing own-reaction chip calls `removeReaction`.

**Steps:**
- [ ] Failing tests: hover bar renders, click stores optimistically and shows undo toast when `draftId` returned, own-message reaction sends `sendReply=false`, chip toggle removes.
- [ ] Implement.
- [ ] `bun run test:web` — green. Commit: `feat(web): emoji reactions with undo-capable tiny replies`.

### Task 19: Desktop parity

**Files:**
- `apps/desktop/frontend/src/views/ComposeView.tsx` (signature auto-apply from account, Smart Send chip beside Send Later, Instant Intro via `buildInstantIntro` from `@calendium/shared`)
- `apps/desktop/frontend/src/views/SettingsView.tsx` (signature textarea + auto-BCC list per account)
- `apps/desktop/frontend/src/views/ThreadPane.tsx` (reaction bar; "Attachments" list with PDF preview dialog via blob iframe; contact summary header block)
- `apps/desktop/frontend/src/views/InboxView.tsx` (Opens feed toggle pane)
- `apps/desktop/frontend/src/lib/mock.ts` (demo data for all of the above, behind the existing mock flag)
- `apps/desktop/frontend/src/lib/compose.ts` + `compose.test.ts` (signature-application helper mirrors web logic), new `views/*.test.tsx` cases

**Interfaces:** all data access through the shared `ApiClient` (already constructed in `lib/api.ts`); no desktop-only endpoints. Feature-map platform scope honored: Smart Send, opens feed, signatures, attachments+preview, reactions, contact pane, Instant Intro — all web+desktop features land here.

**Steps:**
- [ ] Failing vitest cases: signature helper (apply/swap), Instant Intro action produces prefilled compose state, opens feed renders mock page, reaction click calls client, attachment PDF opens preview.
- [ ] Implement views against mock + live client.
- [ ] `bun run test:desktop && bun run --cwd apps/desktop/frontend typecheck` — green. Commit: `feat(desktop): M2.5 compose extras and contact context parity`.

### Task 20: Mobile parity

**Files:**
- `apps/mobile/app/compose.tsx` (plain-text signature auto-append from selected account: `htmlToText`-equivalent strip; Smart Send suggestion line under the schedule row)
- `apps/mobile/app/thread/[id].tsx` (long-press message → reaction row 👍 ❤️ 😂 🎉 ✅; reaction chips)
- `apps/mobile/app/(tabs)/settings.tsx` (signature + auto-BCC editors per account, plain inputs)
- `apps/mobile/app/(tabs)/inbox.tsx` (Opens sheet: header button opens a modal list backed by `listOpens`, load-more on end-reached)
- jest tests alongside (`__tests__/` per Expo convention already used by the suite)

**Interfaces:** mobile scope per feature map: Smart Send, Recent Opens, Auto Bcc (settings), signatures, attachment browsing is deferred to the list already shown per-thread (quick-access pane is web/desktop P2 scope), reactions included. All via shared `ApiClient`.

**Steps:**
- [ ] Failing jest tests: signature appended once on account select; opens sheet renders page and paginates; reaction long-press calls `reactToMessage`; settings save calls `setSignature`/`setAutoBcc`.
- [ ] Implement.
- [ ] `bun run test:mobile` — green. Commit: `feat(mobile): M2.5 compose extras parity`.

### Task 21: Full gates, e2e, docs

**Files:**
- `apps/web/e2e/` (extend the demo-mode Playwright suite: signature appears in compose, opens feed opens and lists, attachment search finds the demo PDF and previews it, reaction chip appears after react, contact pane opens from a thread)
- `docs/feature-map.md` (flip the eight M2.5 rows from `planned` to `scaffolded`/shipped status wording used by M1 rows)

**Steps:**
- [ ] Add the five e2e specs against `DEMO_MODE` fixtures; `bun run test:e2e` locally — green.
- [ ] Update feature-map statuses for: Smart Send, Instant Intro, Recent Opens feed, Auto Bcc, Emoji reactions, Per-account signatures, Attachment quick access, Contact pane (+ Attachment previews platform row).
- [ ] Full gate: `bun run lint && bun run test && bun run typecheck` — all green (Docker running for test:api).
- [ ] Commit: `feat: complete M2.5 compose extras & contact context`; push (lefthook pre-push re-runs the gates).
