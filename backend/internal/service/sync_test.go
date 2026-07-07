package service

// sync_test.go covers SyncService: SyncAccount (incremental mail + calendar
// provider sync, with split-inbox classification at ingest) and ProcessDueWork
// (delayed-send delivery, snooze wake-ups, follow-up reminders).
//
// Known fake limitations (cannot be worked around without modifying the
// shared fakes in fakes_test.go, which Tasks 1-7 depend on):
//   - fakeThreadRepo.ClearSnooze/ClearReminder always return nil; there is no
//     field to program a failure, so "ClearSnooze error is collected but
//     non-fatal" cannot be exercised. Omitted.
//   - fakeMailProvider never records the access token it was called with, so
//     "the refreshed token is the one used for sync/send" is not directly
//     observable; the refresh tests instead assert on the OAuth gateway call
//     and the persisted token (the observable side effects of a refresh).
//
// Two limitations noted in earlier revisions of this file -- fakeMailProvider
// only ever serving a single fixed page, and fakeEventRepo.DeleteByProviderID
// never returning domain.ErrNotFound -- were lifted additively (syncPages /
// deleteByProviderErr in fakes_test.go); see TestSyncAccountDrainsMultiplePages
// and TestSyncAccountDeletedIDTolerantOfAbsence below.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// =============================================================================
// SyncService.SyncAccount
// =============================================================================

// TestSyncAccountHappyPath runs one incremental mail sync (from the stored
// cursor, threads + messages upserted with split classification at ingest,
// NextCursor saved) and one calendar sync (calendars + events upserted,
// DeletedIDs removed), then stamps the account active.
func TestSyncAccountHappyPath(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	clock := newClock(now)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle,
		Email: "me@acme.com", Status: domain.AccountSyncing,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// One mail page: a label, a brand-new thread, and its newest message
	// (direct personal mail => classified important). ThreadID on the message
	// carries the PROVIDER thread id at this boundary.
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Labels:  []domain.Label{{ProviderLabelID: "L1", Name: "Work"}},
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Lunch?", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm1", ThreadID: "pt1",
				From:   domain.EmailAddress{Email: "friend@example.org"},
				To:     []domain.EmailAddress{{Email: "me@acme.com"}},
				SentAt: now,
			},
		}},
		NextCursor: "mail-cursor-2",
		HasMore:    false,
	}

	// One calendar with one upserted event and one upstream deletion.
	cal := newCalendarProvider()
	cal.calendars = []domain.Calendar{{ProviderCalendarID: "pc1", Name: "Personal"}}
	cal.syncPage = port.CalendarSyncPage{
		Events:     []domain.Event{{ProviderEventID: "pe1", Title: "Standup", Start: now, End: now.Add(time.Hour)}},
		DeletedIDs: []string{"pe-gone"},
		NextCursor: "events-cursor-2",
		HasMore:    false,
	}

	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	syncState := newSyncStateRepo()
	// Pre-seed the calendar (matched on account+providerCalendarID so the local
	// id survives) and the event the page deletes, so removal is observable.
	if _, err := calendars.Upsert(ctx, domain.Calendar{ID: "c1", AccountID: "a1", ProviderCalendarID: "pc1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Upsert(ctx, domain.Event{ID: "old", CalendarID: "c1", ProviderEventID: "pe-gone"}); err != nil {
		t.Fatal(err)
	}

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages,
		Calendars: calendars, Events: events, SyncState: syncState,
		MailProviders:     map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: cal},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             clock,
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}

	// Thread ingested, classified important, with a local id.
	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("thread not upserted: %v", err)
	}
	if th.ID == "" {
		t.Fatal("thread stored without a local id")
	}
	if th.Split != domain.SplitImportant {
		t.Fatalf("split = %q, want %q (direct personal mail)", th.Split, domain.SplitImportant)
	}
	// Message mirrored onto the LOCAL thread id.
	msgs, err := messages.ListByThread(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages on thread = %d, want 1", len(msgs))
	}
	// Exactly one calendar (matched, not duplicated); resolve its local id.
	cals, err := calendars.ListByAccount(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 {
		t.Fatalf("calendars = %d, want 1 (matched on providerCalendarID)", len(cals))
	}
	calID := cals[0].ID
	// Event upserted; deleted id removed.
	if _, err := events.GetByProviderID(ctx, calID, "pe1"); err != nil {
		t.Fatalf("synced event missing: %v", err)
	}
	if _, err := events.GetByProviderID(ctx, calID, "pe-gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted event still present, err = %v", err)
	}
	// Cursors persisted for both resources.
	if st, _ := syncState.Get(ctx, "a1", "mail"); st.Cursor != "mail-cursor-2" {
		t.Fatalf("mail cursor = %q, want mail-cursor-2", st.Cursor)
	}
	if st, _ := syncState.Get(ctx, "a1", "events:pc1"); st.Cursor != "events-cursor-2" {
		t.Fatalf("events cursor = %q, want events-cursor-2", st.Cursor)
	}
	// Account finalized: active + LastSyncedAt stamped from the clock.
	fin, _ := accounts.GetByID(ctx, "a1")
	if fin.Status != domain.AccountActive {
		t.Fatalf("status = %q, want %q", fin.Status, domain.AccountActive)
	}
	if fin.LastSyncedAt == nil || !fin.LastSyncedAt.Equal(now) {
		t.Fatalf("LastSyncedAt = %v, want %v", fin.LastSyncedAt, now)
	}
}

// TestSyncAccountUnknownAccount pins the unwrapped ErrNotFound propagated from
// accounts.GetByID for an account that was never seeded.
func TestSyncAccountUnknownAccount(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	mail := newMailProvider()

	svc := NewSyncService(SyncServiceDeps{
		Accounts:      newAccountRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "ghost"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SyncAccount(ghost) err = %v, want ErrNotFound", err)
	}
}

// TestSyncAccountRefreshesExpiredToken exercises the tokenSource refresh path
// through SyncAccount: an expired token is refreshed, re-persisted, and the
// account still finalizes active.
func TestSyncAccountRefreshesExpiredToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "old", RefreshToken: "rt", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	oauth := newOAuthGateway()
	oauth.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "new", ExpiresAt: now.Add(time.Hour)}}

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts,
		OAuth:    map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: oauth},
		Clock:    newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	if oauth.refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", oauth.refreshCalls)
	}
	if oauth.lastRefreshToken != "rt" {
		t.Fatalf("lastRefreshToken = %q, want rt", oauth.lastRefreshToken)
	}
	tok, err := accounts.GetTokens(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "new" {
		t.Fatalf("persisted AccessToken = %q, want new", tok.AccessToken)
	}
	fin, _ := accounts.GetByID(ctx, "a1")
	if fin.Status != domain.AccountActive {
		t.Fatalf("status = %q, want active", fin.Status)
	}
}

// TestSyncAccountRefreshFailureLeavesReauthRequired: a failed refresh flags
// the account reauth_required and SyncAccount surfaces ErrUnauthorized
// without ever reaching the active finalization.
func TestSyncAccountRefreshFailureLeavesReauthRequired(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Status: domain.AccountActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "old", RefreshToken: "rt", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	oauth := newOAuthGateway()
	oauth.refreshErr = errors.New("boom")

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts,
		OAuth:    map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: oauth},
		Clock:    newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("SyncAccount err = %v, want ErrUnauthorized", err)
	}
	fin, err := accounts.GetByID(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if fin.Status != domain.AccountReauthRequired {
		t.Fatalf("status = %q, want reauth_required (never reached the active finalize)", fin.Status)
	}
}

// TestSyncAccountRefreshKeepsOldRefreshTokenWhenEmpty: a refresh response that
// omits RefreshToken must not blank out the previously stored one.
func TestSyncAccountRefreshKeepsOldRefreshTokenWhenEmpty(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "old", RefreshToken: "rt", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	oauth := newOAuthGateway()
	oauth.refreshToken = port.OAuthToken{TokenSet: port.TokenSet{AccessToken: "new", RefreshToken: "", ExpiresAt: now.Add(time.Hour)}}

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts,
		OAuth:    map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: oauth},
		Clock:    newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	tok, err := accounts.GetTokens(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.RefreshToken != "rt" {
		t.Fatalf("RefreshToken = %q, want rt (kept)", tok.RefreshToken)
	}
}

// TestSyncAccountNoMailProviderConfigured: syncMail no-ops when the account's
// provider has no entry in MailProviders; calendar sync still runs and the
// mail cursor is never saved.
func TestSyncAccountNoMailProviderConfigured(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cal := newCalendarProvider()
	cal.calendars = []domain.Calendar{{ProviderCalendarID: "pc1", Name: "Personal"}}
	threads := newThreadRepo()
	messages := newMessageRepo()
	calendars := newCalendarRepo()
	events := newEventRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Messages: messages,
		Calendars: calendars, Events: events, SyncState: syncState,
		CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: cal},
		OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:             newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	cals, err := calendars.ListByAccount(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 {
		t.Fatalf("calendars = %d, want 1 (calendar sync still ran)", len(cals))
	}
	if len(messages.order) != 0 {
		t.Fatalf("messages written = %d, want 0 (no mail provider configured)", len(messages.order))
	}
	if _, err := syncState.Get(ctx, "a1", "mail"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("mail cursor should never be saved, err = %v", err)
	}
	fin, _ := accounts.GetByID(ctx, "a1")
	if fin.Status != domain.AccountActive {
		t.Fatalf("status = %q, want active", fin.Status)
	}
}

// TestSyncAccountNoCalendarProviderConfigured: syncCalendars no-ops when the
// account's provider has no entry in CalendarProviders; mail sync still runs.
func TestSyncAccountNoCalendarProviderConfigured(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
	}
	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	if _, err := threads.GetByProviderID(ctx, "a1", "pt1"); err != nil {
		t.Fatalf("mail sync should still run: %v", err)
	}
	fin, _ := accounts.GetByID(ctx, "a1")
	if fin.Status != domain.AccountActive {
		t.Fatalf("status = %q, want active", fin.Status)
	}
}

// TestSyncAccountRemapsLabelsOntoLocalIDs: a thread's provider label ids are
// replaced by local label ids before persisting, and the mapping is also
// pushed through SetLabels.
func TestSyncAccountRemapsLabelsOntoLocalIDs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Labels:  []domain.Label{{ProviderLabelID: "L1", Name: "Work"}},
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now, LabelIDs: []string{"L1"}}},
	}
	threads := newThreadRepo()
	labels := newLabelRepo()
	messages := newMessageRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	all, err := labels.ListByAccount(ctx, "a1")
	if err != nil || len(all) != 1 {
		t.Fatalf("labels = %+v, err %v, want exactly one", all, err)
	}
	localLabelID := all[0].ID

	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatal(err)
	}
	if len(th.LabelIDs) != 1 || th.LabelIDs[0] != localLabelID {
		t.Fatalf("thread LabelIDs = %v, want [%s] (local id)", th.LabelIDs, localLabelID)
	}
	if len(threads.lastSetLabelIDs) != 1 || threads.lastSetLabelIDs[0] != localLabelID {
		t.Fatalf("SetLabels persisted %v, want [%s]", threads.lastSetLabelIDs, localLabelID)
	}
}

// TestSyncAccountExistingThreadPreservesLocalState: re-syncing a known thread
// keeps its local-only read/snooze/reminder state when the page carries no
// new inbound reply (the message here is from the owner). The message's own
// classification also resolves to "team" (its sender shares the account's
// non-freemail domain), so this also pins that the value survives the pass
// without depending on which of "preserved" vs "recomputed-to-the-same-value"
// code path runs.
func TestSyncAccountExistingThreadPreservesLocalState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", ProviderThreadID: "pt1",
		OpenedAt: &past, SnoozedUntil: &future, RemindAt: &future,
		Split: domain.SplitTeam, LastMessageAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{Message: domain.Message{
			ProviderMessageID: "pm1", ThreadID: "pt1",
			From: domain.EmailAddress{Email: "me@acme.com"}, SentAt: now,
		}}},
	}
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	th, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if th.OpenedAt == nil {
		t.Fatal("OpenedAt cleared, want preserved (local-only read state)")
	}
	if th.SnoozedUntil == nil || !th.SnoozedUntil.Equal(future) {
		t.Fatalf("SnoozedUntil = %v, want %v (no inbound reply, not resurfaced)", th.SnoozedUntil, future)
	}
	if th.RemindAt == nil || !th.RemindAt.Equal(future) {
		t.Fatalf("RemindAt = %v, want %v (unchanged)", th.RemindAt, future)
	}
	if th.Split != domain.SplitTeam {
		t.Fatalf("Split = %q, want team", th.Split)
	}
}

// TestSyncAccountInboundReplyResurfacesThread: a fresh inbound reply
// auto-unsnoozes the thread and cancels a pending follow-up reminder.
func TestSyncAccountInboundReplyResurfacesThread(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	older := now.Add(-time.Hour)
	future := now.Add(24 * time.Hour)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{
		ID: "t1", AccountID: "a1", ProviderThreadID: "pt1",
		SnoozedUntil: &future, RemindAt: &future, LastMessageAt: older,
	}); err != nil {
		t.Fatal(err)
	}

	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{Message: domain.Message{
			ProviderMessageID: "pm1", ThreadID: "pt1",
			From: domain.EmailAddress{Email: "stranger@x.com"}, SentAt: now,
		}}},
	}
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	th, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if th.SnoozedUntil != nil {
		t.Fatalf("SnoozedUntil = %v, want nil (auto-unsnoozed on reply)", th.SnoozedUntil)
	}
	if th.RemindAt != nil {
		t.Fatalf("RemindAt = %v, want nil (reminder cancelled on reply)", th.RemindAt)
	}
}

// TestSyncAccountPushOnNewImportantMail: a brand-new inbox thread classified
// important with a genuinely new inbound message triggers a push per device.
func TestSyncAccountPushOnNewImportantMail(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	devices := newDeviceRepo()
	if _, err := devices.Upsert(ctx, domain.NotificationDevice{ID: "dev1", UserID: "u1", Platform: domain.PlatformIOS, Token: "tok"}); err != nil {
		t.Fatal(err)
	}

	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Lunch?", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{Message: domain.Message{
			ProviderMessageID: "pm1", ThreadID: "pt1",
			From:   domain.EmailAddress{Email: "friend@example.org"},
			To:     []domain.EmailAddress{{Email: "me@acme.com"}},
			SentAt: now,
		}}},
	}
	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, Devices: devices, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Push:          push,
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatal(err)
	}
	if len(push.sent) != 1 {
		t.Fatalf("push sends = %d, want 1", len(push.sent))
	}
	got := push.sent[0]
	if got.Title != "New email" {
		t.Fatalf("title = %q, want %q", got.Title, "New email")
	}
	if got.Data["threadId"] != th.ID {
		t.Fatalf("threadId = %q, want %q", got.Data["threadId"], th.ID)
	}
}

// TestSyncAccountPushVIPTitle: mail from a configured VIP sender is
// classified vip and pushes with the VIP-specific title.
func TestSyncAccountPushVIPTitle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
		VIPSenders: []string{"boss@corp.com"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	devices := newDeviceRepo()
	if _, err := devices.Upsert(ctx, domain.NotificationDevice{ID: "dev1", UserID: "u1", Platform: domain.PlatformIOS, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{Message: domain.Message{
			ProviderMessageID: "pm1", ThreadID: "pt1",
			From: domain.EmailAddress{Email: "boss@corp.com"}, SentAt: now,
		}}},
	}
	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, Devices: devices, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Push:          push,
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	if len(push.sent) != 1 || push.sent[0].Title != "New VIP email" {
		t.Fatalf("push sent = %+v, want one 'New VIP email'", push.sent)
	}
}

// TestSyncAccountNoPushOnNonImportantSplit: bulk mail (List-Unsubscribe) is
// classified news, which never pushes even though push is wired.
func TestSyncAccountNoPushOnNonImportantSplit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	devices := newDeviceRepo()
	if _, err := devices.Upsert(ctx, domain.NotificationDevice{ID: "dev1", UserID: "u1", Platform: domain.PlatformIOS, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm1", ThreadID: "pt1",
				From: domain.EmailAddress{Email: "newsletter@news.example"}, SentAt: now,
			},
			Headers: map[string]string{"List-Unsubscribe": "<mailto:x@news.example>"},
		}},
	}
	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, Devices: devices, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Push:          push,
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatal(err)
	}
	if th.Split != domain.SplitNews {
		t.Fatalf("split = %q, want news", th.Split)
	}
	if len(push.sent) != 0 {
		t.Fatalf("push sends = %d, want 0 (non-important split)", len(push.sent))
	}
}

// TestSyncAccountOrphanMessageSkipped: a message whose provider thread id is
// carried by neither this page nor the repo is silently skipped.
func TestSyncAccountOrphanMessageSkipped(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", InInbox: true, LastMessageAt: now}},
		Messages: []port.IncomingMessage{
			{Message: domain.Message{ProviderMessageID: "pm1", ThreadID: "pt1", From: domain.EmailAddress{Email: "friend@example.org"}, SentAt: now}},
			{Message: domain.Message{ProviderMessageID: "pm-orphan", ThreadID: "pt-unknown", From: domain.EmailAddress{Email: "friend@example.org"}, SentAt: now}},
		},
	}
	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}
	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := messages.ListByThread(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages on known thread = %d, want 1 (orphan excluded)", len(msgs))
	}
	if len(messages.order) != 1 {
		t.Fatalf("total persisted messages = %d, want 1 (orphan silently skipped)", len(messages.order))
	}
}

// TestSyncAccountDeletedIDTolerantOfAbsence exercises the errors.Is swallow
// branch in syncEvents (sync.go: `if err != nil && !errors.Is(err,
// domain.ErrNotFound) { return err }`): a DeleteByProviderID failure of
// exactly domain.ErrNotFound is swallowed and the sync still succeeds, while
// any other DeleteByProviderID error still fails the sync.
func TestSyncAccountDeletedIDTolerantOfAbsence(t *testing.T) {
	newSvc := func(events *fakeEventRepo) *SyncService {
		now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
		accounts := newAccountRepo()
		if _, err := accounts.Create(context.Background(), domain.ConnectedAccount{
			ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
		}); err != nil {
			t.Fatal(err)
		}
		if err := accounts.SaveTokens(context.Background(), "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		cal := newCalendarProvider()
		cal.calendars = []domain.Calendar{{ProviderCalendarID: "pc1", Name: "Personal"}}
		cal.syncPage = port.CalendarSyncPage{DeletedIDs: []string{"never-existed"}}

		return NewSyncService(SyncServiceDeps{
			Accounts: accounts, Calendars: newCalendarRepo(), Events: events, SyncState: newSyncStateRepo(),
			CalendarProviders: map[domain.Provider]port.CalendarProvider{domain.ProviderGoogle: cal},
			OAuth:             map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
			Clock:             newClock(now),
		})
	}

	t.Run("ErrNotFound is swallowed", func(t *testing.T) {
		events := newEventRepo()
		events.deleteByProviderErr = domain.ErrNotFound
		svc := newSvc(events)
		if err := svc.SyncAccount(context.Background(), "a1"); err != nil {
			t.Fatalf("SyncAccount: %v, want nil (ErrNotFound swallowed)", err)
		}
	})

	t.Run("other error propagates", func(t *testing.T) {
		boom := errors.New("boom")
		events := newEventRepo()
		events.deleteByProviderErr = boom
		svc := newSvc(events)
		if err := svc.SyncAccount(context.Background(), "a1"); !errors.Is(err, boom) {
			t.Fatalf("SyncAccount err = %v, want wrapping %v", err, boom)
		}
	})
}

// TestSyncAccountDrainsMultiplePages drains a mail sync loop where every page
// but the last sets HasMore=true, asserting the loop threads NextCursor into
// the next SyncMail call, accumulates every page's threads/messages, and
// persists only the FINAL page's NextCursor.
func TestSyncAccountDrainsMultiplePages(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	page := func(providerThreadID, providerMessageID, nextCursor string, hasMore bool) port.MailSyncPage {
		return port.MailSyncPage{
			Threads: []domain.Thread{{ProviderThreadID: providerThreadID, Subject: providerThreadID, InInbox: true, LastMessageAt: now}},
			Messages: []port.IncomingMessage{{Message: domain.Message{
				ProviderMessageID: providerMessageID, ThreadID: providerThreadID,
				From: domain.EmailAddress{Email: "friend@example.org"}, SentAt: now,
			}}},
			NextCursor: nextCursor,
			HasMore:    hasMore,
		}
	}

	mail := newMailProvider()
	mail.syncPages = []port.MailSyncPage{
		page("pt1", "pm1", "c1", true),
		page("pt2", "pm2", "c2", true),
		page("pt3", "pm3", "c3", false),
	}

	threads := newThreadRepo()
	messages := newMessageRepo()
	labels := newLabelRepo()
	syncState := newSyncStateRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: labels, Threads: threads, Messages: messages, SyncState: syncState,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("SyncAccount: %v", err)
	}

	// SyncMail called exactly 3 times, threading each page's NextCursor into
	// the next call (starting from the empty initial cursor).
	wantCursors := []string{"", "c1", "c2"}
	if len(mail.syncMailCursors) != len(wantCursors) {
		t.Fatalf("SyncMail calls = %d, want %d (cursors %v)", len(mail.syncMailCursors), len(wantCursors), mail.syncMailCursors)
	}
	for i, want := range wantCursors {
		if mail.syncMailCursors[i] != want {
			t.Fatalf("SyncMail call %d cursor = %q, want %q", i, mail.syncMailCursors[i], want)
		}
	}

	// Every page's thread and message were upserted.
	for _, pt := range []string{"pt1", "pt2", "pt3"} {
		th, err := threads.GetByProviderID(ctx, "a1", pt)
		if err != nil {
			t.Fatalf("thread %s not upserted: %v", pt, err)
		}
		msgs, err := messages.ListByThread(ctx, th.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("messages on thread %s = %d, want 1", pt, len(msgs))
		}
	}

	// The FINAL page's NextCursor is what gets persisted, not an intermediate one.
	if st, err := syncState.Get(ctx, "a1", "mail"); err != nil || st.Cursor != "c3" {
		t.Fatalf("mail cursor = %q (err %v), want c3", st.Cursor, err)
	}
}

// TestSyncAccountMailErrorAbortsAndWraps: a provider SyncMail error aborts
// the pass, wraps with the account id, and leaves the account un-finalized.
func TestSyncAccountMailErrorAbortsAndWraps(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com", Status: domain.AccountSyncing,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "valid", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	mail := newMailProvider()
	mail.syncErr = errors.New("gmail 500")

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts,
		// syncMail reads the stored cursor via SyncState before ever calling
		// the provider, so this must be wired even though the provider call
		// itself is what errors.
		SyncState:     newSyncStateRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	err := svc.SyncAccount(ctx, "a1")
	if err == nil || !strings.Contains(err.Error(), "sync mail for account a1") {
		t.Fatalf("err = %v, want wrapping %q", err, "sync mail for account a1")
	}
	fin, _ := accounts.GetByID(ctx, "a1")
	if fin.Status != domain.AccountSyncing {
		t.Fatalf("status = %q, want still syncing (not finalized)", fin.Status)
	}
}

// =============================================================================
// SyncService.ProcessDueWork
// =============================================================================

// A claimed, due, threaded draft is delivered through the provider exactly
// once, mirrored onto its local thread, and then deleted.
func TestProcessDueWorkDeliversClaimedDraft(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	clock := newClock(now)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	// Non-expired token so tokenSource returns it without a refresh.
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	threads := newThreadRepo()
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1", ProviderThreadID: "pt1"}); err != nil {
		t.Fatal(err)
	}

	drafts := newDraftRepo(accounts)
	past := now.Add(-time.Minute) // due
	tid := "t1"
	draft := domain.Draft{
		ID: "d1", AccountID: "a1", ThreadID: &tid,
		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
		Subject: "Lunch?", BodyHTML: "<p>hi</p>", ScheduledAt: &past,
	}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.claimOutcome["d1"] = true
	// ListScheduledDue is fully programmable on the real fake; Create alone
	// does not enroll the draft into it.
	drafts.scheduledDue = []domain.Draft{draft}

	mail := newMailProvider()
	mail.sentResult = port.SentMessage{ProviderMessageID: "pm1", ProviderThreadID: "pt1", SentAt: now}
	messages := newMessageRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Messages: messages, Drafts: drafts,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         clock,
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}

	// Claimed => delivered exactly once, threaded as a reply.
	if len(mail.sent) != 1 {
		t.Fatalf("Send calls = %d, want 1", len(mail.sent))
	}
	if mail.sent[0].ProviderThreadID != "pt1" {
		t.Fatalf("reply ProviderThreadID = %q, want pt1", mail.sent[0].ProviderThreadID)
	}
	// Sent message mirrored onto the local thread (non-draft).
	msgs, err := messages.ListByThread(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].IsDraft {
		t.Fatalf("thread messages = %+v, want one non-draft", msgs)
	}
	// AppendSentMessage bumped the thread counters.
	th, err := threads.GetByID(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if th.MessageCount != 1 || !th.LastMessageAt.Equal(now) {
		t.Fatalf("thread after append = {count:%d last:%v}, want {1 %v}", th.MessageCount, th.LastMessageAt, now)
	}
	// Draft consumed after delivery.
	if _, err := drafts.GetByID(ctx, "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("draft after send err = %v, want ErrNotFound (deleted)", err)
	}
}

// Losing the claim race (ClaimScheduled=false) must not re-send the draft to
// recipients and must leave the draft untouched.
func TestProcessDueWorkSkipsUnclaimedDraft(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	clock := newClock(now)

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	drafts := newDraftRepo(accounts)
	past := now.Add(-time.Minute)
	draft := domain.Draft{
		ID: "d1", AccountID: "a1",
		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
		Subject: "hi", ScheduledAt: &past,
	}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.claimOutcome["d1"] = false // already claimed by a concurrent worker
	drafts.scheduledDue = []domain.Draft{draft}

	mail := newMailProvider()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Drafts: drafts,
		// ProcessDueWork unconditionally also runs the snooze/reminder passes,
		// which touch ThreadRepo -- must be wired even though this test's
		// focus is the draft claim.
		Threads:       newThreadRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         clock,
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("Send calls = %d, want 0 (no double-send after lost claim)", len(mail.sent))
	}
	if _, err := drafts.GetByID(ctx, "d1"); err != nil {
		t.Fatalf("unclaimed draft must survive, got err %v", err)
	}
}

// TestProcessDueWorkStandaloneDraftNoThreadMirror: a claimed standalone draft
// (ThreadID nil) is delivered and deleted, but no message/thread bump happens
// (guarded by msg.ThreadID != "").
func TestProcessDueWorkStandaloneDraftNoThreadMirror(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	drafts := newDraftRepo(accounts)
	past := now.Add(-time.Minute)
	draft := domain.Draft{
		ID: "d1", AccountID: "a1",
		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
		Subject: "hi", ScheduledAt: &past,
	}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.claimOutcome["d1"] = true
	drafts.scheduledDue = []domain.Draft{draft}

	mail := newMailProvider()
	mail.sentResult = port.SentMessage{ProviderMessageID: "pm1", SentAt: now}
	messages := newMessageRepo()
	threads := newThreadRepo()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Messages: messages, Drafts: drafts,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("Send calls = %d, want 1", len(mail.sent))
	}
	if len(messages.order) != 0 {
		t.Fatalf("messages persisted = %d, want 0 (standalone draft has no thread)", len(messages.order))
	}
	if threads.appendSentCalls != 0 {
		t.Fatalf("AppendSentMessage calls = %d, want 0", threads.appendSentCalls)
	}
	if _, err := drafts.GetByID(ctx, "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("draft after send err = %v, want ErrNotFound", err)
	}
}

// TestProcessDueWorkSendErrorRearmsForRetry: a provider Send failure re-arms
// the draft with exponential backoff and reports a non-nil error without
// deleting the draft.
func TestProcessDueWorkSendErrorRearmsForRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	drafts := newDraftRepo(accounts)
	past := now.Add(-time.Minute)
	draft := domain.Draft{
		ID: "d1", AccountID: "a1", SendAttempts: 0,
		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
		Subject: "hi", ScheduledAt: &past,
	}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.claimOutcome["d1"] = true
	drafts.scheduledDue = []domain.Draft{draft}

	mail := newMailProvider()
	mail.sendErr = errors.New("smtp 421")

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Drafts: drafts, Threads: newThreadRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	err := svc.ProcessDueWork(ctx)
	if err == nil {
		t.Fatal("ProcessDueWork err = nil, want non-nil (joined send failure)")
	}
	if drafts.failureCalls != 1 {
		t.Fatalf("RecordSendFailure calls = %d, want 1", drafts.failureCalls)
	}
	if drafts.lastFailureID != "d1" {
		t.Fatalf("lastFailureID = %q, want d1", drafts.lastFailureID)
	}
	wantNext := now.Add(30 * time.Second)
	if drafts.lastNextAttempt == nil || !drafts.lastNextAttempt.Equal(wantNext) {
		t.Fatalf("lastNextAttempt = %v, want %v", drafts.lastNextAttempt, wantNext)
	}
	if !strings.Contains(drafts.lastFailureErr, "smtp 421") {
		t.Fatalf("lastFailureErr = %q, want it to contain smtp 421", drafts.lastFailureErr)
	}
	if _, err := drafts.GetByID(ctx, "d1"); err != nil {
		t.Fatalf("draft must survive a retryable failure, got err %v", err)
	}
}

// TestProcessDueWorkDeadLettersAfterAttemptCap: once attempts reach the cap
// the draft is dead-lettered (scheduled_at left clear) instead of re-armed.
func TestProcessDueWorkDeadLettersAfterAttemptCap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle, Email: "me@acme.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{AccessToken: "at", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	drafts := newDraftRepo(accounts)
	past := now.Add(-time.Minute)
	draft := domain.Draft{
		ID: "d1", AccountID: "a1", SendAttempts: maxSendAttempts - 1,
		To:      []domain.EmailAddress{{Email: "friend@example.org"}},
		Subject: "hi", ScheduledAt: &past,
	}
	if _, err := drafts.Create(ctx, draft); err != nil {
		t.Fatal(err)
	}
	drafts.claimOutcome["d1"] = true
	drafts.scheduledDue = []domain.Draft{draft}

	mail := newMailProvider()
	mail.sendErr = errors.New("smtp 550")

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Drafts: drafts, Threads: newThreadRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err == nil {
		t.Fatal("ProcessDueWork err = nil, want non-nil (dead-letter reported)")
	}
	if drafts.lastNextAttempt != nil {
		t.Fatalf("lastNextAttempt = %v, want nil (dead-lettered, no retry scheduled)", drafts.lastNextAttempt)
	}
	if _, err := drafts.GetByID(ctx, "d1"); err != nil {
		t.Fatalf("dead-lettered draft must survive (not deleted), got err %v", err)
	}
}

// TestProcessDueWorkWakesSnoozedThreads: a due snooze is cleared and pushes
// with the snooze-specific title.
func TestProcessDueWorkWakesSnoozedThreads(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	devices := newDeviceRepo()
	if _, err := devices.Upsert(ctx, domain.NotificationDevice{ID: "dev1", UserID: "u1", Platform: domain.PlatformIOS, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	threads.snoozeDue = []domain.Thread{{ID: "t1", AccountID: "a1", Subject: "Trip planning"}}
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Devices: devices, Push: push,
		Drafts: newDraftRepo(accounts),
		Clock:  newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	found := false
	for _, id := range threads.clearedSnooze {
		if id == "t1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clearedSnooze = %v, want it to contain t1", threads.clearedSnooze)
	}
	if len(push.sent) != 1 || push.sent[0].Title != "Snoozed conversation is back" {
		t.Fatalf("push sent = %+v, want one 'Snoozed conversation is back'", push.sent)
	}
}

// TestProcessDueWorkFiresReminders: a due follow-up reminder is cleared and
// pushes with the thread's subject as the body.
func TestProcessDueWorkFiresReminders(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	devices := newDeviceRepo()
	if _, err := devices.Upsert(ctx, domain.NotificationDevice{ID: "dev1", UserID: "u1", Platform: domain.PlatformIOS, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	threads.remindersDue = []domain.Thread{{ID: "t2", AccountID: "a1", Subject: "ping"}}
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Devices: devices, Push: push,
		Drafts: newDraftRepo(accounts),
		Clock:  newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	found := false
	for _, id := range threads.clearedReminder {
		if id == "t2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clearedReminder = %v, want it to contain t2", threads.clearedReminder)
	}
	if len(push.sent) != 1 {
		t.Fatalf("push sends = %d, want 1", len(push.sent))
	}
	if push.sent[0].Title != "Follow-up reminder" || push.sent[0].Body != "ping" {
		t.Fatalf("push = %+v, want title 'Follow-up reminder' body 'ping'", push.sent[0])
	}
}

// TestProcessDueWorkPushNilSafe: with Push nil, ClearSnooze/ClearReminder
// still run (notifyThread just no-ops) and no panic occurs.
func TestProcessDueWorkPushNilSafe(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	threads.snoozeDue = []domain.Thread{{ID: "t1", AccountID: "a1"}}
	threads.remindersDue = []domain.Thread{{ID: "t2", AccountID: "a1"}}

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads,
		Drafts: newDraftRepo(accounts),
		// Push intentionally omitted (nil): notifyThread must no-op safely.
		Clock: newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(threads.clearedSnooze) != 1 || threads.clearedSnooze[0] != "t1" {
		t.Fatalf("clearedSnooze = %v, want [t1]", threads.clearedSnooze)
	}
	if len(threads.clearedReminder) != 1 || threads.clearedReminder[0] != "t2" {
		t.Fatalf("clearedReminder = %v, want [t2]", threads.clearedReminder)
	}
}

// TestProcessDueWorkNothingDue: an empty pass across all three queues is a
// clean, no-op success.
func TestProcessDueWorkNothingDue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	accounts := newAccountRepo()
	threads := newThreadRepo()
	drafts := newDraftRepo(accounts)
	mail := newMailProvider()
	push := newPush()

	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Threads: threads, Drafts: drafts,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		Push:          push,
		Clock:         newClock(now),
	})

	if err := svc.ProcessDueWork(ctx); err != nil {
		t.Fatalf("ProcessDueWork: %v", err)
	}
	if len(mail.sent) != 0 {
		t.Fatalf("Send calls = %d, want 0", len(mail.sent))
	}
	if len(push.sent) != 0 {
		t.Fatalf("push sends = %d, want 0", len(push.sent))
	}
}
