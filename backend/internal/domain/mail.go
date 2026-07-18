package domain

import (
	"fmt"
	"time"
)

// InboxSplit is a Superhuman-style split-inbox category, assigned at ingest
// by the classification heuristic in internal/service.
type InboxSplit string

const (
	SplitImportant InboxSplit = "important"
	SplitVIP       InboxSplit = "vip"
	SplitTeam      InboxSplit = "team"
	SplitCalendar  InboxSplit = "calendar"
	SplitNews      InboxSplit = "news"
	SplitSocial    InboxSplit = "social"
	SplitOther     InboxSplit = "other"
)

// ParseInboxSplit validates a split query parameter.
func ParseInboxSplit(s string) (InboxSplit, error) {
	switch InboxSplit(s) {
	case SplitImportant, SplitVIP, SplitTeam, SplitCalendar, SplitNews, SplitSocial, SplitOther:
		return InboxSplit(s), nil
	}
	return "", fmt.Errorf("%w: unknown inbox split %q", ErrValidation, s)
}

// ThreadView is a cross-split pseudo-view selected via the `view` query
// parameter (never a labelId): starred conversations, snoozed conversations
// still in the future, or conversations the account owner has sent to.
type ThreadView string

const (
	ThreadViewStarred ThreadView = "starred"
	ThreadViewSnoozed ThreadView = "snoozed"
	ThreadViewSent    ThreadView = "sent"
)

// ParseThreadView validates a view query parameter.
func ParseThreadView(s string) (ThreadView, error) {
	switch ThreadView(s) {
	case ThreadViewStarred, ThreadViewSnoozed, ThreadViewSent:
		return ThreadView(s), nil
	}
	return "", fmt.Errorf("%w: unknown thread view %q", ErrValidation, s)
}

// EmailAddress is a display name + address pair.
type EmailAddress struct {
	Name  *string `json:"name"`
	Email string  `json:"email"`
}

// LabelKind distinguishes provider/system labels from user-created ones.
type LabelKind string

const (
	LabelKindSystem LabelKind = "system"
	LabelKindUser   LabelKind = "user"
)

// Label is a mail label/folder mirrored from the provider.
type Label struct {
	ID              string    `json:"id"`
	AccountID       string    `json:"accountId"`
	ProviderLabelID string    `json:"-"`
	Name            string    `json:"name"`
	Kind            LabelKind `json:"kind"`
	Color           *string   `json:"color"`
}

// Thread is a mail conversation mirrored into Postgres for
// Superhuman-grade list latency.
type Thread struct {
	ID               string         `json:"id"`
	AccountID        string         `json:"accountId"`
	ProviderThreadID string         `json:"-"`
	Subject          string         `json:"subject"`
	Snippet          string         `json:"snippet"`
	Participants     []EmailAddress `json:"participants"`
	LabelIDs         []string       `json:"labelIds"`
	Split            InboxSplit     `json:"split"`
	MessageCount     int            `json:"messageCount"`
	Unread           bool           `json:"unread"`
	Starred          bool           `json:"starred"`
	// InInbox mirrors provider inbox membership: archive/trash/spam clear it
	// (and write through to the provider) so triaged threads leave the inbox
	// list instead of reappearing on every refetch. Provider sync recomputes
	// it from the thread's labels/folder. Not serialized: clients scope by
	// split/view, never by this flag.
	InInbox       bool      `json:"-"`
	LastMessageAt time.Time `json:"lastMessageAt"`
	// OpenedAt is set the first time the owner opens the thread (POST
	// /v1/mail/threads/{id}/open); null until then. Real read state, not mock.
	OpenedAt *time.Time `json:"openedAt"`
	// SnoozedUntil hides the thread from the inbox until it elapses; the
	// worker resurfaces it (unread + push) when due.
	SnoozedUntil *time.Time `json:"snoozedUntil"`
	// RemindAt is a follow-up reminder: resurface if nobody replies by then.
	RemindAt *time.Time `json:"remindAt"`
	// UnsubscribeMailto / UnsubscribeURL / UnsubscribeOneClick are parsed from
	// the newest message's List-Unsubscribe / List-Unsubscribe-Post headers at
	// sync ingest (RFC 2369 / RFC 8058). All zero when the sender offers no
	// unsubscribe. OneClick means the URL accepts the RFC 8058 POST.
	UnsubscribeMailto   *string `json:"unsubscribeMailto"`
	UnsubscribeURL      *string `json:"unsubscribeUrl"`
	UnsubscribeOneClick bool    `json:"unsubscribeOneClick"`
	// Summary is the AI-generated thread summary (empty until generated).
	Summary string `json:"summary,omitempty"`
	// InstantReplies is a cache of AI-generated reply suggestions.
	InstantReplies []string `json:"instantReplies,omitempty"`
}

// Attachment is file metadata on a message (bodies are fetched on demand).
type Attachment struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Message is a single mail message within a thread.
type Message struct {
	ID                string         `json:"id"`
	ThreadID          string         `json:"threadId"`
	AccountID         string         `json:"accountId"`
	ProviderMessageID string         `json:"-"`
	From              EmailAddress   `json:"from"`
	To                []EmailAddress `json:"to"`
	Cc                []EmailAddress `json:"cc"`
	Bcc               []EmailAddress `json:"bcc"`
	Subject           string         `json:"subject"`
	BodyHTML          string         `json:"bodyHtml"`
	BodyText          string         `json:"bodyText"`
	Attachments       []Attachment   `json:"attachments"`
	SentAt            time.Time      `json:"sentAt"`
	IsDraft           bool           `json:"isDraft"`
	// OpenedAt is read-status tracking (Superhuman read receipts).
	OpenedAt *time.Time `json:"openedAt"`
}

// Draft is an unsent message. Sending is always scheduled: "send now" sets
// ScheduledAt to now + the undo-send grace period, Send Later sets it to the
// requested time; the worker delivers due drafts. Clearing ScheduledAt before
// it elapses is "undo send".
type Draft struct {
	ID          string         `json:"id"`
	AccountID   string         `json:"accountId"`
	ThreadID    *string        `json:"threadId"`
	To          []EmailAddress `json:"to"`
	Cc          []EmailAddress `json:"cc"`
	Bcc         []EmailAddress `json:"bcc"`
	Subject     string         `json:"subject"`
	BodyHTML    string         `json:"bodyHtml"`
	ScheduledAt *time.Time     `json:"scheduledAt"`
	// SendAttempts counts worker delivery attempts; after the retry cap the
	// draft is dead-lettered (scheduledAt cleared) with LastError set.
	SendAttempts int       `json:"sendAttempts"`
	LastError    *string   `json:"lastError"`
	UpdatedAt    time.Time `json:"updatedAt"`
	// AiGenerated marks this draft as AI-generated (e.g., auto-reply, auto-draft).
	AiGenerated bool `json:"aiGenerated"`
}

// Snippet is a reusable canned response with an optional keyboard shortcut.
type Snippet struct {
	ID         string  `json:"id"`
	UserID     string  `json:"-"`
	Name       string  `json:"name"`
	Shortcut   *string `json:"shortcut"`
	BodyHTML   string  `json:"bodyHtml"`
	UsageCount int     `json:"usageCount"`
}

// ThreadAction is a one-shot mutation on a thread.
type ThreadAction string

const (
	ThreadActionArchive     ThreadAction = "archive"
	ThreadActionTrash       ThreadAction = "trash"
	ThreadActionStar        ThreadAction = "star"
	ThreadActionUnstar      ThreadAction = "unstar"
	ThreadActionRead        ThreadAction = "read"
	ThreadActionUnread      ThreadAction = "unread"
	ThreadActionSpam        ThreadAction = "spam"
	ThreadActionMoveToInbox ThreadAction = "move_to_inbox"
)

// ParseThreadAction validates an action body parameter.
func ParseThreadAction(s string) (ThreadAction, error) {
	switch ThreadAction(s) {
	case ThreadActionArchive, ThreadActionTrash, ThreadActionStar, ThreadActionUnstar,
		ThreadActionRead, ThreadActionUnread, ThreadActionSpam, ThreadActionMoveToInbox:
		return ThreadAction(s), nil
	}
	return "", fmt.Errorf("%w: unknown thread action %q", ErrValidation, s)
}
