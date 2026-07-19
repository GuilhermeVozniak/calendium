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

func TestMessageRepoOpenHourHistogram(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	opened9a := time.Date(2026, 2, 1, 9, 15, 0, 0, time.UTC)
	opened9b := time.Date(2026, 2, 2, 9, 45, 0, 0, time.UTC)
	opened14 := time.Date(2026, 2, 3, 14, 0, 0, 0, time.UTC)

	seed := func(to, from string, opened *time.Time) {
		if _, err := st.Messages().Upsert(ctx, domain.Message{
			ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
			From:     domain.EmailAddress{Email: from},
			To:       []domain.EmailAddress{{Email: to}},
			SentAt:   time.Now().UTC(),
			OpenedAt: opened,
		}); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	seed("recipient@example.com", acct.Email, &opened9a)
	seed("recipient@example.com", acct.Email, &opened9b)
	seed("recipient@example.com", acct.Email, &opened14)
	seed("recipient@example.com", acct.Email, nil)                   // not opened: excluded
	seed("someone-else@example.com", acct.Email, &opened9a)          // different recipient: excluded
	seed("recipient@example.com", "external@example.com", &opened9a) // not this user's own send: excluded

	hist, err := st.Messages().OpenHourHistogram(ctx, "u1", "recipient@example.com")
	if err != nil {
		t.Fatalf("OpenHourHistogram: %v", err)
	}
	if hist[9] != 2 {
		t.Fatalf("hist[9] = %d, want 2", hist[9])
	}
	if hist[14] != 1 {
		t.Fatalf("hist[14] = %d, want 1", hist[14])
	}
	total := 0
	for _, c := range hist {
		total += c
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (unwanted rows leaked in)", total)
	}
}

func TestMessageRepoContactSummary(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	const contact = "contact@example.com"
	nameOld, nameNew := "Old Name", "New Name"

	th1, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "t1", Split: domain.SplitImportant,
		InInbox: true, MessageCount: 1, LastMessageAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Participants: []domain.EmailAddress{{Email: contact}},
	})
	if err != nil {
		t.Fatalf("seed th1: %v", err)
	}
	th2, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "t2", Split: domain.SplitImportant,
		InInbox: true, MessageCount: 1, LastMessageAt: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC),
		Participants: []domain.EmailAddress{{Email: contact}},
	})
	if err != nil {
		t.Fatalf("seed th2: %v", err)
	}
	// A third thread with no relation to the contact, to prove it's excluded.
	if _, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "unrelated", Split: domain.SplitImportant,
		InInbox: true, MessageCount: 1, LastMessageAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed unrelated thread: %v", err)
	}

	// Older message from the contact.
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th1.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Name: &nameOld, Email: contact}, SentAt: time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed older message: %v", err)
	}
	// Message the user sent to the contact.
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th1.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct.Email}, To: []domain.EmailAddress{{Email: contact}},
		SentAt: time.Date(2026, 2, 2, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed sent message: %v", err)
	}
	// Newest message from the contact, with a different display name.
	newest := time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC)
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th2.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Name: &nameNew, Email: contact}, SentAt: newest,
	}); err != nil {
		t.Fatalf("seed newest message: %v", err)
	}

	summary, err := st.Messages().ContactSummary(ctx, "u1", strings.ToUpper(contact))
	if err != nil {
		t.Fatalf("ContactSummary: %v", err)
	}
	if summary.Email != contact {
		t.Fatalf("Email = %q, want %q", summary.Email, contact)
	}
	if summary.Domain != "example.com" {
		t.Fatalf("Domain = %q, want example.com", summary.Domain)
	}
	if summary.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", summary.MessageCount)
	}
	if summary.Name == nil || *summary.Name != nameNew {
		t.Fatalf("Name = %v, want %q (newest message's name)", summary.Name, nameNew)
	}
	if summary.LastMessageAt == nil || !summary.LastMessageAt.Equal(newest) {
		t.Fatalf("LastMessageAt = %v, want %v", summary.LastMessageAt, newest)
	}
	if summary.ThreadCount != 2 {
		t.Fatalf("ThreadCount = %d, want 2", summary.ThreadCount)
	}
	if len(summary.RecentThreads) != 2 || summary.RecentThreads[0].ID != th2.ID || summary.RecentThreads[1].ID != th1.ID {
		t.Fatalf("RecentThreads = %+v, want [th2, th1] newest first", summary.RecentThreads)
	}
}

func TestMessageRepoContactSummaryEmptyMirror(t *testing.T) {
	st, _ := newTestStore(t)
	summary, err := st.Messages().ContactSummary(context.Background(), "no-such-user", "nobody@example.com")
	if err != nil {
		t.Fatalf("ContactSummary: %v", err)
	}
	if summary.MessageCount != 0 {
		t.Fatalf("MessageCount = %d, want 0", summary.MessageCount)
	}
	if summary.ThreadCount != 0 {
		t.Fatalf("ThreadCount = %d, want 0", summary.ThreadCount)
	}
	if summary.Name != nil {
		t.Fatalf("Name = %v, want nil", summary.Name)
	}
	if summary.LastMessageAt != nil {
		t.Fatalf("LastMessageAt = %v, want nil", summary.LastMessageAt)
	}
	if summary.RecentThreads == nil || len(summary.RecentThreads) != 0 {
		t.Fatalf("RecentThreads = %+v, want non-nil empty slice", summary.RecentThreads)
	}
}

func TestMessageRepoSearchAttachments(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	acct1 := seedAccount(t, st, "u1")
	acct2 := seedAccount(t, st, "u2")
	th1 := seedThread(t, st, acct1.ID, time.Now())
	th1b := seedThread(t, st, acct1.ID, time.Now())
	th2 := seedThread(t, st, acct2.ID, time.Now())

	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Hour)
	t2 := t0.Add(2 * time.Hour)

	// m1: report from the contact, in th1.
	m1, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th1.ID, AccountID: acct1.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "contact@example.com"}, SentAt: t0,
		Attachments: []domain.Attachment{{Filename: "Report.pdf", MimeType: "application/pdf", SizeBytes: 10, ProviderAttachmentID: "gmail-att-1"}},
	})
	if err != nil {
		t.Fatalf("seed m1: %v", err)
	}
	// m2: photo sent by acct1 to the contact, in th1b.
	m2, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th1b.ID, AccountID: acct1.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: acct1.Email}, To: []domain.EmailAddress{{Email: "contact@example.com"}},
		SentAt:      t1,
		Attachments: []domain.Attachment{{Filename: "photo.png", MimeType: "image/png", SizeBytes: 20}},
	})
	if err != nil {
		t.Fatalf("seed m2: %v", err)
	}
	// m3: an invoice-report cc'ing the contact, in th1 (newest).
	m3, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th1.ID, AccountID: acct1.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "other@example.com"}, Cc: []domain.EmailAddress{{Email: "contact@example.com"}},
		SentAt:      t2,
		Attachments: []domain.Attachment{{Filename: "invoice-report.pdf", MimeType: "application/pdf", SizeBytes: 30}},
	})
	if err != nil {
		t.Fatalf("seed m3: %v", err)
	}
	// u2's own report attachment: must never leak into u1's search.
	if _, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th2.ID, AccountID: acct2.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "contact@example.com"}, SentAt: t2,
		Attachments: []domain.Attachment{{Filename: "report.pdf", MimeType: "application/pdf", SizeBytes: 5}},
	}); err != nil {
		t.Fatalf("seed u2 message: %v", err)
	}

	// Case-insensitive filename search.
	byName, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u1", Query: "report"})
	if err != nil {
		t.Fatalf("SearchAttachments by name: %v", err)
	}
	if len(byName.Items) != 2 || byName.Items[0].MessageID != m3.ID || byName.Items[1].MessageID != m1.ID {
		t.Fatalf("byName = %+v, want [m3, m1] newest first", byName.Items)
	}

	// Contact filter matches from/to/cc.
	byContact, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u1", Contact: "contact@example.com"})
	if err != nil {
		t.Fatalf("SearchAttachments by contact: %v", err)
	}
	if len(byContact.Items) != 3 {
		t.Fatalf("byContact = %+v, want 3 items (from/to/cc)", byContact.Items)
	}

	// Thread filter.
	byThread, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u1", ThreadID: th1.ID})
	if err != nil {
		t.Fatalf("SearchAttachments by thread: %v", err)
	}
	if len(byThread.Items) != 2 || byThread.Items[0].MessageID != m3.ID || byThread.Items[1].MessageID != m1.ID {
		t.Fatalf("byThread = %+v, want [m3, m1]", byThread.Items)
	}

	// User isolation: u2 only sees its own attachment.
	u2Hits, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u2", Query: "report"})
	if err != nil {
		t.Fatalf("SearchAttachments u2: %v", err)
	}
	if len(u2Hits.Items) != 1 || u2Hits.Items[0].From.Email != "contact@example.com" {
		t.Fatalf("u2Hits = %+v, want u2's own report.pdf only", u2Hits.Items)
	}

	// Pagination: limit 1 -> cursor -> page 2.
	page1, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u1", Limit: 1})
	if err != nil {
		t.Fatalf("SearchAttachments page1: %v", err)
	}
	if len(page1.Items) != 1 || page1.NextCursor == nil {
		t.Fatalf("page1 = %+v, want 1 item with a cursor", page1)
	}
	if page1.Items[0].MessageID != m3.ID {
		t.Fatalf("page1 item = %+v, want m3's attachment (newest)", page1.Items[0])
	}
	page2, err := st.Messages().SearchAttachments(ctx, port.AttachmentQuery{UserID: "u1", Limit: 1, Cursor: *page1.NextCursor})
	if err != nil {
		t.Fatalf("SearchAttachments page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ID == page1.Items[0].ID {
		t.Fatalf("page2 = %+v, want a different single item than page1", page2.Items)
	}
	if page2.Items[0].MessageID != m2.ID {
		t.Fatalf("page2 item = %+v, want m2's attachment", page2.Items[0])
	}
}

func TestMessageRepoGetAttachment(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	created, err := st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: newID(),
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: time.Now(),
		Attachments: []domain.Attachment{{Filename: "x.pdf", MimeType: "application/pdf", SizeBytes: 1, ProviderAttachmentID: "prov-123"}},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	msg, err := st.Messages().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	attID := msg.Attachments[0].ID

	att, msgID, err := st.Messages().GetAttachment(ctx, attID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if msgID != created.ID {
		t.Fatalf("msgID = %q, want %q", msgID, created.ID)
	}
	if att.ProviderAttachmentID != "prov-123" {
		t.Fatalf("ProviderAttachmentID = %q, want prov-123 (replaceAttachments must persist it)", att.ProviderAttachmentID)
	}

	if _, _, err := st.Messages().GetAttachment(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
