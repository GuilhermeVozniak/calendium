package service

// ai_jobs_voice_test.go covers Task 11's runVoiceProfile: it learns a
// writing-style profile from an account's newest sent mail via
// completeJSONBudgeted, upserts it, and re-enqueues itself ~30 days out so
// the profile stays current. Unlike the thread-scoped job kinds it has no
// existence guard (voice_profile jobs carry no ThreadID); the "not enough
// signal yet" and idempotent-upsert paths below are its equivalents.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func seedSentMessages(messages *fakeMessageRepo, accountID, email string, n int) {
	for i := 0; i < n; i++ {
		id := newID()
		_, _ = messages.Upsert(context.Background(), domain.Message{
			ID:        id,
			AccountID: accountID,
			From:      domain.EmailAddress{Email: email},
			BodyText:  "hi there, thanks for the update! - Alex",
			SentAt:    time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
}

func newVoiceJobService(accounts *fakeAccountRepo, messages *fakeMessageRepo, voice *fakeVoiceProfileRepo, jobs *fakeAiJobRepo, ai *fakeAI, clock *fakeClock) *AIJobService {
	return NewAIJobService(AIJobServiceDeps{
		Jobs:          jobs,
		Usage:         newAiUsageRepo(),
		Accounts:      accounts,
		Messages:      messages,
		VoiceProfiles: voice,
		AI:            ai,
		Clock:         clock,
		DailyLimit:    100,
	})
}

func TestAIJobVoiceProfileFewerThanMinSamplesNoOp(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	seedSentMessages(messages, "a1", "alex@example.com", 4) // below voiceMinSamples (5)
	voice := newVoiceProfileRepo()
	jobs := newAiJobRepo()
	ai := newAI()
	ai.jsonOut = `{"profile":"should not be reached"}`

	svc := newVoiceJobService(accounts, messages, voice, jobs, ai, newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile}
	if err := svc.runVoiceProfile(ctx, j); err != nil {
		t.Fatalf("runVoiceProfile() error = %v, want nil", err)
	}
	if _, ok := voice.byUser["u1"]; ok {
		t.Fatal("VoiceProfiles.Upsert was called, want skipped (not enough samples)")
	}
	if len(jobs.queue) != 0 {
		t.Fatalf("jobs.queue = %v, want empty (no self re-enqueue without a generated profile)", jobs.queue)
	}
	if ai.lastSystem != "" {
		t.Fatal("AI.CompleteJSON was called, want skipped (not enough samples)")
	}
}

func TestAIJobVoiceProfileGeneratesUpsertsAndReenqueues(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	seedSentMessages(messages, "a1", "alex@example.com", 10)
	voice := newVoiceProfileRepo()
	jobs := newAiJobRepo()
	ai := newAI()
	ai.jsonOut = `{"profile":"- Signs off with 'Best, Alex'\n- Short sentences"}`
	ai.jsonModel = "gpt-voice"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newClock(now)

	svc := newVoiceJobService(accounts, messages, voice, jobs, ai, clock)

	j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile}
	if err := svc.runVoiceProfile(ctx, j); err != nil {
		t.Fatalf("runVoiceProfile() error = %v, want nil", err)
	}

	p, ok := voice.byUser["u1"]
	if !ok {
		t.Fatal("VoiceProfiles.Upsert was not called")
	}
	if p.Profile == "" {
		t.Fatal("upserted profile.Profile is empty")
	}
	if p.SampleCount != 10 {
		t.Fatalf("upserted profile.SampleCount = %d, want 10", p.SampleCount)
	}
	if p.Model != "gpt-voice" {
		t.Fatalf("upserted profile.Model = %q, want gpt-voice", p.Model)
	}
	if !p.UpdatedAt.Equal(now) {
		t.Fatalf("upserted profile.UpdatedAt = %v, want %v", p.UpdatedAt, now)
	}

	if len(jobs.queue) != 1 {
		t.Fatalf("jobs.queue = %v, want exactly 1 self re-enqueue", jobs.queue)
	}
	re := jobs.queue[0]
	if re.Kind != domain.AiJobVoiceProfile || re.UserID != "u1" || re.AccountID != "a1" || re.ThreadID != nil {
		t.Fatalf("re-enqueued job = %+v, want voice_profile for u1/a1 with nil ThreadID", re)
	}
	wantRunAfter := now.Add(30 * 24 * time.Hour)
	if !re.RunAfter.Equal(wantRunAfter) {
		t.Fatalf("re-enqueued RunAfter = %v, want %v", re.RunAfter, wantRunAfter)
	}
	if re.ID == j.ID {
		t.Fatal("re-enqueued job reused the original job's ID, want a fresh ID")
	}
}

// A re-run against an account that already has a profile (e.g. a duplicate
// job, since voice_profile jobs fall outside the thread-scoped dedup index)
// must not error or duplicate: Upsert overwrites the single per-user row.
func TestAIJobVoiceProfileRerunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	seedSentMessages(messages, "a1", "alex@example.com", 10)
	voice := newVoiceProfileRepo()
	voice.byUser["u1"] = domain.VoiceProfile{UserID: "u1", Profile: "- old profile", SampleCount: 3, Model: "old-model"}
	jobs := newAiJobRepo()
	ai := newAI()
	ai.jsonOut = `{"profile":"- new profile"}`
	ai.jsonModel = "gpt-voice-2"

	svc := newVoiceJobService(accounts, messages, voice, jobs, ai, newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile}
	if err := svc.runVoiceProfile(ctx, j); err != nil {
		t.Fatalf("runVoiceProfile() error = %v, want nil", err)
	}
	if len(voice.byUser) != 1 {
		t.Fatalf("voice.byUser has %d entries, want exactly 1 (overwrite, not duplicate)", len(voice.byUser))
	}
	if got := voice.byUser["u1"].Profile; got != "- new profile" {
		t.Fatalf("profile = %q, want overwritten to '- new profile'", got)
	}
}

func TestAIJobVoiceProfileAccountNotFoundDropsJob(t *testing.T) {
	ctx := context.Background()
	svc := newVoiceJobService(newAccountRepo(), newMessageRepo(), newVoiceProfileRepo(), newAiJobRepo(), newAI(), newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "missing", Kind: domain.AiJobVoiceProfile}
	err := svc.runVoiceProfile(ctx, j)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (runJob drops the job)", err)
	}
}

func TestAIJobVoiceProfileAIErrorPropagatesRaw(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	seedSentMessages(messages, "a1", "alex@example.com", 10)
	voice := newVoiceProfileRepo()
	ai := newAI()
	aiErr := domain.ErrAIUnavailable
	ai.jsonErr = aiErr

	svc := newVoiceJobService(accounts, messages, voice, newAiJobRepo(), ai, newClock(time.Now()))

	j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile}
	err := svc.runVoiceProfile(ctx, j)
	if !errors.Is(err, aiErr) {
		t.Fatalf("err = %v, want to wrap %v", err, aiErr)
	}
	if _, ok := voice.byUser["u1"]; ok {
		t.Fatal("VoiceProfiles.Upsert was called despite AI error")
	}
}

// End-to-end through ProcessDueAiJobs/runJob: confirms the original job is
// completed (deleted) and exactly one fresh job lands in the queue.
func TestAIJobVoiceProfileEndToEndViaProcessDueAiJobs(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "alex@example.com"}); err != nil {
		t.Fatal(err)
	}
	messages := newMessageRepo()
	seedSentMessages(messages, "a1", "alex@example.com", 10)
	voice := newVoiceProfileRepo()
	jobs := newAiJobRepo()
	jobs.queue = []domain.AiJob{{ID: "j1", UserID: "u1", AccountID: "a1", Kind: domain.AiJobVoiceProfile}}
	ai := newAI()
	ai.jsonOut = `{"profile":"- terse"}`

	svc := newVoiceJobService(accounts, messages, voice, jobs, ai, newClock(time.Now()))

	if err := svc.ProcessDueAiJobs(ctx); err != nil {
		t.Fatalf("ProcessDueAiJobs() error = %v, want nil", err)
	}
	if len(jobs.completed) != 1 || jobs.completed[0] != "j1" {
		t.Fatalf("completed = %v, want [j1]", jobs.completed)
	}
	if len(jobs.queue) != 1 {
		t.Fatalf("jobs.queue = %v, want exactly 1 fresh re-enqueued job", jobs.queue)
	}
	if len(jobs.failed) != 0 {
		t.Fatalf("failed = %v, want none", jobs.failed)
	}
}
