package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

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
		AI:            ai,
		Clock:         newClock(time.Now()),
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

func mustUpsert(t *testing.T, messages *fakeMessageRepo, m domain.Message) {
	t.Helper()
	if _, err := messages.Upsert(context.Background(), m); err != nil {
		t.Fatalf("Upsert(%q): %v", m.ID, err)
	}
}
