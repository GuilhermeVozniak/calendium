package service

import (
	"context"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// probeThreads / probeAccounts count the handlers' guard fetches, so the
// table below can tell "skipped before dispatch" from "dispatched and then
// dropped": every handler's first read is one of these two.
type probeThreads struct {
	port.ThreadRepo
	gets int
}

func (p *probeThreads) GetByID(ctx context.Context, id string) (domain.Thread, error) {
	p.gets++
	return p.ThreadRepo.GetByID(ctx, id)
}

type probeAccounts struct {
	port.AccountRepo
	gets int
}

func (p *probeAccounts) GetByID(ctx context.Context, id string) (domain.ConnectedAccount, error) {
	p.gets++
	return p.AccountRepo.GetByID(ctx, id)
}

// I-1: every queued AI job kind is background work (all are enqueued by sync
// or by a job re-queueing itself, never by a direct user action; on-demand AI
// goes through AIService). With Settings → AI → "Background AI processing"
// off, the runner completes each one as skipped without reaching its
// handler (so no model call, no budget, no re-queue). With the switch on,
// every kind is dispatched as before.
func TestProcessDueAiJobsGatesEveryBackgroundKind(t *testing.T) {
	if len(domain.AiJobKinds) != 6 {
		t.Fatalf("domain.AiJobKinds = %v: a new kind must be classified in isBackgroundAiJob and added here", domain.AiJobKinds)
	}
	for _, kind := range domain.AiJobKinds {
		for _, on := range []bool{false, true} {
			name := string(kind) + "/off"
			if on {
				name = string(kind) + "/on"
			}
			t.Run(name, func(t *testing.T) {
				if !isBackgroundAiJob(kind) {
					t.Fatalf("%s is not classified as background; every queued kind is", kind)
				}
				ctx := context.Background()
				settings := newUserSettingsRepo()
				if err := settings.SetAIBackground(ctx, "u1", on); err != nil {
					t.Fatal(err)
				}
				threads := &probeThreads{ThreadRepo: newThreadRepo()}
				accounts := &probeAccounts{AccountRepo: newAccountRepo()}
				jobs := newAiJobRepo()
				j := domain.AiJob{ID: "j1", UserID: "u1", AccountID: "a1", Kind: kind, Attempts: 1}
				if kind != domain.AiJobVoiceProfile {
					j.ThreadID = strPtr("t1")
				}
				jobs.queue = []domain.AiJob{j}
				usage := newAiUsageRepo()
				ai := newAI()
				svc := NewAIJobService(AIJobServiceDeps{
					Jobs: jobs, Usage: usage, Threads: threads, Accounts: accounts,
					Messages: newMessageRepo(), VoiceProfiles: newVoiceProfileRepo(),
					UserSettings: settings, AI: ai, Clock: newClock(time.Now()), DailyLimit: 10,
				})

				if err := svc.ProcessDueAiJobs(ctx); err != nil {
					t.Fatalf("ProcessDueAiJobs() = %v", err)
				}
				if len(jobs.completed) != 1 || jobs.completed[0] != "j1" || len(jobs.failed) != 0 {
					t.Fatalf("completed=%v failed=%v, want j1 completed", jobs.completed, jobs.failed)
				}
				reached := threads.gets + accounts.gets
				if !on {
					if reached != 0 || len(usage.calls) != 0 || ai.lastSystem != "" || len(jobs.queue) != 0 {
						t.Fatalf("switch off: handler reads=%d usage=%v ai=%q queue=%v, want the job skipped untouched",
							reached, usage.calls, ai.lastSystem, jobs.queue)
					}
					return
				}
				if reached == 0 {
					t.Fatal("switch on: the handler was never reached")
				}
			})
		}
	}
}
