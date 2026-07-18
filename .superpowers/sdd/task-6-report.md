# Task 6 report — Provider gateways: FetchAttachment + attachment-id ingest

## Setup
- Worktree based on main; `git merge feat/m2-5-compose` fast-forwarded cleanly (a23d019), no conflicts.
- Confirmed Task 1 had left `FetchAttachment` "not implemented" stubs in both
  `backend/internal/adapter/out/googleapi/mail.go` and
  `backend/internal/adapter/out/msgraph/mail.go`, and that
  `domain.Attachment.ProviderAttachmentID` already existed (added by the
  merged Task 1/compose work).

## Implementation

### googleapi (Gmail)
- `mail.go`: `FetchAttachment` now calls
  `GET /gmail/v1/users/me/messages/{mid}/attachments/{aid}` via the existing
  `doJSON` (so 401/403 → `domain.ErrUnauthorized`, 404 → `domain.ErrNotFound`
  automatically), decodes the `data` field as base64url
  (`base64.URLEncoding.WithPadding(base64.NoPadding)` after trimming `=`,
  matching the existing `decodeBase64URL` convention), and returns mimeType
  `""` per the brief (Gmail's attachments.get response carries no mime type;
  callers fall back to the mirrored mime type from message metadata).
- `gmail_types.go`: `extractParts` now also sets
  `ProviderAttachmentID: p.Body.AttachmentID` on parsed attachment metadata,
  alongside the pre-existing `ID: p.Body.AttachmentID` field (left untouched
  — that field's reuse as a provider-id-as-local-id is pre-existing behavior
  outside this task's scope, owned by the parallel postgres track).

### msgraph (Graph)
- `client.go`: added `doRaw`, a sibling to `doJSON` for non-JSON bodies —
  same bearer-token/error-envelope/`*httpError` conventions (so 401/403/404
  map to the same domain sentinels), but returns raw bytes + the response's
  `Content-Type` header instead of JSON-decoding. Capped at 64MB
  (`maxAttachmentBytes`) via `io.LimitReader` as a defensive ceiling.
- `mail.go`: `FetchAttachment` calls
  `GET /v1.0/me/messages/{mid}/attachments/{aid}/$value` via `doRaw` and
  returns the body + Content-Type verbatim.
- Attachment-id ingest gap: unlike Gmail, the Graph adapter's `SyncMail`
  never parsed attachment metadata at all (delta `$select` had no
  attachment fields; `mapGraphMessage` always set `Attachments: []`). To
  satisfy "populate `ProviderAttachmentID`" for Graph, added:
  - `hasAttachments` to `deltaSelect`.
  - `graphMessage.HasAttachments bool`.
  - `fetchAttachmentMeta`: a new per-message call to
    `GET /v1.0/me/messages/{id}/attachments?$select=id,name,contentType,size`
    (metadata only, no `contentBytes`), invoked from the `SyncMail` loop only
    when `gm.HasAttachments` is true, populating
    `Filename`/`MimeType`/`SizeBytes`/`ProviderAttachmentID`.
  - This is adapter-internal (no port change): `port.MailProvider` and
    `port.IncomingMessage`/`domain.Message.Attachments` shapes are unchanged.
    Existing delta tests are unaffected since none of their fixtures set
    `hasAttachments`, so no extra HTTP call fires for them (the mock server's
    `default: t.Errorf` case would have caught an unexpected call).

## Tests added (httptest-based, TDD)
- googleapi: `TestClient_FetchAttachment_DecodesBase64URL` (hits the right
  URL, bearer header, exact byte round-trip through base64url, empty
  mimeType), `TestClient_FetchAttachment_NotFound` (→ `ErrNotFound`),
  `TestClient_FetchAttachment_BadDataErrors` (malformed base64 → error, not
  silently swallowed), `TestClient_SyncMail_AttachmentProviderID` (thread
  with an attachment part → `ProviderAttachmentID` set from
  `body.attachmentId`).
- msgraph: `TestClient_SyncMail_AttachmentProviderID` (`hasAttachments:true`
  triggers the metadata fetch with the expected `$select`, populates
  filename/mimeType/size/`ProviderAttachmentID`), 
  `TestClient_FetchAttachment_RawBodyAndContentType` (right URL, bearer
  header, raw byte round-trip, Content-Type passthrough),
  `TestClient_FetchAttachment_NotFound` (→ `ErrNotFound`),
  `TestClient_FetchAttachment_Unauthorized` (→ `ErrUnauthorized`).

## Validation
- `gofmt -l backend` — clean.
- `cd backend && go test ./internal/adapter/out/googleapi/... ./internal/adapter/out/msgraph/...` — 171 passed.
- `cd backend && go build ./... && go test ./...` — 1561 passed, 17 packages.
- `bun run lint:go` — 0 issues (both `backend` and `apps/desktop`).

## Commit
`609ca1c` — `feat(backend): provider attachment download + attachment-id ingest`
(single commit, 6 files changed, +317/-11), with the required trailers.

## Concerns / follow-ups for other tracks
- Graph's per-message attachment-metadata fetch adds one extra Graph API
  call per synced message that has attachments (unavoidable given the delta
  endpoint never carried attachment info) — acceptable for M2.5 scope but
  worth knowing if sync latency/rate-limit budgets get scrutinized later.
- Gmail's `Attachment.ID` continues to be set to the provider attachment id
  at ingest (pre-existing behavior, not touched here) while the new
  `ProviderAttachmentID` field carries the same value under its documented
  contract; the postgres track's `replaceAttachments` (`a.ID == "" → newID()`)
  means a non-empty `ID` from Gmail bypasses local id generation — flagging
  in case the postgres agent wants to normalize this, but it's out of this
  task's file ownership.

## Fix — review follow-up (commit 609ca1c review, applied on feat/m2-5-compose)

Two findings from review:

1. **MODERATE**: Gmail `FetchAttachment` routed through `doJSON`, whose
   `io.LimitReader` capped response bodies at 8MB (`defaultJSONBodyLimit`,
   sized for message metadata) — but the attachment envelope's base64url
   `data` field inflates the raw attachment size ~33%, so any attachment
   over ~6MB raw silently truncated into an opaque JSON-decode error.
   Graph's `doRaw` already had a dedicated 64MB cap for this same case.
   Fix: `client.go` now has `doJSONLimit(ctx, method, url, token, body, out,
   maxBytes)`, parameterized by cap; `doJSON` delegates to it with the
   existing `defaultJSONBodyLimit` (8MB) unchanged. It reads
   `maxBytes+1` bytes so an oversized 2xx body is distinguishable from one
   that exactly fits, and returns a clean `domain.ErrValidation`-wrapped
   error instead of silently truncating. `FetchAttachment` (`mail.go`) now
   calls `doJSONLimit` with a new `maxAttachmentBytes = 64 << 20`, matching
   Graph's cap.
2. **MINOR**: `TestClient_FetchAttachment_DecodesBase64URL` used a
   30-byte payload (`len % 3 == 0`), so the padding-trim path
   (`strings.TrimRight(res.Data, "=")` before unpadded decode) was never
   exercised. Made the test table-driven with three cases —
   `len % 3 == 0` (no padding), `== 1` (two `=` padding chars), `== 2` (one
   `=` padding char) — each asserting exact decoded-byte equality.

Tests added:
- `TestClient_FetchAttachment_LargePayloadUnderNewCap` (`client_test.go`
  package, `mail_test.go`): a 9MB raw payload (~12MB base64-encoded, over
  the old 8MB cap, under the new 64MB one) decodes fully; asserts exact
  length and a SHA-256 hash match against the original bytes.
- `TestClient_DoJSONLimit_ExceedsCapReturnsValidationError`
  (`client_test.go`): calls `doJSONLimit` directly with a small `maxBytes`
  (16) so the truncation-detected-error path is exercised cheaply, without
  allocating anywhere near 64MB; asserts `errors.Is(err,
  domain.ErrValidation)`.
- `TestClient_FetchAttachment_DecodesBase64URL` extended to table-driven
  padding-class coverage (see finding 2 above).

Validation: `cd backend && go test ./internal/adapter/out/googleapi/`
(93 passed) `&& go test ./...` (1593 passed, 17 packages) `&& gofmt -l .`
(clean) `&& bun run lint:go` (0 issues, both `backend` and
`apps/desktop`).

Commit: `fix(backend): dedicated size cap for Gmail attachment downloads`.
