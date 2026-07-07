package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestDraftRepoCreateAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	created, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID,
		Subject:   "Draft subject",
		BodyHTML:  "<p>hi</p>",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create did not assign an id")
	}

	got, err := st.Drafts().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Subject != "Draft subject" {
		t.Fatalf("Subject = %q", got.Subject)
	}
	if got.To == nil || got.Cc == nil || got.Bcc == nil {
		t.Fatalf("To/Cc/Bcc must normalize to non-nil empty: %+v", got)
	}
	if got.SendAttempts != 0 {
		t.Fatalf("SendAttempts = %d, want 0", got.SendAttempts)
	}
	if got.LastError != nil {
		t.Fatalf("LastError = %v, want nil", got.LastError)
	}
}

func TestDraftRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	older, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "older",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Create older: %v", err)
	}
	newer, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "newer",
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	list, err := st.Drafts().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("ListByUser = %+v, want [newer, older]", list)
	}
}

func TestDraftRepoListScheduledDue(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")
	now := time.Now().UTC()

	due, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "due", ScheduledAt: timePtr2(now.Add(-1 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("Create due: %v", err)
	}
	if _, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "future", ScheduledAt: timePtr2(now.Add(1 * time.Hour)),
	}); err != nil {
		t.Fatalf("Create future: %v", err)
	}
	if _, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "unscheduled",
	}); err != nil {
		t.Fatalf("Create unscheduled: %v", err)
	}

	list, err := st.Drafts().ListScheduledDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListScheduledDue: %v", err)
	}
	if len(list) != 1 || list[0].ID != due.ID {
		t.Fatalf("ListScheduledDue = %+v, want only the due draft", list)
	}
}

func TestDraftRepoClaimScheduled(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	due, err := st.Drafts().Create(ctx, domain.Draft{
		AccountID: acct.ID, Subject: "due", ScheduledAt: timePtr2(time.Now().Add(-1 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, err := st.Drafts().ClaimScheduled(ctx, due.ID)
	if err != nil {
		t.Fatalf("ClaimScheduled 1: %v", err)
	}
	if !claimed {
		t.Fatal("first ClaimScheduled must succeed")
	}
	afterClaim, err := st.Drafts().GetByID(ctx, due.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if afterClaim.ScheduledAt != nil {
		t.Fatalf("ScheduledAt after claim = %v, want nil", afterClaim.ScheduledAt)
	}

	claimedAgain, err := st.Drafts().ClaimScheduled(ctx, due.ID)
	if err != nil {
		t.Fatalf("ClaimScheduled 2: %v", err)
	}
	if claimedAgain {
		t.Fatal("second ClaimScheduled must not succeed (double-send guard)")
	}

	unscheduled, err := st.Drafts().Create(ctx, domain.Draft{AccountID: acct.ID, Subject: "no schedule"})
	if err != nil {
		t.Fatalf("Create unscheduled: %v", err)
	}
	claimedUnscheduled, err := st.Drafts().ClaimScheduled(ctx, unscheduled.ID)
	if err != nil {
		t.Fatalf("ClaimScheduled unscheduled: %v", err)
	}
	if claimedUnscheduled {
		t.Fatal("ClaimScheduled on an unscheduled draft must return false")
	}
}

func TestDraftRepoRecordSendFailure(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	d, err := st.Drafts().Create(ctx, domain.Draft{AccountID: acct.ID, Subject: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	next := timePtr2(time.Now().Add(5 * time.Minute))
	if err := st.Drafts().RecordSendFailure(ctx, d.ID, next, "smtp 550"); err != nil {
		t.Fatalf("RecordSendFailure 1: %v", err)
	}
	after1, err := st.Drafts().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after1.SendAttempts != 1 {
		t.Fatalf("SendAttempts = %d, want 1", after1.SendAttempts)
	}
	if after1.LastError == nil || *after1.LastError != "smtp 550" {
		t.Fatalf("LastError = %v, want smtp 550", after1.LastError)
	}
	if after1.ScheduledAt == nil || !after1.ScheduledAt.Equal(*next) {
		t.Fatalf("ScheduledAt = %v, want %v", after1.ScheduledAt, next)
	}

	if err := st.Drafts().RecordSendFailure(ctx, d.ID, nil, "again"); err != nil {
		t.Fatalf("RecordSendFailure 2: %v", err)
	}
	after2, err := st.Drafts().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after2.SendAttempts != 2 {
		t.Fatalf("SendAttempts = %d, want 2", after2.SendAttempts)
	}
	if after2.ScheduledAt != nil {
		t.Fatalf("ScheduledAt after dead-letter = %v, want nil", after2.ScheduledAt)
	}

	if err := st.Drafts().RecordSendFailure(ctx, "nope", nil, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RecordSendFailure unknown: err = %v, want ErrNotFound", err)
	}
}

func TestDraftRepoUpdateResetsRetryBudget(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	d, err := st.Drafts().Create(ctx, domain.Draft{AccountID: acct.ID, Subject: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := st.Drafts().RecordSendFailure(ctx, d.ID, timePtr2(time.Now().Add(time.Minute)), "boom"); err != nil {
		t.Fatalf("RecordSendFailure: %v", err)
	}
	failed, err := st.Drafts().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if failed.SendAttempts == 0 || failed.LastError == nil {
		t.Fatalf("fixture not in failed state: %+v", failed)
	}

	failed.Subject = "edited"
	failed.UpdatedAt = time.Now().UTC()
	if err := st.Drafts().Update(ctx, failed); err != nil {
		t.Fatalf("Update: %v", err)
	}
	edited, err := st.Drafts().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if edited.SendAttempts != 0 {
		t.Fatalf("SendAttempts after Update = %d, want 0", edited.SendAttempts)
	}
	if edited.LastError != nil {
		t.Fatalf("LastError after Update = %v, want nil", edited.LastError)
	}

	if err := st.Drafts().Update(ctx, domain.Draft{ID: "nope", AccountID: acct.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update unknown: err = %v, want ErrNotFound", err)
	}
	if err := st.Drafts().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}
