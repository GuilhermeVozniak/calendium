package service

// ai_jobs_autodraft_test.go covers Task 8's runAutoDraft: the auto_draft AI
// job handler that drafts (or refreshes) a provisional reply to a thread
// awaiting a response from the owner, optionally offering availability slots
// when the newest message is a meeting request. Handlers are exercised
// directly (not through ProcessDueAiJobs) for precise control over the
// thread/message/draft fixtures each scenario needs.

import (
	"context"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

// newAutoDraftDeps wires the common fixtures shared by every scenario below:
// one connected account (owner@example.com), one thread on it, and empty
// jobs/usage/drafts repos ready for the test to seed further.
func newAutoDraftDeps(t *testing.T, now time.Time) (AIJobServiceDeps, *fakeAccountRepo, *fakeThreadRepo, *fakeMessageRepo, *fakeDraftRepo, *fakeAI, *fakeCalendarService) {
	t.Helper()
	accounts := newAccountRepo()
	if _, err := accounts.Create(context.Background(), domain.ConnectedAccount{
		ID: "a1", UserID: "u1", Email: "owner@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	threads := newThreadRepo()
	if _, err := threads.Upsert(context.Background(), domain.Thread{
		ID: "t1", AccountID: "a1", Subject: "Hello",
	}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	drafts := newDraftRepo(accounts)
	ai := newAI()
	cal := newCalendarService()

	deps := AIJobServiceDeps{
		Jobs:       newAiJobRepo(),
		Usage:      newAiUsageRepo(),
		Accounts:   accounts,
		Threads:    threads,
		Messages:   messages,
		Drafts:     drafts,
		Calendar:   cal,
		AI:         ai,
		Clock:      newClock(now),
		DailyLimit: 300,
	}
	return deps, accounts, threads, messages, drafts, ai, cal
}

func autoDraftJob(payload map[string]string) domain.AiJob {
	if payload == nil {
		payload = map[string]string{}
	}
	return domain.AiJob{
		ID: "j1", UserID: "u1", AccountID: "a1",
		Kind: domain.AiJobAutoDraft, ThreadID: strPtr("t1"), Payload: payload,
	}
}

func TestAIJobAutoDraftAwaitingReplyCreatesDraft(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"model subject","bodyHtml":"<p>reply</p>"}`
	ai.jsonModel = "gpt-test"

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("drafts = %d, want 1", len(all))
	}
	d := all[0]
	if !d.AiGenerated {
		t.Fatalf("draft.AiGenerated = false, want true")
	}
	if d.ThreadID == nil || *d.ThreadID != "t1" {
		t.Fatalf("draft.ThreadID = %v, want t1", d.ThreadID)
	}
	if len(d.To) != 1 || d.To[0].Email != "sender@example.com" {
		t.Fatalf("draft.To = %v, want [sender@example.com]", d.To)
	}
	if d.Subject != "Re: Hello" {
		t.Fatalf("draft.Subject = %q, want %q (thread subject wins over model subject)", d.Subject, "Re: Hello")
	}
	if d.BodyHTML != "<p>reply</p>" {
		t.Fatalf("draft.BodyHTML = %q, want <p>reply</p>", d.BodyHTML)
	}
	if d.ScheduledAt != nil {
		t.Fatalf("draft.ScheduledAt = %v, want nil (auto drafts never auto-send)", d.ScheduledAt)
	}
}

// TestAIJobAutoDraftSubjectAvoidsDoublePrefix covers MINOR 4 (final-review
// fix wave): when the thread subject already carries a "Re:" prefix
// (case-insensitively, with surrounding whitespace), the auto-drafted reply
// must reuse it as-is rather than stacking a second "Re: Re: " prefix.
func TestAIJobAutoDraftSubjectAvoidsDoublePrefix(t *testing.T) {
	tests := []struct {
		name          string
		threadSubject string
		wantSubject   string
	}{
		{"already prefixed, standard case", "Re: Budget approval", "Re: Budget approval"},
		{"already prefixed, different case", "re: Budget approval", "re: Budget approval"},
		{"already prefixed, upper case with leading space", "  RE: Budget approval", "RE: Budget approval"},
		{"not yet prefixed gets one Re:", "Budget approval", "Re: Budget approval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
			deps, _, threads, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
			threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: tt.threadSubject}
			if _, err := messages.Upsert(ctx, domain.Message{
				ID: "m1", ThreadID: "t1", AccountID: "a1",
				From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
			ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"model subject","bodyHtml":"<p>reply</p>"}`

			svc := NewAIJobService(deps)
			if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
				t.Fatalf("runAutoDraft() error = %v, want nil", err)
			}

			all, err := drafts.ListByUser(ctx, "u1")
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 {
				t.Fatalf("drafts = %d, want 1", len(all))
			}
			if all[0].Subject != tt.wantSubject {
				t.Fatalf("draft.Subject = %q, want %q", all[0].Subject, tt.wantSubject)
			}
		})
	}
}

func TestAIJobAutoDraftShouldDraftFalseCreatesNoDraft(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":false,"isMeetingRequest":false,"subject":"","bodyHtml":""}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("drafts = %d, want 0 (shouldDraft=false stores nothing)", len(all))
	}
}

func TestAIJobAutoDraftMeetingRequestOffersAvailability(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, cal := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	cal.calendars = []domain.Calendar{{ID: "c1", TimeZone: "America/New_York", IsPrimary: true}}
	cal.slots = []domain.AvailabilitySlot{
		{Start: now.Add(1 * time.Hour), End: now.Add(90 * time.Minute)},
		{Start: now.Add(2 * time.Hour), End: now.Add(150 * time.Minute)},
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":true,"subject":"model subject","bodyHtml":"<p>how about one of these times</p>"}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	if !strings.Contains(ai.lastUser, "AVAILABILITY:") {
		t.Fatalf("second CompleteJSON user prompt = %q, want it to contain an AVAILABILITY: block", ai.lastUser)
	}

	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("drafts = %d, want 1", len(all))
	}
	if all[0].BodyHTML != "<p>how about one of these times</p>" {
		t.Fatalf("draft.BodyHTML = %q, want the second pass's body persisted", all[0].BodyHTML)
	}

	// Two CompleteJSON passes for a meeting request: 2 budget units charged.
	usage := deps.Usage.(*fakeAiUsageRepo)
	if usage.calls["u1"] != 2 {
		t.Fatalf("usage.calls[u1] = %d, want 2 (scheduling drafts cost 2 budget units)", usage.calls["u1"])
	}
}

func TestAIJobAutoDraftNonMeetingRequestChargesOneBudgetUnit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, _, ai, _ := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"x","bodyHtml":"<p>x</p>"}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}
	usage := deps.Usage.(*fakeAiUsageRepo)
	if usage.calls["u1"] != 1 {
		t.Fatalf("usage.calls[u1] = %d, want 1 (single CompleteJSON pass)", usage.calls["u1"])
	}
}

func TestAIJobAutoDraftExistingAIDraftUpdatedNotDuplicated(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	if _, err := drafts.Create(ctx, domain.Draft{
		ID: "d1", AccountID: "a1", ThreadID: strPtr("t1"),
		To:      []domain.EmailAddress{{Email: "old-sender@example.com"}},
		Subject: "Re: Hello", BodyHTML: "<p>stale</p>",
		AiGenerated: true, UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"x","bodyHtml":"<p>fresh</p>"}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("drafts = %d, want 1 (updated in place, not duplicated)", len(all))
	}
	d := all[0]
	if d.ID != "d1" {
		t.Fatalf("draft.ID = %q, want d1 (same draft updated)", d.ID)
	}
	if d.BodyHTML != "<p>fresh</p>" {
		t.Fatalf("draft.BodyHTML = %q, want <p>fresh</p> (refreshed for the new message)", d.BodyHTML)
	}
	if len(d.To) != 1 || d.To[0].Email != "sender@example.com" {
		t.Fatalf("draft.To = %v, want [sender@example.com] (re-addressed to the newest sender)", d.To)
	}
}

func TestAIJobAutoDraftUserEditedDraftUntouched(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	// The owner edited the AI draft (MailService.UpdateDraft clears the
	// flag on any manual save); GetAiGeneratedByThread now returns
	// ErrNotFound for this thread, same as if there were no draft at all.
	if _, err := drafts.Create(ctx, domain.Draft{
		ID: "d1", AccountID: "a1", ThreadID: strPtr("t1"),
		To:      []domain.EmailAddress{{Email: "old-sender@example.com"}},
		Subject: "Re: Hello", BodyHTML: "<p>the owner's own words</p>",
		AiGenerated: false, UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"x","bodyHtml":"<p>fresh</p>"}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	got, err := drafts.GetByID(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyHTML != "<p>the owner's own words</p>" {
		t.Fatalf("draft.BodyHTML = %q, want the user-edited draft left untouched", got.BodyHTML)
	}
	if got.AiGenerated {
		t.Fatalf("draft.AiGenerated = true, want it to stay false (user-owned)")
	}
}

func TestAIJobAutoDraftNewestMessageFromOwnerSkips(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "owner@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"x","bodyHtml":"<p>x</p>"}`

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q lastUser=%q), want it skipped before any LLM call", ai.lastSystem, ai.lastUser)
	}
	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("drafts = %d, want 0", len(all))
	}
}

func TestAIJobAutoDraftLastMessageIDAlreadyProcessedSkips(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, messages, drafts, ai, _ := newAutoDraftDeps(t, now)
	if _, err := messages.Upsert(ctx, domain.Message{
		ID: "m1", ThreadID: "t1", AccountID: "a1",
		From: domain.EmailAddress{Email: "sender@example.com"}, SentAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ai.jsonOut = `{"shouldDraft":true,"isMeetingRequest":false,"subject":"x","bodyHtml":"<p>x</p>"}`

	svc := NewAIJobService(deps)
	job := autoDraftJob(map[string]string{"lastMessageId": "m1"})
	if err := svc.runAutoDraft(ctx, job); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}

	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called, want it skipped: this exact message was already drafted against")
	}
	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("drafts = %d, want 0", len(all))
	}
}

func TestAIJobAutoDraftNoMessagesIsNoop(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	deps, _, _, _, drafts, ai, _ := newAutoDraftDeps(t, now)

	svc := NewAIJobService(deps)
	if err := svc.runAutoDraft(ctx, autoDraftJob(nil)); err != nil {
		t.Fatalf("runAutoDraft() error = %v, want nil", err)
	}
	if ai.lastSystem != "" {
		t.Fatalf("AI was called with no messages on the thread, want it skipped")
	}
	all, err := drafts.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("drafts = %d, want 0", len(all))
	}
}
