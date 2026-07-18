package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestThreadRepoListPaginationCursor(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ { // strictly increasing last_message_at
		seedThread(t, st, acct.ID, base.Add(time.Duration(i)*time.Hour))
	}

	page1, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
	}
	if page1.NextCursor == nil {
		t.Fatal("page1 NextCursor = nil, want a cursor")
	}
	// keyset order is last_message_at DESC.
	if !page1.Items[0].LastMessageAt.After(page1.Items[1].LastMessageAt) {
		t.Fatal("page not ordered by last_message_at DESC")
	}

	page2, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2, Cursor: *page1.NextCursor})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("page2 items = %d, want 2", len(page2.Items))
	}
	if page2.Items[0].ID == page1.Items[1].ID {
		t.Fatal("cursor overlap: page2 repeats the last row of page1")
	}

	// final page: 1 remaining, no further cursor.
	page3, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Limit: 2, Cursor: *page2.NextCursor})
	if err != nil {
		t.Fatalf("List page3: %v", err)
	}
	if len(page3.Items) != 1 || page3.NextCursor != nil {
		t.Fatalf("page3 = %d items, cursor=%v; want 1 item, nil cursor", len(page3.Items), page3.NextCursor)
	}
}

func TestThreadRepoListRequiresUser(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Threads().List(context.Background(), port.ThreadQuery{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestThreadRepoUpsertGetByProviderIDAndID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	pid := newID()
	created, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID:        acct.ID,
		ProviderThreadID: pid,
		Subject:          "hi there",
		Split:            domain.SplitOther,
		InInbox:          true,
		LastMessageAt:    time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if created.Participants == nil || created.LabelIDs == nil {
		t.Fatalf("nil slices must be normalized: %+v", created)
	}

	byProvider, err := st.Threads().GetByProviderID(ctx, acct.ID, pid)
	if err != nil {
		t.Fatalf("GetByProviderID: %v", err)
	}
	byID, err := st.Threads().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byProvider.ID != created.ID || byID.ID != created.ID {
		t.Fatalf("mismatched ids: provider=%s id=%s created=%s", byProvider.ID, byID.ID, created.ID)
	}
	if byID.Participants == nil || byID.LabelIDs == nil {
		t.Fatalf("nil slices must be normalized on read: %+v", byID)
	}
}

func TestThreadRepoUpsertPreservesOpenedAt(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	pid := newID()
	th, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: pid, Subject: "s", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := st.Threads().MarkOpened(ctx, th.ID); err != nil {
		t.Fatalf("MarkOpened: %v", err)
	}
	opened, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if opened.OpenedAt == nil {
		t.Fatal("opened_at not set after MarkOpened")
	}

	// second upsert on same (accountID, providerThreadID) must preserve opened_at
	// in the database. Upsert's RETURNING clause only scans `id` back into the
	// returned struct (unlike calendarRepo.Upsert, which explicitly re-scans
	// color/is_visible), so the persisted value must be observed via GetByID,
	// not trusted from the Upsert return value.
	resynced, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: pid, Subject: "s2", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert resync: %v", err)
	}
	persisted, err := st.Threads().GetByID(ctx, resynced.ID)
	if err != nil {
		t.Fatalf("GetByID after resync: %v", err)
	}
	if persisted.OpenedAt == nil || !persisted.OpenedAt.Equal(*opened.OpenedAt) {
		t.Fatalf("opened_at not preserved on resync: got %v, want %v", persisted.OpenedAt, opened.OpenedAt)
	}
}

func TestThreadRepoMarkOpenedIdempotent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	if err := st.Threads().MarkOpened(ctx, th.ID); err != nil {
		t.Fatalf("MarkOpened 1: %v", err)
	}
	first, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if first.OpenedAt == nil {
		t.Fatal("opened_at nil after first MarkOpened")
	}
	if first.Unread {
		t.Fatal("unread must be false after MarkOpened")
	}

	if err := st.Threads().MarkOpened(ctx, th.ID); err != nil {
		t.Fatalf("MarkOpened 2: %v", err)
	}
	second, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !second.OpenedAt.Equal(*first.OpenedAt) {
		t.Fatalf("opened_at changed on second MarkOpened: %v -> %v", first.OpenedAt, second.OpenedAt)
	}
	if second.Unread {
		t.Fatal("unread must remain false")
	}

	if err := st.Threads().MarkOpened(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MarkOpened unknown: err = %v, want ErrNotFound", err)
	}
}

func TestThreadRepoListDefaultInboxOnly(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	inInbox, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "in", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert inInbox: %v", err)
	}
	if _, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "out", Split: domain.SplitOther,
		InInbox: false, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Upsert outOfInbox: %v", err)
	}

	page, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != inInbox.ID {
		t.Fatalf("default List = %+v, want only the in-inbox thread", page.Items)
	}
}

func TestThreadRepoListViewStarredAndSnoozed(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	starred, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "star", Split: domain.SplitOther,
		InInbox: false, Starred: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert starred: %v", err)
	}
	if _, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "plain", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Upsert plain: %v", err)
	}

	starredPage, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", View: domain.ThreadViewStarred})
	if err != nil {
		t.Fatalf("List starred: %v", err)
	}
	if len(starredPage.Items) != 1 || starredPage.Items[0].ID != starred.ID {
		t.Fatalf("starred view = %+v, want only the starred thread", starredPage.Items)
	}

	future := seedThread(t, st, acct.ID, time.Now())
	future.SnoozedUntil = timePtr2(time.Now().Add(1 * time.Hour))
	if err := st.Threads().Update(ctx, future); err != nil {
		t.Fatalf("Update snooze: %v", err)
	}
	past := seedThread(t, st, acct.ID, time.Now())
	past.SnoozedUntil = timePtr2(time.Now().Add(-1 * time.Hour))
	if err := st.Threads().Update(ctx, past); err != nil {
		t.Fatalf("Update past snooze: %v", err)
	}

	snoozedPage, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", View: domain.ThreadViewSnoozed})
	if err != nil {
		t.Fatalf("List snoozed: %v", err)
	}
	if len(snoozedPage.Items) != 1 || snoozedPage.Items[0].ID != future.ID {
		t.Fatalf("snoozed view = %+v, want only the future-snoozed thread", snoozedPage.Items)
	}
}

func TestThreadRepoListAccountAndSplitFilters(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acctA := seedAccount(t, st, "u1")
	acctB, err := st.Accounts().Create(ctx, domain.ConnectedAccount{
		UserID: "u1", Provider: domain.ProviderMicrosoft, Email: "u1+ms@example.com", Status: domain.AccountActive,
	})
	if err != nil {
		t.Fatalf("Create acctB: %v", err)
	}

	important, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acctA.ID, ProviderThreadID: newID(), Subject: "imp", Split: domain.SplitImportant,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert important: %v", err)
	}
	if _, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acctB.ID, ProviderThreadID: newID(), Subject: "news", Split: domain.SplitNews,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Upsert news: %v", err)
	}

	byAccount, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", AccountID: acctA.ID})
	if err != nil {
		t.Fatalf("List byAccount: %v", err)
	}
	if len(byAccount.Items) != 1 || byAccount.Items[0].ID != important.ID {
		t.Fatalf("List AccountID filter = %+v", byAccount.Items)
	}

	bySplit, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Split: domain.SplitImportant})
	if err != nil {
		t.Fatalf("List bySplit: %v", err)
	}
	if len(bySplit.Items) != 1 || bySplit.Items[0].ID != important.ID {
		t.Fatalf("List Split filter = %+v", bySplit.Items)
	}
}

func TestThreadRepoListQueryFilter(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	th, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "Quarterly report", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	match, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Query: "quarterly"})
	if err != nil {
		t.Fatalf("List Query match: %v", err)
	}
	if len(match.Items) != 1 || match.Items[0].ID != th.ID {
		t.Fatalf("List Query match = %+v", match.Items)
	}

	noMatch, err := st.Threads().List(ctx, port.ThreadQuery{UserID: "u1", Query: "zzz"})
	if err != nil {
		t.Fatalf("List Query no-match: %v", err)
	}
	if len(noMatch.Items) != 0 {
		t.Fatalf("List Query no-match = %+v, want empty", noMatch.Items)
	}
}

func TestThreadRepoListMalformedCursor(t *testing.T) {
	st, _ := newTestStore(t)
	_, err := st.Threads().List(context.Background(), port.ThreadQuery{UserID: "u1", Cursor: "!!not-base64!!"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestThreadRepoSetLabels(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	l1, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: newID(), Name: "Label1"})
	if err != nil {
		t.Fatalf("Upsert l1: %v", err)
	}
	l2, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: newID(), Name: "Label2"})
	if err != nil {
		t.Fatalf("Upsert l2: %v", err)
	}

	if err := st.Threads().SetLabels(ctx, th.ID, []string{l1.ID, l2.ID}); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	got, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.LabelIDs) != 2 {
		t.Fatalf("LabelIDs = %+v, want 2 entries", got.LabelIDs)
	}
	want := map[string]bool{l1.ID: true, l2.ID: true}
	for _, id := range got.LabelIDs {
		if !want[id] {
			t.Fatalf("unexpected label id %q in %+v", id, got.LabelIDs)
		}
	}

	if err := st.Threads().SetLabels(ctx, th.ID, nil); err != nil {
		t.Fatalf("SetLabels nil: %v", err)
	}
	cleared, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID after clear: %v", err)
	}
	if len(cleared.LabelIDs) != 0 {
		t.Fatalf("LabelIDs after clear = %+v, want empty", cleared.LabelIDs)
	}
}

func TestThreadRepoSearch(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	th, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "Quarterly numbers", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	found, err := st.Threads().Search(ctx, "u1", "quarterly", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(found) != 1 || found[0].ID != th.ID {
		t.Fatalf("Search = %+v", found)
	}

	none, err := st.Threads().Search(ctx, "u1", "nonexistentzzz", 0)
	if err != nil {
		t.Fatalf("Search no-match: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("Search no-match = %+v, want empty non-nil slice", none)
	}
}

func TestThreadRepoSnoozeAndReminderDue(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC()

	snoozed := seedThread(t, st, acct.ID, now)
	snoozed.SnoozedUntil = timePtr2(now.Add(-1 * time.Hour))
	if err := st.Threads().Update(ctx, snoozed); err != nil {
		t.Fatalf("Update snoozed: %v", err)
	}
	notDue := seedThread(t, st, acct.ID, now)
	notDue.SnoozedUntil = timePtr2(now.Add(1 * time.Hour))
	if err := st.Threads().Update(ctx, notDue); err != nil {
		t.Fatalf("Update notDue: %v", err)
	}

	due, err := st.Threads().ListSnoozeDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListSnoozeDue: %v", err)
	}
	if len(due) != 1 || due[0].ID != snoozed.ID {
		t.Fatalf("ListSnoozeDue = %+v, want only the due thread", due)
	}

	if err := st.Threads().ClearSnooze(ctx, snoozed.ID); err != nil {
		t.Fatalf("ClearSnooze: %v", err)
	}
	cleared, err := st.Threads().GetByID(ctx, snoozed.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if cleared.SnoozedUntil != nil {
		t.Fatalf("SnoozedUntil after ClearSnooze = %v, want nil", cleared.SnoozedUntil)
	}
	if !cleared.Unread {
		t.Fatal("Unread after ClearSnooze must be true")
	}
	if err := st.Threads().ClearSnooze(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ClearSnooze unknown: err = %v, want ErrNotFound", err)
	}

	// --- reminders ---
	remindDue := seedThread(t, st, acct.ID, now)
	remindDue.RemindAt = timePtr2(now.Add(-1 * time.Hour))
	if err := st.Threads().Update(ctx, remindDue); err != nil {
		t.Fatalf("Update remindDue: %v", err)
	}

	dueReminders, err := st.Threads().ListRemindersDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListRemindersDue: %v", err)
	}
	if len(dueReminders) != 1 || dueReminders[0].ID != remindDue.ID {
		t.Fatalf("ListRemindersDue = %+v", dueReminders)
	}

	if err := st.Threads().ClearReminder(ctx, remindDue.ID); err != nil {
		t.Fatalf("ClearReminder: %v", err)
	}
	clearedReminder, err := st.Threads().GetByID(ctx, remindDue.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if clearedReminder.RemindAt != nil {
		t.Fatalf("RemindAt after ClearReminder = %v, want nil", clearedReminder.RemindAt)
	}
	if !clearedReminder.Unread {
		t.Fatal("Unread after ClearReminder must be true")
	}
	if err := st.Threads().ClearReminder(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ClearReminder unknown: err = %v, want ErrNotFound", err)
	}
}

func TestThreadRepoAppendSentMessage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	th, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: newID(), Subject: "s", Split: domain.SplitOther,
		InInbox: true, MessageCount: 1, LastMessageAt: t0,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	t1 := t0.Add(1 * time.Hour)
	if err := st.Threads().AppendSentMessage(ctx, th.ID, t1); err != nil {
		t.Fatalf("AppendSentMessage: %v", err)
	}
	after1, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after1.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2", after1.MessageCount)
	}
	if !after1.LastMessageAt.Equal(t1) {
		t.Fatalf("LastMessageAt = %v, want %v", after1.LastMessageAt, t1)
	}

	tEarlier := t0.Add(30 * time.Minute)
	if err := st.Threads().AppendSentMessage(ctx, th.ID, tEarlier); err != nil {
		t.Fatalf("AppendSentMessage earlier: %v", err)
	}
	after2, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after2.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", after2.MessageCount)
	}
	if !after2.LastMessageAt.Equal(t1) {
		t.Fatalf("LastMessageAt = %v, want unchanged %v (GREATEST)", after2.LastMessageAt, t1)
	}

	if err := st.Threads().AppendSentMessage(ctx, "nope", t1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("AppendSentMessage unknown: err = %v, want ErrNotFound", err)
	}
}

// timePtr2 is a tiny local helper turning a time.Time into a *time.Time for
// fixture construction (distinct name so it doesn't collide with the
// production timePtr(sql.NullTime) helper in helpers.go).
func timePtr2(t time.Time) *time.Time {
	tt := t.UTC().Truncate(time.Microsecond)
	return &tt
}

func TestThreadRepoSetSummaryAndInstantReplies(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.Threads().SetSummary(ctx, th.ID, "quick recap", at); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	if err := st.Threads().SetInstantReplies(ctx, th.ID, []string{"Sounds good", "Will do"}, at); err != nil {
		t.Fatalf("SetInstantReplies: %v", err)
	}

	got, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Summary != "quick recap" {
		t.Fatalf("Summary = %q, want %q", got.Summary, "quick recap")
	}
	if len(got.InstantReplies) != 2 || got.InstantReplies[0] != "Sounds good" || got.InstantReplies[1] != "Will do" {
		t.Fatalf("InstantReplies = %+v", got.InstantReplies)
	}
	if got.InstantRepliesUpdatedAt == nil || !got.InstantRepliesUpdatedAt.Equal(at) {
		t.Fatalf("InstantRepliesUpdatedAt = %v, want %v", got.InstantRepliesUpdatedAt, at)
	}

	if err := st.Threads().SetSummary(ctx, "nope", "x", at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetSummary unknown: err = %v, want ErrNotFound", err)
	}
	if err := st.Threads().SetInstantReplies(ctx, "nope", nil, at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetInstantReplies unknown: err = %v, want ErrNotFound", err)
	}
}

func TestThreadRepoUpsertPreservesSummaryAndInstantReplies(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	pid := newID()
	th, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: pid, Subject: "s", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.Threads().SetSummary(ctx, th.ID, "ai summary", at); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	if err := st.Threads().SetInstantReplies(ctx, th.ID, []string{"yes"}, at); err != nil {
		t.Fatalf("SetInstantReplies: %v", err)
	}

	// A provider re-sync (new Upsert with a fresh, zero-valued Thread) must
	// not clobber the AI-generated fields, same as opened_at.
	if _, err := st.Threads().Upsert(ctx, domain.Thread{
		AccountID: acct.ID, ProviderThreadID: pid, Subject: "s2", Split: domain.SplitOther,
		InInbox: true, LastMessageAt: time.Now().UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Upsert resync: %v", err)
	}

	got, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Summary != "ai summary" {
		t.Fatalf("Summary after resync = %q, want %q", got.Summary, "ai summary")
	}
	if len(got.InstantReplies) != 1 || got.InstantReplies[0] != "yes" {
		t.Fatalf("InstantReplies after resync = %+v", got.InstantReplies)
	}
}

func TestThreadRepoSetReminderIfUnset(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	th := seedThread(t, st, acct.ID, time.Now())

	first := timePtr2(time.Now().Add(2 * time.Hour))
	if err := st.Threads().SetReminderIfUnset(ctx, th.ID, *first); err != nil {
		t.Fatalf("SetReminderIfUnset 1: %v", err)
	}
	got, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RemindAt == nil || !got.RemindAt.Equal(*first) {
		t.Fatalf("RemindAt = %v, want %v", got.RemindAt, first)
	}

	// Already set: a second call must be a silent no-op, not overwrite the
	// user-chosen reminder and not error.
	second := timePtr2(time.Now().Add(5 * time.Hour))
	if err := st.Threads().SetReminderIfUnset(ctx, th.ID, *second); err != nil {
		t.Fatalf("SetReminderIfUnset 2 (no-op): %v", err)
	}
	got2, err := st.Threads().GetByID(ctx, th.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !got2.RemindAt.Equal(*first) {
		t.Fatalf("RemindAt after no-op = %v, want unchanged %v", got2.RemindAt, first)
	}

	// A missing thread is also a silent no-op.
	if err := st.Threads().SetReminderIfUnset(ctx, "nope", *first); err != nil {
		t.Fatalf("SetReminderIfUnset missing thread: %v", err)
	}
}
