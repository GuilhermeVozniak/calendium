package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
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

func TestMessageRepoListOpensOrderingAndExclusions(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1") // email is "u1+google@example.com"
	th := seedThread(t, st, acct.ID, time.Now())

	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	opened1 := base                    // oldest open
	opened2 := base.Add(1 * time.Hour) // middle open
	opened3 := base.Add(2 * time.Hour) // newest open

	oldest, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "oldest open",
		SentAt: base.Add(-3 * time.Hour), OpenedAt: &opened1,
	})
	if err != nil {
		t.Fatalf("Upsert oldest: %v", err)
	}
	middle, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "middle open",
		SentAt: base.Add(-2 * time.Hour), OpenedAt: &opened2,
	})
	if err != nil {
		t.Fatalf("Upsert middle: %v", err)
	}
	newest, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "newest open",
		SentAt: base.Add(-1 * time.Hour), OpenedAt: &opened3,
	})
	if err != nil {
		t.Fatalf("Upsert newest: %v", err)
	}
	// Sent but never opened: excluded regardless of from address.
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "unopened", SentAt: base,
	}); err != nil {
		t.Fatalf("Upsert unopened: %v", err)
	}
	// Received (not sent) and opened: excluded because from != account email.
	receivedOpen := base.Add(3 * time.Hour)
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "someone-else@example.com"}, Subject: "inbound opened",
		SentAt: base, OpenedAt: &receivedOpen,
	}); err != nil {
		t.Fatalf("Upsert received: %v", err)
	}

	// A different user's account/thread/message: excluded even though opened.
	seedUser(t, st, "u2")
	acct2 := seedAccount(t, st, "u2")
	th2 := seedThread(t, st, acct2.ID, time.Now())
	otherOpen := base.Add(4 * time.Hour)
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th2.ID, AccountID: acct2.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct2.Email}, Subject: "other user open",
		SentAt: base, OpenedAt: &otherOpen,
	}); err != nil {
		t.Fatalf("Upsert other user: %v", err)
	}

	// Full page: newest open first.
	page, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("ListOpens: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("ListOpens Items = %+v, want 3 items", page.Items)
	}
	if page.Items[0].MessageID != newest.ID || page.Items[1].MessageID != middle.ID || page.Items[2].MessageID != oldest.ID {
		t.Fatalf("ListOpens order = %+v, want [newest, middle, oldest]", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("NextCursor = %v, want nil (only 3 items, limit 10)", *page.NextCursor)
	}
	if page.Items[0].ThreadID != th.ID || page.Items[0].AccountID != acct.ID || page.Items[0].Subject != "newest open" {
		t.Fatalf("newest item fields = %+v", page.Items[0])
	}

	// Keyset pagination: limit=2 yields a NextCursor; the second page yields
	// exactly the third (oldest) row and no further cursor.
	page1, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 2})
	if err != nil {
		t.Fatalf("ListOpens page1: %v", err)
	}
	if len(page1.Items) != 2 || page1.Items[0].MessageID != newest.ID || page1.Items[1].MessageID != middle.ID {
		t.Fatalf("page1 Items = %+v, want [newest, middle]", page1.Items)
	}
	if page1.NextCursor == nil {
		t.Fatal("page1 NextCursor = nil, want a cursor")
	}

	page2, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 2, Cursor: *page1.NextCursor})
	if err != nil {
		t.Fatalf("ListOpens page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].MessageID != oldest.ID {
		t.Fatalf("page2 Items = %+v, want [oldest]", page2.Items)
	}
	if page2.NextCursor != nil {
		t.Fatalf("page2 NextCursor = %v, want nil (feed exhausted)", *page2.NextCursor)
	}
}

func TestMessageRepoListOpensTieBreakOnID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	// Two messages opened at the exact same instant: the tiebreaker is id DESC.
	tie := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	m1, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "tie a",
		SentAt: tie.Add(-1 * time.Hour), OpenedAt: &tie,
	})
	if err != nil {
		t.Fatalf("Upsert m1: %v", err)
	}
	m2, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, Subject: "tie b",
		SentAt: tie.Add(-1 * time.Hour), OpenedAt: &tie,
	})
	if err != nil {
		t.Fatalf("Upsert m2: %v", err)
	}

	// Compute expected order: id DESC among the tied pair.
	want := []string{m1.ID, m2.ID}
	if want[0] < want[1] {
		want[0], want[1] = want[1], want[0]
	}

	full, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("ListOpens: %v", err)
	}
	if len(full.Items) != 2 || full.Items[0].MessageID != want[0] || full.Items[1].MessageID != want[1] {
		t.Fatalf("ListOpens tie order = %+v, want %v", full.Items, want)
	}

	// Cursor at the boundary between the tied pair (limit=1): page2 must
	// return exactly the second-in-order row, not re-return the first or skip
	// past both.
	page1, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 1})
	if err != nil {
		t.Fatalf("ListOpens page1: %v", err)
	}
	if len(page1.Items) != 1 || page1.Items[0].MessageID != want[0] || page1.NextCursor == nil {
		t.Fatalf("page1 = %+v", page1)
	}
	page2, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1", Limit: 1, Cursor: *page1.NextCursor})
	if err != nil {
		t.Fatalf("ListOpens page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].MessageID != want[1] {
		t.Fatalf("page2 = %+v, want [%s]", page2.Items, want[1])
	}
	if page2.NextCursor != nil {
		t.Fatalf("page2 NextCursor = %v, want nil", *page2.NextCursor)
	}
}

func TestMessageRepoListOpensEmpty(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	page, err := st.Messages().ListOpens(ctx, port.OpensQuery{UserID: "u1"})
	if err != nil {
		t.Fatalf("ListOpens: %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("ListOpens empty feed Items = %+v, want empty non-nil slice", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("NextCursor = %v, want nil", *page.NextCursor)
	}
}
