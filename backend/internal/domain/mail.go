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
	LastMessageAt    time.Time      `json:"lastMessageAt"`
	// SnoozedUntil hides the thread from the inbox until it elapses; the
	// worker resurfaces it (unread + push) when due.
	SnoozedUntil *time.Time `json:"snoozedUntil"`
	// RemindAt is a follow-up reminder: resurface if nobody replies by then.
	RemindAt *time.Time `json:"remindAt"`
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
