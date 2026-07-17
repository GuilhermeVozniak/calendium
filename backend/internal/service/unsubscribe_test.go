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

func TestSyncPreservesUnsubscribeAcrossHeaderlessDeltas(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Minute)  // newer header-less reply
	t2 := t0.Add(20 * time.Minute)  // even newer with different headers

	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Provider: domain.ProviderGoogle,
		Email: "owner@acme.com", Status: domain.AccountActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveTokens(ctx, "a1", port.TokenSet{
		AccessToken: "tok", RefreshToken: "r", ExpiresAt: t2.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	threads := newThreadRepo()
	mail := newMailProvider()
	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: newLabelRepo(), Threads: threads,
		Messages: newMessageRepo(), SyncState: newSyncStateRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(t2),
	})

	// Sync 1: message with List-Unsubscribe headers stamps the thread.
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Discussion", LastMessageAt: t0, InInbox: true}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm1", ThreadID: "pt1",
				From:   domain.EmailAddress{Email: "digest@news.example"},
				To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
				SentAt: t0,
			},
			Headers: map[string]string{
				"List-Unsubscribe":      "<mailto:unsub@news.example>, <https://news.example/u?id=1>",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			},
		}},
	}
	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}

	th, err := threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("GetByProviderID after sync 1: %v", err)
	}
	if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:unsub@news.example" {
		t.Fatalf("sync 1: UnsubscribeMailto = %v, want mailto:unsub@news.example", th.UnsubscribeMailto)
	}
	if th.UnsubscribeURL == nil || *th.UnsubscribeURL != "https://news.example/u?id=1" {
		t.Fatalf("sync 1: UnsubscribeURL = %v, want https://news.example/u?id=1", th.UnsubscribeURL)
	}
	if !th.UnsubscribeOneClick {
		t.Fatal("sync 1: UnsubscribeOneClick = false, want true")
	}

	// Sync 2: newer header-less reply on the same thread.
	// The update path runs; previously stamped values must survive.
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Discussion", LastMessageAt: t1, InInbox: true}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm2", ThreadID: "pt1",
				From:   domain.EmailAddress{Email: "owner@acme.com"},
				To:     []domain.EmailAddress{{Email: "digest@news.example"}},
				SentAt: t1,
			},
			Headers: map[string]string{}, // no List-Unsubscribe header
		}},
	}
	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}

	th, err = threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("GetByProviderID after sync 2: %v", err)
	}
	if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:unsub@news.example" {
		t.Fatalf("sync 2: UnsubscribeMailto = %v, want mailto:unsub@news.example (preserved)", th.UnsubscribeMailto)
	}
	if th.UnsubscribeURL == nil || *th.UnsubscribeURL != "https://news.example/u?id=1" {
		t.Fatalf("sync 2: UnsubscribeURL = %v, want https://news.example/u?id=1 (preserved)", th.UnsubscribeURL)
	}
	if !th.UnsubscribeOneClick {
		t.Fatal("sync 2: UnsubscribeOneClick = false, want true (preserved)")
	}

	// Sync 3: newer message with different unsubscribe info replaces the stamp
	// (newest-carrier-wins).
	mail.syncPage = port.MailSyncPage{
		Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Discussion", LastMessageAt: t2, InInbox: true}},
		Messages: []port.IncomingMessage{{
			Message: domain.Message{
				ProviderMessageID: "pm3", ThreadID: "pt1",
				From:   domain.EmailAddress{Email: "digest@news.example"},
				To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
				SentAt: t2,
			},
			Headers: map[string]string{
				"List-Unsubscribe": "<mailto:newsdesk@example.com>",
			},
		}},
	}
	if err := svc.SyncAccount(ctx, "a1"); err != nil {
		t.Fatalf("Sync 3: %v", err)
	}

	th, err = threads.GetByProviderID(ctx, "a1", "pt1")
	if err != nil {
		t.Fatalf("GetByProviderID after sync 3: %v", err)
	}
	if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:newsdesk@example.com" {
		t.Fatalf("sync 3: UnsubscribeMailto = %v, want mailto:newsdesk@example.com (replaced)", th.UnsubscribeMailto)
	}
	if th.UnsubscribeURL != nil {
		t.Fatalf("sync 3: UnsubscribeURL = %v, want nil (no new URL)", th.UnsubscribeURL)
	}
	if th.UnsubscribeOneClick {
		t.Fatal("sync 3: UnsubscribeOneClick = true, want false (no new URL)")
	}
}

func TestSyncStampsUnsubscribeCarrierIndependentOfPageOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	oldTime := now.Add(-10 * time.Minute)

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
	threads := newThreadRepo()
	svc := NewSyncService(SyncServiceDeps{
		Accounts: accounts, Labels: newLabelRepo(), Threads: threads,
		Messages: newMessageRepo(), SyncState: newSyncStateRepo(),
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: mail},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: newOAuthGateway()},
		Clock:         newClock(now),
	})

	t.Run("carrier-after-headerless", func(t *testing.T) {
		// Older message with headers comes AFTER newer headerless message in the page.
		// This should still stamp the thread, but current code fails because the running max
		// already saw the newer message, so the older one's branch never runs.
		mail.syncPage = port.MailSyncPage{
			Threads: []domain.Thread{{ProviderThreadID: "pt1", Subject: "Test", LastMessageAt: now, InInbox: true}},
			Messages: []port.IncomingMessage{
				// Newer message, no headers
				{
					Message: domain.Message{
						ProviderMessageID: "pm2", ThreadID: "pt1",
						From:   domain.EmailAddress{Email: "owner@acme.com"},
						To:     []domain.EmailAddress{{Email: "news@example.com"}},
						SentAt: now,
					},
					Headers: map[string]string{}, // no List-Unsubscribe header
				},
				// Older message with headers (comes after in the slice)
				{
					Message: domain.Message{
						ProviderMessageID: "pm1", ThreadID: "pt1",
						From:   domain.EmailAddress{Email: "news@example.com"},
						To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
						SentAt: oldTime,
					},
					Headers: map[string]string{
						"List-Unsubscribe": "<mailto:unsub@news.example>",
					},
				},
			},
		}
		if err := svc.SyncAccount(ctx, "a1"); err != nil {
			t.Fatalf("SyncAccount: %v", err)
		}

		th, err := threads.GetByProviderID(ctx, "a1", "pt1")
		if err != nil {
			t.Fatalf("GetByProviderID: %v", err)
		}
		// The carrier's info should be stamped even though it came AFTER the headerless message
		if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:unsub@news.example" {
			t.Fatalf("UnsubscribeMailto = %v, want mailto:unsub@news.example", th.UnsubscribeMailto)
		}
	})

	t.Run("carrier-before-headerless", func(t *testing.T) {
		// Older message with headers comes BEFORE newer headerless message in the page.
		// This should also work (and probably does with current code).
		mail.syncPage = port.MailSyncPage{
			Threads: []domain.Thread{{ProviderThreadID: "pt2", Subject: "Test2", LastMessageAt: now, InInbox: true}},
			Messages: []port.IncomingMessage{
				// Older message with headers (comes first in the slice)
				{
					Message: domain.Message{
						ProviderMessageID: "pm3", ThreadID: "pt2",
						From:   domain.EmailAddress{Email: "news@example.com"},
						To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
						SentAt: oldTime,
					},
					Headers: map[string]string{
						"List-Unsubscribe": "<https://news.example/u?id=1>",
					},
				},
				// Newer message, no headers
				{
					Message: domain.Message{
						ProviderMessageID: "pm4", ThreadID: "pt2",
						From:   domain.EmailAddress{Email: "owner@acme.com"},
						To:     []domain.EmailAddress{{Email: "news@example.com"}},
						SentAt: now,
					},
					Headers: map[string]string{}, // no List-Unsubscribe header
				},
			},
		}
		if err := svc.SyncAccount(ctx, "a1"); err != nil {
			t.Fatalf("SyncAccount: %v", err)
		}

		th, err := threads.GetByProviderID(ctx, "a1", "pt2")
		if err != nil {
			t.Fatalf("GetByProviderID: %v", err)
		}
		if th.UnsubscribeURL == nil || *th.UnsubscribeURL != "https://news.example/u?id=1" {
			t.Fatalf("UnsubscribeURL = %v, want https://news.example/u?id=1", th.UnsubscribeURL)
		}
	})

	t.Run("two-carriers-newer-wins", func(t *testing.T) {
		// Two carriers in one page: the newer one should win regardless of order.
		mail.syncPage = port.MailSyncPage{
			Threads: []domain.Thread{{ProviderThreadID: "pt3", Subject: "Test3", LastMessageAt: now, InInbox: true}},
			Messages: []port.IncomingMessage{
				// Older carrier
				{
					Message: domain.Message{
						ProviderMessageID: "pm5", ThreadID: "pt3",
						From:   domain.EmailAddress{Email: "news@example.com"},
						To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
						SentAt: oldTime,
					},
					Headers: map[string]string{
						"List-Unsubscribe": "<mailto:old@example.com>",
					},
				},
				// Newer carrier
				{
					Message: domain.Message{
						ProviderMessageID: "pm6", ThreadID: "pt3",
						From:   domain.EmailAddress{Email: "news@example.com"},
						To:     []domain.EmailAddress{{Email: "owner@acme.com"}},
						SentAt: now,
					},
					Headers: map[string]string{
						"List-Unsubscribe": "<mailto:new@example.com>",
					},
				},
			},
		}
		if err := svc.SyncAccount(ctx, "a1"); err != nil {
			t.Fatalf("SyncAccount: %v", err)
		}

		th, err := threads.GetByProviderID(ctx, "a1", "pt3")
		if err != nil {
			t.Fatalf("GetByProviderID: %v", err)
		}
		// The newer carrier should win
		if th.UnsubscribeMailto == nil || *th.UnsubscribeMailto != "mailto:new@example.com" {
			t.Fatalf("UnsubscribeMailto = %v, want mailto:new@example.com (newer carrier wins)", th.UnsubscribeMailto)
		}
	})
}
