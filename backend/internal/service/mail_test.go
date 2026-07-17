package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- MailService fixture (Task 5) --------------------------------------------

// mailFixture wires a MailService to the shared package-service fakes with a
// Google mail provider + oauth gateway registered and a frozen clock. It is
// SelfHosted so the paywall is bypassed (paywall is exercised elsewhere).
type mailFixture struct {
	svc      *MailService
	accounts *fakeAccountRepo
	threads  *fakeThreadRepo
	messages *fakeMessageRepo
	drafts   *fakeDraftRepo
	snippets *fakeSnippetRepo
	provider *fakeMailProvider
	oauth    *fakeOAuthGateway
	clock    *fakeClock
}

func newMailFixture(t *testing.T) *mailFixture {
	t.Helper()
	clk := newClock(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC))
	accounts := newAccountRepo()
	f := &mailFixture{
		accounts: accounts,
		threads:  newThreadRepo(),
		messages: newMessageRepo(),
		drafts:   newDraftRepo(accounts),
		snippets: newSnippetRepo(),
		provider: newMailProvider(),
		oauth:    newOAuthGateway(),
		clock:    clk,
	}
	f.svc = NewMailService(MailServiceDeps{
		Accounts:      f.accounts,
		Threads:       f.threads,
		Messages:      f.messages,
		Drafts:        f.drafts,
		Snippets:      f.snippets,
		MailProviders: map[domain.Provider]port.MailProvider{domain.ProviderGoogle: f.provider},
		OAuth:         map[domain.Provider]port.OAuthGateway{domain.ProviderGoogle: f.oauth},
		Clock:         clk,
		SelfHosted:    true,
		UndoSendGrace: 15 * time.Second,
	})
	return f
}

// seedAccount persists an owned Google account with a fresh (non-expired)
// token so provider write-through resolves an access token without a refresh.
func (f *mailFixture) seedAccount(t *testing.T, id, userID string) domain.ConnectedAccount {
	t.Helper()
	ctx := context.Background()
	a := domain.ConnectedAccount{
		ID:       id,
		UserID:   userID,
		Email:    userID + "@acme.com",
		Provider: domain.ProviderGoogle,
		Status:   domain.AccountActive,
	}
	if _, err := f.accounts.Create(ctx, a); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := f.accounts.SaveTokens(ctx, id, port.TokenSet{
		AccessToken:  "tok-" + id,
		RefreshToken: "refresh-" + id,
		ExpiresAt:    f.clock.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	return a
}

// seedThread persists a thread (ProviderThreadID = "p-"+id) with optional mutation.
func (f *mailFixture) seedThread(t *testing.T, id, accountID string, mut func(*domain.Thread)) domain.Thread {
	t.Helper()
	th := domain.Thread{ID: id, AccountID: accountID, ProviderThreadID: "p-" + id, InInbox: true}
	if mut != nil {
		mut(&th)
	}
	if _, err := f.threads.Upsert(context.Background(), th); err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	return th
}

// --- MailService.ActOnThread (all 8 actions) ---------------------------------

func TestActOnThreadMirrorsAndWritesThrough(t *testing.T) {
	const owner = "u1"
	tests := []struct {
		action     domain.ThreadAction
		wantAdd    []string
		wantRemove []string
		check      func(t *testing.T, got domain.Thread)
	}{
		{
			action: domain.ThreadActionArchive, wantAdd: nil, wantRemove: []string{port.LabelKeyInbox},
			check: func(t *testing.T, g domain.Thread) {
				if g.InInbox {
					t.Fatal("archive: InInbox = true, want false")
				}
			},
		},
		{
			action: domain.ThreadActionTrash, wantAdd: []string{port.LabelKeyTrash}, wantRemove: []string{port.LabelKeyInbox},
			check: func(t *testing.T, g domain.Thread) {
				if g.InInbox {
					t.Fatal("trash: InInbox = true, want false")
				}
			},
		},
		{
			action: domain.ThreadActionStar, wantAdd: []string{port.LabelKeyStarred}, wantRemove: nil,
			check: func(t *testing.T, g domain.Thread) {
				if !g.Starred {
					t.Fatal("star: Starred = false, want true")
				}
			},
		},
		{
			action: domain.ThreadActionUnstar, wantAdd: nil, wantRemove: []string{port.LabelKeyStarred},
			check: func(t *testing.T, g domain.Thread) {
				if g.Starred {
					t.Fatal("unstar: Starred = true, want false")
				}
			},
		},
		{
			action: domain.ThreadActionRead, wantAdd: nil, wantRemove: []string{port.LabelKeyUnread},
			check: func(t *testing.T, g domain.Thread) {
				if g.Unread {
					t.Fatal("read: Unread = true, want false")
				}
			},
		},
		{
			action: domain.ThreadActionUnread, wantAdd: []string{port.LabelKeyUnread}, wantRemove: nil,
			check: func(t *testing.T, g domain.Thread) {
				if !g.Unread {
					t.Fatal("unread: Unread = false, want true")
				}
			},
		},
		{
			action: domain.ThreadActionSpam, wantAdd: []string{port.LabelKeySpam}, wantRemove: []string{port.LabelKeyInbox},
			check: func(t *testing.T, g domain.Thread) {
				if g.InInbox {
					t.Fatal("spam: InInbox = true, want false")
				}
			},
		},
		{
			action:  domain.ThreadActionMoveToInbox,
			wantAdd: []string{port.LabelKeyInbox}, wantRemove: []string{port.LabelKeyTrash, port.LabelKeySpam},
			check: func(t *testing.T, g domain.Thread) {
				if !g.InInbox {
					t.Fatal("move_to_inbox: InInbox = false, want true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			f := newMailFixture(t)
			f.seedAccount(t, "a1", owner)
			// Seed the opposite of the mutation so each change is observable.
			// move_to_inbox flips InInbox false->true, so it must seed false;
			// every other action seeds InInbox=true.
			inInbox := tt.action != domain.ThreadActionMoveToInbox
			f.seedThread(t, "t1", "a1", func(th *domain.Thread) {
				th.InInbox = inInbox
				th.Starred = tt.action == domain.ThreadActionUnstar
				th.Unread = tt.action == domain.ThreadActionRead
			})

			got, err := f.svc.ActOnThread(context.Background(), owner, "t1", tt.action)
			if err != nil {
				t.Fatalf("ActOnThread(%s): %v", tt.action, err)
			}
			tt.check(t, got)

			// Local mirror persisted through ThreadRepo.Update.
			stored, err := f.threads.GetByID(context.Background(), "t1")
			if err != nil {
				t.Fatalf("reload thread: %v", err)
			}
			tt.check(t, stored)

			// Exactly one provider write-through with canonical keys, addressed
			// by the provider thread id and carrying the resolved access token.
			if f.provider.modifyLabelsCalls != 1 {
				t.Fatalf("ModifyLabels calls = %d, want 1", f.provider.modifyLabelsCalls)
			}
			if f.provider.lastModifyThreadID != "p-t1" {
				t.Fatalf("provider thread id = %q, want p-t1", f.provider.lastModifyThreadID)
			}
			if f.provider.lastModifyToken != "tok-a1" {
				t.Fatalf("access token = %q, want tok-a1", f.provider.lastModifyToken)
			}
			if !reflect.DeepEqual(f.provider.lastModifyAdd, tt.wantAdd) {
				t.Fatalf("add = %v, want %v", f.provider.lastModifyAdd, tt.wantAdd)
			}
			if !reflect.DeepEqual(f.provider.lastModifyRemove, tt.wantRemove) {
				t.Fatalf("remove = %v, want %v", f.provider.lastModifyRemove, tt.wantRemove)
			}
		})
	}
}

// --- MailService.BulkActOnThreads ---------------------------------------------

func TestBulkActOnThreadsArchivesAndReportsFailures(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	f.seedThread(t, "t1", "a1", nil)
	f.seedThread(t, "t2", "a1", nil)

	res, err := f.svc.BulkActOnThreads(ctx, "u1", []string{"t1", "ghost", "t2"}, domain.ThreadActionArchive)
	if err != nil {
		t.Fatalf("BulkActOnThreads: %v", err)
	}
	if len(res.Threads) != 2 {
		t.Fatalf("len(Threads) = %d, want 2", len(res.Threads))
	}
	for _, th := range res.Threads {
		if th.InInbox {
			t.Fatalf("thread %s still InInbox after bulk archive", th.ID)
		}
	}
	if !reflect.DeepEqual(res.FailedIDs, []string{"ghost"}) {
		t.Fatalf("FailedIDs = %v, want [ghost]", res.FailedIDs)
	}
	if f.provider.modifyLabelsCalls != 2 {
		t.Fatalf("provider write-throughs = %d, want 2", f.provider.modifyLabelsCalls)
	}
}

func TestBulkActOnThreadsValidatesInput(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	if _, err := f.svc.BulkActOnThreads(ctx, "u1", nil, domain.ThreadActionArchive); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty ids err = %v, want ErrValidation", err)
	}
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = "t"
	}
	if _, err := f.svc.BulkActOnThreads(ctx, "u1", tooMany, domain.ThreadActionArchive); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("201 ids err = %v, want ErrValidation", err)
	}
}

// --- MailService.SendDraft: send-now schedules at now + undo-send grace -------

func TestSendDraftSchedulesAtGrace(t *testing.T) {
	const owner = "u1"
	f := newMailFixture(t)
	f.seedAccount(t, "a1", owner)
	now := f.clock.Now()

	if _, err := f.drafts.Create(context.Background(), domain.Draft{
		ID:        "d1",
		AccountID: "a1",
		To:        []domain.EmailAddress{{Email: "bob@x.com"}},
		Subject:   "hi",
		BodyHTML:  "<p>hi</p>",
	}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	msg, err := f.svc.SendDraft(context.Background(), owner, "d1")
	if err != nil {
		t.Fatalf("SendDraft: %v", err)
	}

	wantSendAt := now.Add(15 * time.Second) // fixture UndoSendGrace
	// Provisional message returned to the client.
	if !msg.SentAt.Equal(wantSendAt) {
		t.Fatalf("provisional SentAt = %v, want now+grace %v", msg.SentAt, wantSendAt)
	}
	if !msg.IsDraft {
		t.Fatal("provisional message IsDraft = false, want true")
	}
	if msg.From.Email != "u1@acme.com" {
		t.Fatalf("provisional From = %q, want the account email", msg.From.Email)
	}
	if msg.ID == "" {
		t.Fatal("provisional message has no id")
	}
	if len(msg.Attachments) != 0 || msg.Attachments == nil {
		t.Fatalf("provisional Attachments = %v, want non-nil empty slice", msg.Attachments)
	}

	// Draft persisted with ScheduledAt = now+grace (the worker's claim token).
	stored, err := f.drafts.GetByID(context.Background(), "d1")
	if err != nil {
		t.Fatalf("reload draft: %v", err)
	}
	if stored.ScheduledAt == nil || !stored.ScheduledAt.Equal(wantSendAt) {
		t.Fatalf("stored ScheduledAt = %v, want %v", stored.ScheduledAt, wantSendAt)
	}
	if !stored.UpdatedAt.Equal(now) {
		t.Fatalf("stored UpdatedAt = %v, want %v", stored.UpdatedAt, now)
	}
}

// --- MailService.MarkThreadOpened provider write-through ---------------------
//
// TestMarkThreadOpenedIdempotentAndOwned (ops_test.go) covers ownership and
// idempotency with no provider wired up; this asserts the PROVIDER
// write-through branch specifically: opening an unread thread removes the
// UNREAD label upstream, in addition to the local mirror update.

func TestMarkThreadOpenedWritesThroughToProvider(t *testing.T) {
	const owner = "u1"
	f := newMailFixture(t)
	f.seedAccount(t, "a1", owner)
	f.seedThread(t, "t1", "a1", func(th *domain.Thread) { th.Unread = true })

	if err := f.svc.MarkThreadOpened(context.Background(), owner, "t1"); err != nil {
		t.Fatalf("MarkThreadOpened: %v", err)
	}

	// Local mirror updated via ThreadRepo.MarkOpened.
	if f.threads.markOpened != 1 {
		t.Fatalf("MarkOpened calls = %d, want 1", f.threads.markOpened)
	}

	// Provider write-through: ModifyLabels called with the provider thread id,
	// the resolved access token, add=nil, remove=[UNREAD].
	if f.provider.modifyLabelsCalls != 1 {
		t.Fatalf("ModifyLabels calls = %d, want 1", f.provider.modifyLabelsCalls)
	}
	if f.provider.lastModifyThreadID != "p-t1" {
		t.Fatalf("provider thread id = %q, want p-t1", f.provider.lastModifyThreadID)
	}
	if f.provider.lastModifyToken != "tok-a1" {
		t.Fatalf("access token = %q, want tok-a1", f.provider.lastModifyToken)
	}
	if f.provider.lastModifyAdd != nil {
		t.Fatalf("add = %v, want nil", f.provider.lastModifyAdd)
	}
	if !reflect.DeepEqual(f.provider.lastModifyRemove, []string{port.LabelKeyUnread}) {
		t.Fatalf("remove = %v, want [%s]", f.provider.lastModifyRemove, port.LabelKeyUnread)
	}
}

// --- MailService.GetThread ----------------------------------------------------

func TestGetThread(t *testing.T) {
	const owner = "u1"

	t.Run("owned thread returns messages in insertion order", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)
		if _, err := f.messages.Upsert(context.Background(), domain.Message{ID: "m1", ThreadID: "t1"}); err != nil {
			t.Fatalf("seed m1: %v", err)
		}
		if _, err := f.messages.Upsert(context.Background(), domain.Message{ID: "m2", ThreadID: "t1"}); err != nil {
			t.Fatalf("seed m2: %v", err)
		}

		got, msgs, err := f.svc.GetThread(context.Background(), owner, "t1")
		if err != nil {
			t.Fatalf("GetThread: %v", err)
		}
		if got.ID != "t1" {
			t.Fatalf("thread ID = %q, want t1", got.ID)
		}
		if len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].ID != "m2" {
			t.Fatalf("messages = %+v, want [m1 m2] in order", msgs)
		}
	})

	t.Run("owned thread with no messages returns non-nil empty slice", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)

		_, msgs, err := f.svc.GetThread(context.Background(), owner, "t1")
		if err != nil {
			t.Fatalf("GetThread: %v", err)
		}
		if msgs == nil || len(msgs) != 0 {
			t.Fatalf("messages = %v, want non-nil empty slice", msgs)
		}
	})

	t.Run("foreign thread returns ErrNotFound with zero thread and nil messages", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)

		got, msgs, err := f.svc.GetThread(context.Background(), "intruder", "t1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got.ID != "" {
			t.Fatalf("thread = %+v, want zero value", got)
		}
		if msgs != nil {
			t.Fatalf("messages = %v, want nil", msgs)
		}
	})

	t.Run("unknown thread id returns ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		_, _, err := f.svc.GetThread(context.Background(), owner, "missing")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- MailService.SnoozeThread -------------------------------------------------

func TestSnoozeThread(t *testing.T) {
	const owner = "u1"

	t.Run("future until sets SnoozedUntil and persists", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)
		until := f.clock.Now().Add(time.Hour)

		got, err := f.svc.SnoozeThread(context.Background(), owner, "t1", until)
		if err != nil {
			t.Fatalf("SnoozeThread: %v", err)
		}
		if got.SnoozedUntil == nil || !got.SnoozedUntil.Equal(until) {
			t.Fatalf("SnoozedUntil = %v, want %v", got.SnoozedUntil, until)
		}
		stored, err := f.threads.GetByID(context.Background(), "t1")
		if err != nil {
			t.Fatalf("reload thread: %v", err)
		}
		if stored.SnoozedUntil == nil || !stored.SnoozedUntil.Equal(until) {
			t.Fatalf("stored SnoozedUntil = %v, want %v", stored.SnoozedUntil, until)
		}
		if f.provider.modifyLabelsCalls != 0 {
			t.Fatalf("ModifyLabels calls = %d, want 0 (no provider side-effects)", f.provider.modifyLabelsCalls)
		}
	})

	t.Run("until equal to now is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)

		_, err := f.svc.SnoozeThread(context.Background(), owner, "t1", f.clock.Now())
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("past until is ErrValidation even for a foreign thread", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)

		_, err := f.svc.SnoozeThread(context.Background(), "intruder", "t1", f.clock.Now().Add(-time.Hour))
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation (validation precedes ownership)", err)
		}
	})

	t.Run("foreign thread with future until is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)

		_, err := f.svc.SnoozeThread(context.Background(), "intruder", "t1", f.clock.Now().Add(time.Hour))
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- MailService.UnsnoozeThread -----------------------------------------------

func TestUnsnoozeThreadClearsSnoozeWithoutUnread(t *testing.T) {
	f := newMailFixture(t)
	ctx := context.Background()
	f.seedAccount(t, "a1", "u1")
	until := f.clock.Now().Add(4 * time.Hour)
	f.seedThread(t, "t1", "a1", func(th *domain.Thread) {
		th.SnoozedUntil = &until
		th.Unread = false
	})

	got, err := f.svc.UnsnoozeThread(ctx, "u1", "t1")
	if err != nil {
		t.Fatalf("UnsnoozeThread: %v", err)
	}
	if got.SnoozedUntil != nil {
		t.Fatalf("SnoozedUntil = %v, want nil", got.SnoozedUntil)
	}
	if got.Unread {
		t.Fatal("Unread flipped to true; unsnooze must not fake a wake-up")
	}

	// Foreign user gets 404 semantics.
	if _, err := f.svc.UnsnoozeThread(ctx, "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign unsnooze err = %v, want ErrNotFound", err)
	}
}

// --- MailService.SetReminder --------------------------------------------------

func TestSetReminder(t *testing.T) {
	const owner = "u1"

	t.Run("future remindAt sets RemindAt and persists", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)
		remindAt := f.clock.Now().Add(time.Hour)

		got, err := f.svc.SetReminder(context.Background(), owner, "t1", &remindAt)
		if err != nil {
			t.Fatalf("SetReminder: %v", err)
		}
		if got.RemindAt == nil || !got.RemindAt.Equal(remindAt) {
			t.Fatalf("RemindAt = %v, want %v", got.RemindAt, remindAt)
		}
		stored, err := f.threads.GetByID(context.Background(), "t1")
		if err != nil {
			t.Fatalf("reload thread: %v", err)
		}
		if stored.RemindAt == nil || !stored.RemindAt.Equal(remindAt) {
			t.Fatalf("stored RemindAt = %v, want %v", stored.RemindAt, remindAt)
		}
		if f.provider.modifyLabelsCalls != 0 {
			t.Fatalf("ModifyLabels calls = %d, want 0 (no provider side-effects)", f.provider.modifyLabelsCalls)
		}
	})

	t.Run("nil clears an existing reminder", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		existing := f.clock.Now().Add(time.Hour)
		f.seedThread(t, "t1", "a1", func(th *domain.Thread) { th.RemindAt = &existing })

		got, err := f.svc.SetReminder(context.Background(), owner, "t1", nil)
		if err != nil {
			t.Fatalf("SetReminder: %v", err)
		}
		if got.RemindAt != nil {
			t.Fatalf("RemindAt = %v, want nil", got.RemindAt)
		}
		stored, err := f.threads.GetByID(context.Background(), "t1")
		if err != nil {
			t.Fatalf("reload thread: %v", err)
		}
		if stored.RemindAt != nil {
			t.Fatalf("stored RemindAt = %v, want nil", stored.RemindAt)
		}
	})

	t.Run("past remindAt is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)
		past := f.clock.Now().Add(-time.Hour)

		_, err := f.svc.SetReminder(context.Background(), owner, "t1", &past)
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("foreign thread with future remindAt is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedThread(t, "t1", "a1", nil)
		future := f.clock.Now().Add(time.Hour)

		_, err := f.svc.SetReminder(context.Background(), "intruder", "t1", &future)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- MailService.CreateDraft --------------------------------------------------

func TestCreateDraft(t *testing.T) {
	const owner = "u1"

	t.Run("missing AccountID is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		_, err := f.svc.CreateDraft(context.Background(), owner, port.DraftInput{})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("account owned by another user is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedAccount(t, "a2", "other")

		_, err := f.svc.CreateDraft(context.Background(), owner, port.DraftInput{AccountID: "a2"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("happy path generates id and persists", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		got, err := f.svc.CreateDraft(context.Background(), owner, port.DraftInput{
			AccountID: "a1",
			To:        []domain.EmailAddress{{Email: "x@y.com"}},
			Subject:   "s",
			BodyHTML:  "b",
		})
		if err != nil {
			t.Fatalf("CreateDraft: %v", err)
		}
		if got.ID == "" {
			t.Fatal("no generated id")
		}
		if got.AccountID != "a1" {
			t.Fatalf("AccountID = %q, want a1", got.AccountID)
		}
		if len(got.To) != 1 || got.To[0].Email != "x@y.com" {
			t.Fatalf("To = %+v, want [x@y.com]", got.To)
		}
		if got.Cc == nil || len(got.Cc) != 0 {
			t.Fatalf("Cc = %v, want non-nil empty slice", got.Cc)
		}
		if got.Bcc == nil || len(got.Bcc) != 0 {
			t.Fatalf("Bcc = %v, want non-nil empty slice", got.Bcc)
		}
		if !got.UpdatedAt.Equal(f.clock.Now()) {
			t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, f.clock.Now())
		}
		stored, err := f.drafts.GetByID(context.Background(), got.ID)
		if err != nil {
			t.Fatalf("reload draft: %v", err)
		}
		if stored.ID != got.ID {
			t.Fatalf("stored draft id = %q, want %q", stored.ID, got.ID)
		}
	})

	t.Run("ScheduledAt passed through", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		future := f.clock.Now().Add(time.Hour)

		got, err := f.svc.CreateDraft(context.Background(), owner, port.DraftInput{
			AccountID:   "a1",
			ScheduledAt: &future,
		})
		if err != nil {
			t.Fatalf("CreateDraft: %v", err)
		}
		if got.ScheduledAt == nil || !got.ScheduledAt.Equal(future) {
			t.Fatalf("ScheduledAt = %v, want %v", got.ScheduledAt, future)
		}
	})
}

// --- MailService.UpdateDraft ---------------------------------------------------

func TestUpdateDraft(t *testing.T) {
	const owner = "u1"

	seedDraft := func(t *testing.T, f *mailFixture) {
		t.Helper()
		if _, err := f.drafts.Create(context.Background(), domain.Draft{
			ID: "d1", AccountID: "a1", Subject: "orig",
		}); err != nil {
			t.Fatalf("seed draft: %v", err)
		}
	}

	t.Run("foreign draft is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", "other")
		seedDraft(t, f)

		_, err := f.svc.UpdateDraft(context.Background(), owner, "d1", port.DraftInput{})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("moving to a different account is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedAccount(t, "a2", owner)
		seedDraft(t, f)

		_, err := f.svc.UpdateDraft(context.Background(), owner, "d1", port.DraftInput{AccountID: "a2"})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("happy path updates fields and UpdatedAt", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		seedDraft(t, f)
		f.clock.Advance(time.Minute)
		future := f.clock.Now().Add(time.Hour)

		got, err := f.svc.UpdateDraft(context.Background(), owner, "d1", port.DraftInput{
			Subject:     "updated",
			BodyHTML:    "body",
			To:          []domain.EmailAddress{{Email: "z@y.com"}},
			ScheduledAt: &future,
		})
		if err != nil {
			t.Fatalf("UpdateDraft: %v", err)
		}
		if got.Subject != "updated" || got.BodyHTML != "body" {
			t.Fatalf("draft not updated: %+v", got)
		}
		if len(got.To) != 1 || got.To[0].Email != "z@y.com" {
			t.Fatalf("To = %+v, want [z@y.com]", got.To)
		}
		if got.ScheduledAt == nil || !got.ScheduledAt.Equal(future) {
			t.Fatalf("ScheduledAt = %v, want %v", got.ScheduledAt, future)
		}
		if !got.UpdatedAt.Equal(f.clock.Now()) {
			t.Fatalf("UpdatedAt = %v, want %v (advanced clock)", got.UpdatedAt, f.clock.Now())
		}
		stored, err := f.drafts.GetByID(context.Background(), "d1")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if stored.Subject != "updated" {
			t.Fatalf("persisted subject = %q, want updated", stored.Subject)
		}
	})

	t.Run("empty AccountID keeps the existing account", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		seedDraft(t, f)

		got, err := f.svc.UpdateDraft(context.Background(), owner, "d1", port.DraftInput{AccountID: ""})
		if err != nil {
			t.Fatalf("UpdateDraft: %v", err)
		}
		if got.AccountID != "a1" {
			t.Fatalf("AccountID = %q, want a1", got.AccountID)
		}
	})
}

// --- MailService.GetDraft ------------------------------------------------------

func TestGetDraft(t *testing.T) {
	const owner = "u1"

	t.Run("owned draft returns it", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed draft: %v", err)
		}

		got, err := f.svc.GetDraft(context.Background(), owner, "d1")
		if err != nil {
			t.Fatalf("GetDraft: %v", err)
		}
		if got.ID != "d1" {
			t.Fatalf("ID = %q, want d1", got.ID)
		}
	})

	t.Run("foreign draft is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", "other")
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed draft: %v", err)
		}

		_, err := f.svc.GetDraft(context.Background(), owner, "d1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown id is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		_, err := f.svc.GetDraft(context.Background(), owner, "missing")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- MailService.ListDrafts -----------------------------------------------------

func TestListDrafts(t *testing.T) {
	const owner = "u1"

	t.Run("returns drafts under the user's accounts", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d2", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d2: %v", err)
		}

		got, err := f.svc.ListDrafts(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListDrafts: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("excludes another user's drafts", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		f.seedAccount(t, "a2", "other")
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d2", AccountID: "a2"}); err != nil {
			t.Fatalf("seed d2: %v", err)
		}

		got, err := f.svc.ListDrafts(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListDrafts: %v", err)
		}
		if len(got) != 1 || got[0].ID != "d1" {
			t.Fatalf("got = %+v, want only d1", got)
		}
	})

	t.Run("no drafts returns non-nil empty slice", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		got, err := f.svc.ListDrafts(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListDrafts: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got = %v, want non-nil empty slice", got)
		}
	})
}

// --- MailService.DeleteDraft ----------------------------------------------------

func TestDeleteDraft(t *testing.T) {
	const owner = "u1"

	t.Run("owned draft is deleted", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		if err := f.svc.DeleteDraft(context.Background(), owner, "d1"); err != nil {
			t.Fatalf("DeleteDraft: %v", err)
		}
		if _, err := f.drafts.GetByID(context.Background(), "d1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound after delete", err)
		}
	})

	t.Run("foreign draft is ErrNotFound and not deleted", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", "other")
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		if err := f.svc.DeleteDraft(context.Background(), owner, "d1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := f.drafts.GetByID(context.Background(), "d1"); err != nil {
			t.Fatalf("draft should still exist: %v", err)
		}
	})
}

// --- MailService.SendDraft: validation + ownership guards ----------------------

func TestSendDraftValidationAndOwnership(t *testing.T) {
	const owner = "u1"

	t.Run("no recipients is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		_, err := f.svc.SendDraft(context.Background(), owner, "d1")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("foreign draft is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", "other")
		if _, err := f.drafts.Create(context.Background(), domain.Draft{
			ID: "d1", AccountID: "a1", To: []domain.EmailAddress{{Email: "x@y.com"}},
		}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		_, err := f.svc.SendDraft(context.Background(), owner, "d1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown draft id is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)

		_, err := f.svc.SendDraft(context.Background(), owner, "missing")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- MailService.SendDraft: Send Later keeps the requested time ----------------

func TestSendDraftSendLaterKeepsScheduledTime(t *testing.T) {
	const owner = "u1"

	t.Run("future ScheduledAt beyond grace is kept", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		wantAt := f.clock.Now().Add(2 * time.Hour)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{
			ID:          "d1",
			AccountID:   "a1",
			To:          []domain.EmailAddress{{Email: "x@y.com"}},
			ScheduledAt: &wantAt,
		}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		msg, err := f.svc.SendDraft(context.Background(), owner, "d1")
		if err != nil {
			t.Fatalf("SendDraft: %v", err)
		}
		if !msg.SentAt.Equal(wantAt) {
			t.Fatalf("SentAt = %v, want %v", msg.SentAt, wantAt)
		}
		stored, err := f.drafts.GetByID(context.Background(), "d1")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if stored.ScheduledAt == nil || !stored.ScheduledAt.Equal(wantAt) {
			t.Fatalf("stored ScheduledAt = %v, want %v", stored.ScheduledAt, wantAt)
		}
	})

	t.Run("past ScheduledAt is treated as send-now", func(t *testing.T) {
		f := newMailFixture(t)
		f.seedAccount(t, "a1", owner)
		past := f.clock.Now().Add(-time.Hour)
		if _, err := f.drafts.Create(context.Background(), domain.Draft{
			ID:          "d1",
			AccountID:   "a1",
			To:          []domain.EmailAddress{{Email: "x@y.com"}},
			ScheduledAt: &past,
		}); err != nil {
			t.Fatalf("seed d1: %v", err)
		}

		msg, err := f.svc.SendDraft(context.Background(), owner, "d1")
		if err != nil {
			t.Fatalf("SendDraft: %v", err)
		}
		want := f.clock.Now().Add(15 * time.Second)
		if !msg.SentAt.Equal(want) {
			t.Fatalf("SentAt = %v, want now+grace %v", msg.SentAt, want)
		}
	})
}

// --- MailService.CreateSnippet --------------------------------------------------

func TestCreateSnippet(t *testing.T) {
	const owner = "u1"

	t.Run("blank name is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)

		_, err := f.svc.CreateSnippet(context.Background(), owner, port.SnippetInput{Name: "  ", BodyHTML: "b"})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("blank body is ErrValidation", func(t *testing.T) {
		f := newMailFixture(t)

		_, err := f.svc.CreateSnippet(context.Background(), owner, port.SnippetInput{Name: "n", BodyHTML: " "})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("happy path persists the snippet", func(t *testing.T) {
		f := newMailFixture(t)
		shortcut := "/sc"

		got, err := f.svc.CreateSnippet(context.Background(), owner, port.SnippetInput{
			Name: "n", Shortcut: &shortcut, BodyHTML: "b",
		})
		if err != nil {
			t.Fatalf("CreateSnippet: %v", err)
		}
		if got.ID == "" {
			t.Fatal("no generated id")
		}
		if got.UserID != owner {
			t.Fatalf("UserID = %q, want %q", got.UserID, owner)
		}
		if got.Name != "n" || got.BodyHTML != "b" {
			t.Fatalf("snippet = %+v", got)
		}
		if got.Shortcut == nil || *got.Shortcut != "/sc" {
			t.Fatalf("Shortcut = %v, want /sc", got.Shortcut)
		}
		stored, err := f.snippets.GetByID(context.Background(), got.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if stored.ID != got.ID {
			t.Fatalf("stored id = %q, want %q", stored.ID, got.ID)
		}
	})
}

// --- MailService.UpdateSnippet --------------------------------------------------

func TestUpdateSnippet(t *testing.T) {
	const owner = "u1"

	t.Run("foreign snippet is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: "other", Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}

		_, err := f.svc.UpdateSnippet(context.Background(), owner, "s1", port.SnippetInput{Name: "n2", BodyHTML: "b2"})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("blank name is ErrValidation before ownership", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: "other", Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}

		_, err := f.svc.UpdateSnippet(context.Background(), owner, "s1", port.SnippetInput{Name: " ", BodyHTML: "b2"})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("happy path updates fields", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: owner, Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}
		shortcut := "/x"

		got, err := f.svc.UpdateSnippet(context.Background(), owner, "s1", port.SnippetInput{
			Name: "n2", Shortcut: &shortcut, BodyHTML: "b2",
		})
		if err != nil {
			t.Fatalf("UpdateSnippet: %v", err)
		}
		if got.Name != "n2" || got.BodyHTML != "b2" {
			t.Fatalf("snippet = %+v", got)
		}
		if got.Shortcut == nil || *got.Shortcut != "/x" {
			t.Fatalf("Shortcut = %v, want /x", got.Shortcut)
		}
		stored, err := f.snippets.GetByID(context.Background(), "s1")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if stored.Name != "n2" {
			t.Fatalf("stored Name = %q, want n2", stored.Name)
		}
	})
}

// --- MailService.DeleteSnippet --------------------------------------------------

func TestDeleteSnippet(t *testing.T) {
	const owner = "u1"

	t.Run("owned snippet is deleted", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: owner, Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}

		if err := f.svc.DeleteSnippet(context.Background(), owner, "s1"); err != nil {
			t.Fatalf("DeleteSnippet: %v", err)
		}
		if _, err := f.snippets.GetByID(context.Background(), "s1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound after delete", err)
		}
	})

	t.Run("foreign snippet is ErrNotFound and not deleted", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: "other", Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}

		if err := f.svc.DeleteSnippet(context.Background(), owner, "s1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := f.snippets.GetByID(context.Background(), "s1"); err != nil {
			t.Fatalf("snippet should still exist: %v", err)
		}
	})
}

// --- MailService.ListSnippets ----------------------------------------------------

func TestListSnippets(t *testing.T) {
	const owner = "u1"

	t.Run("returns the user's snippets", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: owner, Name: "n1", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed s1: %v", err)
		}
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s2", UserID: owner, Name: "n2", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed s2: %v", err)
		}

		got, err := f.svc.ListSnippets(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("excludes another user's snippet", func(t *testing.T) {
		f := newMailFixture(t)
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s1", UserID: owner, Name: "n1", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed s1: %v", err)
		}
		if _, err := f.snippets.Create(context.Background(), domain.Snippet{ID: "s2", UserID: "other", Name: "n2", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed s2: %v", err)
		}

		got, err := f.svc.ListSnippets(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		if len(got) != 1 || got[0].ID != "s1" {
			t.Fatalf("got = %+v, want only s1", got)
		}
	})

	t.Run("no snippets returns non-nil empty slice", func(t *testing.T) {
		f := newMailFixture(t)

		got, err := f.svc.ListSnippets(context.Background(), owner)
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got = %v, want non-nil empty slice", got)
		}
	})
}
