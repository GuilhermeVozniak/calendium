package service

// ai_test.go covers AIService.Compose's personal-voice injection (Task 11):
// compose/reply carry the learned VoiceProfile in the system prompt when one
// exists; summarize/ask/editing actions never do; a missing profile (no
// VoiceProfiles repo wired, or none learned yet) leaves the prompt
// unchanged either way.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func newTestAIService(ai *fakeAI, voice *fakeVoiceProfileRepo) *AIService {
	return NewAIService(AIServiceDeps{
		Accounts:      newAccountRepo(),
		Threads:       newThreadRepo(),
		Messages:      newMessageRepo(),
		Drafts:        newDraftRepo(newAccountRepo()),
		VoiceProfiles: voice,
		Usage:         newAiUsageRepo(),
		AI:            ai,
		Clock:         newClock(time.Now()),
		SelfHosted:    true, // bypass the subscription gate; not what this file tests
	})
}

func TestAIServiceComposeInjectsVoiceProfileForComposeAndReply(t *testing.T) {
	for _, action := range []domain.AiAction{domain.AiCompose, domain.AiReply} {
		t.Run(string(action), func(t *testing.T) {
			ai := newAI()
			voice := newVoiceProfileRepo()
			voice.byUser["u1"] = domain.VoiceProfile{UserID: "u1", Profile: "- signs off with 'Best, Alex'"}
			svc := newTestAIService(ai, voice)

			_, err := svc.Compose(context.Background(), "u1", domain.AiComposeRequest{
				Action: action,
				Prompt: "let them know I'll be late",
			})
			if err != nil {
				t.Fatalf("Compose() error = %v, want nil", err)
			}
			if !strings.Contains(ai.lastSystem, "Best, Alex") {
				t.Fatalf("system prompt = %q, want it to carry the voice profile", ai.lastSystem)
			}
		})
	}
}

func TestAIServiceComposeNoInjectionForNonGenerativeActions(t *testing.T) {
	profile := "- signs off with 'Best, Alex'"
	tests := []struct {
		action domain.AiAction
		prompt string
		tone   string
	}{
		{domain.AiSummarize, "", ""},
		{domain.AiAsk, "what's the deadline?", ""},
		{domain.AiImprove, "improve this", ""},
		{domain.AiShorten, "shorten this", ""},
		{domain.AiSimplify, "simplify this", ""},
		{domain.AiFixGrammar, "fix this", ""},
		{domain.AiChangeTone, "make it warmer", "warmer"},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			ai := newAI()
			voice := newVoiceProfileRepo()
			voice.byUser["u1"] = domain.VoiceProfile{UserID: "u1", Profile: profile}
			svc := newTestAIService(ai, voice)

			_, err := svc.Compose(context.Background(), "u1", domain.AiComposeRequest{
				Action: tt.action,
				Prompt: tt.prompt,
				Tone:   tt.tone,
			})
			if err != nil {
				t.Fatalf("Compose() error = %v, want nil", err)
			}
			if strings.Contains(ai.lastSystem, profile) {
				t.Fatalf("action %s: system prompt = %q, want it to NOT carry the voice profile", tt.action, ai.lastSystem)
			}
			if ai.lastSystem != systemPromptFor(domain.AiComposeRequest{Action: tt.action, Tone: tt.tone}) {
				t.Fatalf("action %s: system prompt = %q, want unchanged base prompt %q", tt.action, ai.lastSystem, systemPromptFor(domain.AiComposeRequest{Action: tt.action, Tone: tt.tone}))
			}
		})
	}
}

func TestAIServiceComposeMissingProfileLeavesPromptUnchanged(t *testing.T) {
	ai := newAI()
	voice := newVoiceProfileRepo() // no profile learned yet -> Get returns ErrNotFound
	svc := newTestAIService(ai, voice)

	_, err := svc.Compose(context.Background(), "u1", domain.AiComposeRequest{
		Action: domain.AiCompose,
		Prompt: "draft a follow-up",
	})
	if err != nil {
		t.Fatalf("Compose() error = %v, want nil", err)
	}
	if ai.lastSystem != systemPromptFor(domain.AiComposeRequest{Action: domain.AiCompose}) {
		t.Fatalf("system prompt = %q, want unchanged base prompt %q", ai.lastSystem, systemPromptFor(domain.AiComposeRequest{Action: domain.AiCompose}))
	}
}

func TestAIServiceComposeNilVoiceProfilesRepoSkipsInjection(t *testing.T) {
	ai := newAI()
	svc := NewAIService(AIServiceDeps{
		Accounts:   newAccountRepo(),
		Threads:    newThreadRepo(),
		Messages:   newMessageRepo(),
		Drafts:     newDraftRepo(newAccountRepo()),
		Usage:      newAiUsageRepo(),
		AI:         ai,
		Clock:      newClock(time.Now()),
		SelfHosted: true,
		// VoiceProfiles intentionally left nil.
	})

	_, err := svc.Compose(context.Background(), "u1", domain.AiComposeRequest{
		Action: domain.AiCompose,
		Prompt: "draft a follow-up",
	})
	if err != nil {
		t.Fatalf("Compose() error = %v, want nil", err)
	}
	if ai.lastSystem != systemPromptFor(domain.AiComposeRequest{Action: domain.AiCompose}) {
		t.Fatalf("system prompt = %q, want unchanged base prompt %q", ai.lastSystem, systemPromptFor(domain.AiComposeRequest{Action: domain.AiCompose}))
	}
}

// --- AIService.Ask ------------------------------------------------------------
//
// Ask retrieves candidate messages (thread-scoped or via Threads.Search),
// renders them as [msg:<id>] tagged context, asks the model, then validates
// every cited id against the candidates it actually fetched before mapping
// survivors to domain.AiSource. These tests pin that contract per
// docs/../task-13-brief.md.

func newAskTestService(accounts *fakeAccountRepo, threads *fakeThreadRepo, messages *fakeMessageRepo, ai *fakeAI) *AIService {
	return NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Usage:         newAiUsageRepo(),
		AI:            ai,
		Clock:         newClock(time.Now()),
		DailyLimit:    100,
		SelfHosted:    true,
	})
}

func TestAIAskThreadScopedIncludesOnlyThatThread(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}

	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Target thread"}
	threads.byID["t2"] = domain.Thread{ID: "t2", AccountID: "a1", Subject: "Other thread"}

	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "a@x.com"},
		BodyText: "In scope message.", SentAt: time.Now(),
	})
	mustUpsert(t, messages, domain.Message{
		ID: "m2", ThreadID: "t2", From: domain.EmailAddress{Email: "b@x.com"},
		BodyText: "Out of scope message.", SentAt: time.Now(),
	})

	ai := newAI()
	ai.jsonOut = `{"answer":"yes","sourceMessageIds":["m1"]}`
	ai.jsonModel = "openrouter/auto"

	svc := newAskTestService(accounts, threads, messages, ai)
	resp, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "what's up?", ThreadID: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ai.lastUser, "[msg:m1]") {
		t.Fatalf("prompt missing target thread message: %q", ai.lastUser)
	}
	if strings.Contains(ai.lastUser, "[msg:m2]") || strings.Contains(ai.lastUser, "Out of scope") {
		t.Fatalf("prompt leaked message from a different thread: %q", ai.lastUser)
	}
	if threads.searchGotQuery != "" {
		t.Fatalf("thread-scoped ask should not call Threads.Search, got query %q", threads.searchGotQuery)
	}
	if len(resp.Sources) != 1 || resp.Sources[0].MessageID != "m1" || resp.Sources[0].ThreadID != "t1" {
		t.Fatalf("sources = %+v, want one source citing m1/t1", resp.Sources)
	}
}

func TestAIAskMailboxWideCallsThreadsSearch(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}

	threads := newThreadRepo()
	hit := domain.Thread{ID: "t9", AccountID: "a1", Subject: "Q3 budget"}
	threads.byID["t9"] = hit
	threads.searchResult = []domain.Thread{hit}

	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m9", ThreadID: "t9", From: domain.EmailAddress{Email: "cfo@x.com"},
		BodyText: "Budget is approved.", SentAt: time.Now(),
	})

	ai := newAI()
	ai.jsonOut = `{"answer":"approved","sourceMessageIds":["m9"]}`
	ai.jsonModel = "m"

	svc := newAskTestService(accounts, threads, messages, ai)
	const question = "was the budget approved?"
	if _, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: question}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if threads.searchGotUserID != owner || threads.searchGotQuery != question {
		t.Fatalf("Threads.Search(userID=%q, query=%q), want (%q, %q)", threads.searchGotUserID, threads.searchGotQuery, owner, question)
	}
	if !strings.Contains(ai.lastUser, "[msg:m9]") {
		t.Fatalf("prompt missing search-hit message: %q", ai.lastUser)
	}
}

func TestAIAskDropsHallucinatedCitation(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Real thread"}
	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "a@x.com"},
		BodyText: "Real message.", SentAt: time.Now(),
	})

	ai := newAI()
	// The model cites a real id (m1) plus one it invented (m-does-not-exist).
	ai.jsonOut = `{"answer":"because reasons","sourceMessageIds":["m1","m-does-not-exist"]}`
	ai.jsonModel = "m"

	svc := newAskTestService(accounts, threads, messages, ai)
	resp, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "why?", ThreadID: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("sources = %+v, want exactly the one real citation (hallucination dropped)", resp.Sources)
	}
	if resp.Sources[0].MessageID != "m1" {
		t.Fatalf("sources[0].MessageID = %q, want m1", resp.Sources[0].MessageID)
	}
}

func TestAIAskSourcesCarrySubjectAndSnippet(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Travel plans"}
	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "a@x.com"},
		BodyText: "Flight leaves at 9am on Friday from gate 12.", SentAt: time.Now(),
	})

	ai := newAI()
	ai.jsonOut = `{"answer":"9am Friday","sourceMessageIds":["m1"]}`
	ai.jsonModel = "m"

	svc := newAskTestService(accounts, threads, messages, ai)
	resp, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "when's the flight?", ThreadID: "t1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Sources) != 1 {
		t.Fatalf("sources = %+v, want 1", resp.Sources)
	}
	src := resp.Sources[0]
	if src.ThreadID != "t1" || src.Subject != "Travel plans" {
		t.Fatalf("source = %+v, want threadId=t1 subject=%q", src, "Travel plans")
	}
	if src.Snippet != "Flight leaves at 9am on Friday from gate 12." {
		t.Fatalf("snippet = %q, want the (short, untruncated) body", src.Snippet)
	}
}

// TestAIAskNoCandidatesSkipsChargeAndAI covers MINOR 5 (final-review fix
// wave): a mailbox-wide question with no matching threads/messages must not
// charge the daily budget or call the model with an empty context -- it
// returns an honest "no sources" answer instead.
func TestAIAskNoCandidatesSkipsChargeAndAI(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	threads := newThreadRepo() // Threads.Search returns no hits
	messages := newMessageRepo()
	ai := newAI()
	ai.jsonOut = `{"answer":"should not be reached","sourceMessageIds":[]}`
	usage := newAiUsageRepo()

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Usage:         usage,
		AI:            ai,
		Clock:         newClock(time.Now()),
		DailyLimit:    10,
		SelfHosted:    true,
	})

	resp, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "anything about the moon landing?"})
	if err != nil {
		t.Fatalf("Ask() error = %v, want nil", err)
	}
	if resp.Answer != "I couldn't find anything in your mail about that." {
		t.Fatalf("Answer = %q, want the honest no-sources message", resp.Answer)
	}
	if len(resp.Sources) != 0 {
		t.Fatalf("Sources = %+v, want empty", resp.Sources)
	}
	if usage.calls[owner] != 0 {
		t.Fatalf("usage.calls[owner] = %d, want 0 (no charge with no candidates)", usage.calls[owner])
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q, lastUser=%q), want it skipped with no candidates", ai.lastSystem, ai.lastUser)
	}
}

func TestAIAskEmptyQuestionIsValidationError(t *testing.T) {
	accounts := newAccountRepo()
	threads := newThreadRepo()
	messages := newMessageRepo()
	ai := newAI()

	svc := newAskTestService(accounts, threads, messages, ai)
	_, err := svc.Ask(context.Background(), "u1", domain.AiAskRequest{Question: "   "})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called for an empty question, want it short-circuited")
	}
}

func TestAIAskNilAIIsUnavailable(t *testing.T) {
	accounts := newAccountRepo()
	threads := newThreadRepo()
	messages := newMessageRepo()

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		AI:            nil,
		Clock:         newClock(time.Now()),
		SelfHosted:    true,
	})
	_, err := svc.Ask(context.Background(), "u1", domain.AiAskRequest{Question: "hello?"})
	if !errors.Is(err, domain.ErrAIUnavailable) {
		t.Fatalf("err = %v, want ErrAIUnavailable", err)
	}
}

// TestAIAskBudgetExhaustedBlocksAndSkipsAI covers the daily AI budget gate on
// Ask (mirroring InstantReplies): once the shared per-user counter is at the
// limit, Ask must fail with domain.ErrRateLimited and never reach the model
// -- no CompleteJSON call, no wasted candidate work spent for nothing.
func TestAIAskBudgetExhaustedBlocksAndSkipsAI(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Thread"}
	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "a@x.com"},
		BodyText: "Body.", SentAt: time.Now(),
	})

	ai := newAI()
	ai.jsonOut = `{"answer":"should not be reached","sourceMessageIds":[]}`
	usage := newAiUsageRepo()
	usage.calls[owner] = 1 // already at the limit

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Usage:         usage,
		AI:            ai,
		Clock:         newClock(time.Now()),
		DailyLimit:    1,
		SelfHosted:    true,
	})

	_, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "what's up?", ThreadID: "t1"})
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want domain.ErrRateLimited", err)
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q, lastUser=%q), want budget exhaustion to skip generation entirely", ai.lastSystem, ai.lastUser)
	}
}

// TestAIAskBudgetChargedOnceOnSuccess covers the charge side of the same
// gate: a single successful Ask call increments the shared daily counter
// exactly once.
func TestAIAskBudgetChargedOnceOnSuccess(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Thread"}
	messages := newMessageRepo()
	mustUpsert(t, messages, domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "a@x.com"},
		BodyText: "Body.", SentAt: time.Now(),
	})

	ai := newAI()
	ai.jsonOut = `{"answer":"yes","sourceMessageIds":["m1"]}`
	usage := newAiUsageRepo()

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Usage:         usage,
		AI:            ai,
		Clock:         newClock(time.Now()),
		DailyLimit:    10,
		SelfHosted:    true,
	})

	if _, err := svc.Ask(context.Background(), owner, domain.AiAskRequest{Question: "what's up?", ThreadID: "t1"}); err != nil {
		t.Fatalf("Ask() error = %v, want nil", err)
	}
	if usage.calls[owner] != 1 {
		t.Fatalf("usage.calls[owner] = %d, want 1 (charged once on success)", usage.calls[owner])
	}
}

func mustUpsert(t *testing.T, messages *fakeMessageRepo, m domain.Message) {
	t.Helper()
	if _, err := messages.Upsert(context.Background(), m); err != nil {
		t.Fatalf("Upsert(%q): %v", m.ID, err)
	}
}

// aiProposeFixture wires an AIService (paywall bypassed via SelfHosted) plus
// its collaborator fakes for ProposeEvent tests.
type aiProposeFixture struct {
	svc      *AIService
	threads  *fakeThreadRepo
	accounts *fakeAccountRepo
	messages *fakeMessageRepo
	ai       *fakeAI
	cal      *fakeCalendarService
	clock    *fakeClock
}

func newAIProposeFixture(t *testing.T) *aiProposeFixture {
	t.Helper()
	base := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)
	accounts := newAccountRepo()
	threads := newThreadRepo()
	messages := newMessageRepo()
	ai := newAI()
	cal := newCalendarService()

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Calendar:      cal,
		Usage:         newAiUsageRepo(),
		AI:            ai,
		Clock:         clock,
		SelfHosted:    true, // bypass the paywall; entitlement tested elsewhere
	})
	return &aiProposeFixture{svc: svc, threads: threads, accounts: accounts, messages: messages, ai: ai, cal: cal, clock: clock}
}

// seedThread registers an owned thread with one message from a guest.
func (f *aiProposeFixture) seedThread(t *testing.T, participants ...string) {
	t.Helper()
	f.accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: "u1", Email: "owner@example.com"}
	var parts []domain.EmailAddress
	for _, p := range participants {
		parts = append(parts, domain.EmailAddress{Email: p})
	}
	f.threads.byID["t1"] = domain.Thread{
		ID: "t1", AccountID: "a1", Subject: "Sync next week", Participants: parts,
	}
	if _, err := f.messages.Upsert(context.Background(), domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "guest@example.com"},
		BodyText: "Can we meet?", SentAt: f.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAIProposeEventSnapsToOfferedSlot pins the happy path: the model's
// preferredSlot matches an offered AVAILABILITY slot verbatim, so the
// proposal uses that slot's Start/End, the clamped duration, and the
// title/location/notes straight from the model.
func TestAIProposeEventSnapsToOfferedSlot(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	slot2 := f.clock.Now().Add(48 * time.Hour)
	f.cal.slots = []domain.AvailabilitySlot{
		{Start: slot1, End: slot1.Add(30 * time.Minute)},
		{Start: slot2, End: slot2.Add(30 * time.Minute)},
	}

	f.ai.jsonOut = `{"title":"Sync call","attendeeEmails":["guest@example.com"],"durationMinutes":45,"preferredSlot":"` +
		slot2.Format(time.RFC3339) + `","location":"Zoom","notes":"agenda"}`
	f.ai.jsonModel = "gpt-test"

	got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("ProposeEvent() error = %v, want nil", err)
	}
	if !got.Start.Equal(slot2) {
		t.Fatalf("Start = %v, want offered slot2 %v", got.Start, slot2)
	}
	if !got.End.Equal(slot2.Add(45 * time.Minute)) {
		t.Fatalf("End = %v, want Start+45m", got.End)
	}
	if got.Title != "Sync call" {
		t.Fatalf("Title = %q, want %q", got.Title, "Sync call")
	}
	if got.Location != "Zoom" || got.Notes != "agenda" {
		t.Fatalf("Location/Notes = %q/%q, want Zoom/agenda", got.Location, got.Notes)
	}
	wantAttendees := []string{"owner@example.com", "guest@example.com"}
	if !reflect.DeepEqual(got.Attendees, wantAttendees) {
		t.Fatalf("Attendees = %v, want %v", got.Attendees, wantAttendees)
	}
	if f.cal.availCalls != 1 {
		t.Fatalf("Availability calls = %d, want 1", f.cal.availCalls)
	}
	wantTo := f.clock.Now().Add(7 * 24 * time.Hour)
	if !f.cal.lastAvailTo.Equal(wantTo) {
		t.Fatalf("Availability `to` = %v, want now+7d %v", f.cal.lastAvailTo, wantTo)
	}
}

// TestAIProposeEventInvalidSlotFallsBackToFirst covers both flavors of a bad
// preferredSlot -- unparseable text and a syntactically valid RFC3339
// timestamp that just isn't one of the offered slots -- falling back to the
// first offered slot rather than erroring (proposal honesty: we never invent
// a time the model didn't actually validate against real availability).
func TestAIProposeEventInvalidSlotFallsBackToFirst(t *testing.T) {
	tests := []struct {
		name          string
		preferredSlot string
	}{
		{"unparseable text", "next Tuesday afternoon"},
		{"valid RFC3339 but not an offered slot", "2099-01-01T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAIProposeFixture(t)
			f.seedThread(t, "owner@example.com", "guest@example.com")

			slot1 := f.clock.Now().Add(24 * time.Hour)
			f.cal.slots = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
			f.ai.jsonOut = `{"title":"Sync","attendeeEmails":[],"durationMinutes":30,"preferredSlot":"` + tt.preferredSlot + `"}`

			got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
			if err != nil {
				t.Fatalf("ProposeEvent() error = %v, want nil", err)
			}
			if !got.Start.Equal(slot1) {
				t.Fatalf("Start = %v, want fallback to first offered slot %v", got.Start, slot1)
			}
		})
	}
}

// TestAIProposeEventDropsAttendeeNotInThread covers the honesty rule for
// attendees: an email the model proposes that never appeared as a thread
// participant is dropped, while the owning account is always included.
func TestAIProposeEventDropsAttendeeNotInThread(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	f.cal.slots = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
	f.ai.jsonOut = `{"title":"Sync","attendeeEmails":["guest@example.com","stranger@example.com"],"durationMinutes":30,"preferredSlot":"` +
		slot1.Format(time.RFC3339) + `"}`

	got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if err != nil {
		t.Fatalf("ProposeEvent() error = %v, want nil", err)
	}
	wantAttendees := []string{"owner@example.com", "guest@example.com"}
	if !reflect.DeepEqual(got.Attendees, wantAttendees) {
		t.Fatalf("Attendees = %v, want %v (stranger@example.com dropped)", got.Attendees, wantAttendees)
	}
}

// TestAIProposeEventBudgetExhausted covers FIX 3 (final-review fix wave):
// ProposeEvent shares the same daily AI budget as Ask/InstantReplies/Compose,
// so an exhausted counter must block it with domain.ErrRateLimited once real
// availability is confirmed, never reaching port.AI.CompleteJSON.
func TestAIProposeEventBudgetExhausted(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	clock := newClock(base)
	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner, Email: "owner@example.com"}
	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{
		ID: "t1", AccountID: "a1", Subject: "Sync next week",
		Participants: []domain.EmailAddress{{Email: "owner@example.com"}, {Email: "guest@example.com"}},
	}
	messages := newMessageRepo()
	if _, err := messages.Upsert(context.Background(), domain.Message{
		ID: "m1", ThreadID: "t1", From: domain.EmailAddress{Email: "guest@example.com"},
		BodyText: "Can we meet?", SentAt: clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	cal := newCalendarService()
	slot1 := clock.Now().Add(24 * time.Hour)
	cal.slots = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}

	ai := newAI()
	ai.jsonOut = `{"title":"should not be reached","attendeeEmails":[],"durationMinutes":30,"preferredSlot":""}`
	usage := newAiUsageRepo()
	usage.calls[owner] = 1 // already at the limit

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        newDraftRepo(accounts),
		Calendar:      cal,
		Usage:         usage,
		AI:            ai,
		Clock:         clock,
		DailyLimit:    1,
		SelfHosted:    true,
	})

	_, err := svc.ProposeEvent(context.Background(), owner, "t1")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want domain.ErrRateLimited", err)
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q, lastUser=%q), want budget exhaustion to skip generation entirely", ai.lastSystem, ai.lastUser)
	}
}

// TestAIProposeEventNoAvailabilityIsConflict covers the "no free slots in the
// next 7 days" case: a validation-style domain.ErrConflict, not a 500 and not
// a silently invented time.
func TestAIProposeEventNoAvailabilityIsConflict(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")
	f.cal.slots = nil // no free slots

	_, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want wrapping domain.ErrConflict", err)
	}
	if err.Error() != "conflict: no free slots" {
		t.Fatalf("err message = %q, want %q", err.Error(), "conflict: no free slots")
	}
	if f.ai.lastSystem != "" {
		t.Fatalf("AI was invoked (lastSystem=%q), want it skipped when there's nothing to propose against", f.ai.lastSystem)
	}
}

// TestAIProposeEventMalformedModelOutputIsRejected is the proposal-honesty
// regression test: when the model's JSON doesn't decode into
// eventProposalOut, ProposeEvent must reject it with domain.ErrAIOutput
// (returned raw, per the LLM error-mapping contract) rather than passing any
// partial/garbage output through as a proposal.
func TestAIProposeEventMalformedModelOutputIsRejected(t *testing.T) {
	f := newAIProposeFixture(t)
	f.seedThread(t, "owner@example.com", "guest@example.com")

	slot1 := f.clock.Now().Add(24 * time.Hour)
	f.cal.slots = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
	f.ai.jsonOut = `not json` // fakeAI.CompleteJSON wraps the decode failure in domain.ErrAIOutput

	_, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
	if !errors.Is(err, domain.ErrAIOutput) {
		t.Fatalf("err = %v, want wrapping domain.ErrAIOutput", err)
	}
}

// TestAIProposeEventClampsDuration covers the [15, 480]-minute clamp
// (default 30 when the model omits it) so a bogus/absent durationMinutes
// never produces a degenerate or unbounded event.
func TestAIProposeEventClampsDuration(t *testing.T) {
	tests := []struct {
		name        string
		modelMins   int
		wantMinutes time.Duration
	}{
		{"unset defaults to 30", 0, 30 * time.Minute},
		{"below floor clamps to 15", 5, 15 * time.Minute},
		{"above ceiling clamps to 480", 1000, 480 * time.Minute},
		{"within range passes through", 60, 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAIProposeFixture(t)
			f.seedThread(t, "owner@example.com", "guest@example.com")

			slot1 := f.clock.Now().Add(24 * time.Hour)
			f.cal.slots = []domain.AvailabilitySlot{{Start: slot1, End: slot1.Add(30 * time.Minute)}}
			f.ai.jsonOut = fmt.Sprintf(`{"title":"Sync","attendeeEmails":[],"durationMinutes":%d,"preferredSlot":%q}`,
				tt.modelMins, slot1.Format(time.RFC3339))

			got, err := f.svc.ProposeEvent(context.Background(), "u1", "t1")
			if err != nil {
				t.Fatalf("ProposeEvent() error = %v, want nil", err)
			}
			if got.End.Sub(got.Start) != tt.wantMinutes {
				t.Fatalf("duration = %v, want %v", got.End.Sub(got.Start), tt.wantMinutes)
			}
		})
	}
}
