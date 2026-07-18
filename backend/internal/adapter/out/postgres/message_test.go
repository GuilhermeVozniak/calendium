package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestMessageRepoUpsertGetByIDAndProviderID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	pmid := newID()
	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	created, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID:          th.ID,
		AccountID:         acct.ID,
		ProviderMessageID: pmid,
		From:              domain.EmailAddress{Email: "sender@example.com"},
		Subject:           "Hi",
		SentAt:            sentAt,
		Attachments: []domain.Attachment{
			{Filename: "a.pdf", MimeType: "application/pdf", SizeBytes: 100},
		},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	byID, err := st.Messages().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(byID.Attachments) != 1 || byID.Attachments[0].Filename != "a.pdf" {
		t.Fatalf("Attachments = %+v", byID.Attachments)
	}
	if byID.To == nil || byID.Cc == nil || byID.Bcc == nil {
		t.Fatalf("To/Cc/Bcc must normalize to non-nil empty: %+v", byID)
	}

	byProvider, err := st.Messages().GetByProviderID(ctx, acct.ID, pmid)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	if byProvider.ID != byID.ID {
		t.Fatalf("GetByProviderID id = %s, want %s", byProvider.ID, byID.ID)
	}
}

func TestMessageRepoUpsertReplacesAttachments(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	pmid := newID()
	sentAt := time.Now().UTC().Truncate(time.Microsecond)
	created, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: pmid,
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: sentAt,
		Attachments: []domain.Attachment{
			{Filename: "a.pdf", MimeType: "application/pdf", SizeBytes: 100},
			{Filename: "b.pdf", MimeType: "application/pdf", SizeBytes: 200},
		},
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	first, err := st.Messages().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(first.Attachments) != 2 {
		t.Fatalf("first Attachments = %+v, want 2", first.Attachments)
	}

	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: pmid,
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: sentAt,
		Attachments: []domain.Attachment{
			{Filename: "a.pdf", MimeType: "application/pdf", SizeBytes: 100},
		},
	}); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	second, err := st.Messages().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID after resync: %v", err)
	}
	if len(second.Attachments) != 1 || second.Attachments[0].Filename != "a.pdf" {
		t.Fatalf("second Attachments = %+v, want only a.pdf", second.Attachments)
	}
}

func TestMessageRepoListByThread(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	t0 := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Hour)
	m1, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "a@example.com"}, SentAt: t0,
		Attachments: []domain.Attachment{{Filename: "first.txt", MimeType: "text/plain"}},
	})
	if err != nil {
		t.Fatalf("Upsert m1: %v", err)
	}
	m2, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "b@example.com"}, SentAt: t1,
		Attachments: []domain.Attachment{{Filename: "second.txt", MimeType: "text/plain"}},
	})
	if err != nil {
		t.Fatalf("Upsert m2: %v", err)
	}

	list, err := st.Messages().ListByThread(ctx, th.ID)
	if err != nil {
		t.Fatalf("ListByThread: %v", err)
	}
	if len(list) != 2 || list[0].ID != m1.ID || list[1].ID != m2.ID {
		t.Fatalf("ListByThread order = %+v, want [m1, m2]", list)
	}
	if len(list[0].Attachments) != 1 || list[0].Attachments[0].Filename != "first.txt" {
		t.Fatalf("list[0] Attachments = %+v", list[0].Attachments)
	}
	if len(list[1].Attachments) != 1 || list[1].Attachments[0].Filename != "second.txt" {
		t.Fatalf("list[1] Attachments = %+v", list[1].Attachments)
	}
}

func TestMessageRepoGetByIDMissing(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Messages().GetByID(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMessageRepoListSentByAccount(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1") // email is "u1+google@example.com"
	th := seedThread(t, st, acct.ID, time.Now())

	t0 := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Hour)
	t2 := t0.Add(2 * time.Hour)

	// Older sent message from the account's own address.
	older, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "older sent", SentAt: t0,
	})
	if err != nil {
		t.Fatalf("Upsert older: %v", err)
	}
	// Newer sent message, mixed case to prove the match is case-insensitive.
	newer, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: strings.ToUpper(acct.Email)}, Subject: "newer sent", SentAt: t2,
	})
	if err != nil {
		t.Fatalf("Upsert newer: %v", err)
	}
	// Inbound message from someone else on the same account/thread: excluded.
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "someone-else@example.com"}, Subject: "inbound", SentAt: t1,
	}); err != nil {
		t.Fatalf("Upsert inbound: %v", err)
	}

	list, err := st.Messages().ListSentByAccount(ctx, acct.ID, acct.Email, 10)
	if err != nil {
		t.Fatalf("ListSentByAccount: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("ListSentByAccount = %+v, want [newer, older]", list)
	}

	limited, err := st.Messages().ListSentByAccount(ctx, acct.ID, acct.Email, 1)
	if err != nil {
		t.Fatalf("ListSentByAccount limit: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != newer.ID {
		t.Fatalf("ListSentByAccount limit=1 = %+v, want [newer]", limited)
	}
}
