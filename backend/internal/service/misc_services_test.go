package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- AIService.Compose -------------------------------------------------------

// TestAIServiceComposeAssemblesPrompt pins the exact system+user prompt handed
// to port.AI.Complete when a thread, a draft, and an instruction are all
// present, and that the response echoes the gateway's text+model. Read ai.go:
// the user prompt is "Conversation subject: <s>\n\n" + per-message
// "From <email> at <RFC3339>:\n<body>\n\n" (newest aiContextMessages, BodyText
// preferred over BodyHTML) + `Current draft (subject %q):\n<html>\n\n` +
// "Instruction: <prompt>", then strings.TrimSpace.
func TestAIServiceComposeAssemblesPrompt(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	sentAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	accounts := newAccountRepo()
	accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}

	threads := newThreadRepo()
	threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Q3 planning"}

	messages := newMessageRepo()
	if _, err := messages.Upsert(context.Background(), domain.Message{
		ID:       "m1",
		ThreadID: "t1",
		From:     domain.EmailAddress{Email: "boss@acme.com"},
		BodyText: "Let's sync Thursday.",
		SentAt:   sentAt,
	}); err != nil {
		t.Fatal(err)
	}

	drafts := newDraftRepo(accounts)
	drafts.byID["d1"] = domain.Draft{ID: "d1", AccountID: "a1", Subject: "Re: Q3 planning", BodyHTML: "<p>Sure</p>"}

	ai := newAI()
	ai.text = "Generated reply"
	ai.model = "openrouter/auto"

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       threads,
		Messages:      messages,
		Drafts:        drafts,
		Usage:         newAiUsageRepo(),
		AI:            ai,
		Clock:         newClock(base),
		SelfHosted:    true, // bypass the paywall; entitlement tested separately
	})

	resp, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{
		Action:   domain.AiCompose,
		Prompt:   "make it warmer",
		ThreadID: "t1",
		DraftID:  "d1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Generated reply" || resp.Model != "openrouter/auto" {
		t.Fatalf("response = %+v, want Text/Model from the AI gateway", resp)
	}
	wantPrompt := domain.AiComposeRequest{Action: domain.AiCompose}
	if ai.lastSystem != systemPromptFor(wantPrompt) {
		t.Fatalf("system prompt = %q, want %q", ai.lastSystem, systemPromptFor(wantPrompt))
	}
	wantUser := "Conversation subject: Q3 planning\n\n" +
		"From boss@acme.com at 2026-01-02T03:04:05Z:\nLet's sync Thursday.\n\n" +
		"Current draft (subject \"Re: Q3 planning\"):\n<p>Sure</p>\n\n" +
		"Instruction: make it warmer"
	if ai.lastUser != wantUser {
		t.Fatalf("user prompt =\n%q\nwant\n%q", ai.lastUser, wantUser)
	}
}

// TestAIServiceComposeValidation covers ordering + sentinel paths in Compose:
// entitlement is checked BEFORE ParseAiAction, ParseAiAction before the
// prompt-required rule, and the prompt-required rule is skipped for AiSummarize.
func TestAIServiceComposeValidation(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	newSvc := func(selfHosted, entitled bool) (*AIService, *fakeAI) {
		accounts := newAccountRepo()
		accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
		accounts.byID["a2"] = domain.ConnectedAccount{ID: "a2", UserID: "someone-else"}
		threads := newThreadRepo()
		threads.byID["t1"] = domain.Thread{ID: "t1", AccountID: "a1", Subject: "Sub"}
		threads.byID["t2"] = domain.Thread{ID: "t2", AccountID: "a2", Subject: "Foreign"}
		subs := newSubscriptionRepo()
		if entitled {
			if err := subs.Upsert(context.Background(), domain.Subscription{UserID: owner, Status: domain.SubscriptionActive}); err != nil {
				t.Fatal(err)
			}
		}
		ai := newAI()
		ai.text, ai.model = "ok", "m"
		svc := NewAIService(AIServiceDeps{
			Subscriptions: subs,
			Accounts:      accounts,
			Threads:       threads,
			Messages:      newMessageRepo(),
			Drafts:        newDraftRepo(accounts),
			Usage:         newAiUsageRepo(),
			AI:            ai,
			Clock:         newClock(base),
			SelfHosted:    selfHosted,
		})
		return svc, ai
	}

	tests := []struct {
		name       string
		selfHosted bool
		entitled   bool
		req        domain.AiComposeRequest
		wantErr    error // nil = success
		wantCalled bool  // whether port.AI.Complete should be invoked
	}{
		{"unknown action", true, false, domain.AiComposeRequest{Action: "translate", Prompt: "hi"}, domain.ErrValidation, false},
		{"empty action", true, false, domain.AiComposeRequest{Action: "", Prompt: "hi"}, domain.ErrValidation, false},
		{"compose without prompt", true, false, domain.AiComposeRequest{Action: domain.AiCompose}, domain.ErrValidation, false},
		{"reply without prompt", true, false, domain.AiComposeRequest{Action: domain.AiReply}, domain.ErrValidation, false},
		{"ask without prompt", true, false, domain.AiComposeRequest{Action: domain.AiAsk}, domain.ErrValidation, false},
		{"summarize without prompt is allowed", true, false, domain.AiComposeRequest{Action: domain.AiSummarize, ThreadID: "t1"}, nil, true},
		{"foreign thread is not found", true, false, domain.AiComposeRequest{Action: domain.AiAsk, Prompt: "q", ThreadID: "t2"}, domain.ErrNotFound, false},
		{"paywall wins over invalid action", false, false, domain.AiComposeRequest{Action: "", Prompt: ""}, domain.ErrPaymentRequired, false},
		{"entitled via active subscription", false, true, domain.AiComposeRequest{Action: domain.AiCompose, Prompt: "write it"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, ai := newSvc(tt.selfHosted, tt.entitled)
			_, err := svc.Compose(context.Background(), owner, tt.req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// fakeAI has no call counter; each subtest gets a fresh fakeAI
			// (lastSystem zero-valued to ""), and systemPromptFor always
			// returns a non-empty string, so lastSystem != "" is equivalent
			// to "Complete was invoked".
			if got := ai.lastSystem != ""; got != tt.wantCalled {
				t.Fatalf("AI invoked = %v, want %v", got, tt.wantCalled)
			}
		})
	}
}

// TestAIServiceComposeBudgetExhausted covers FIX 3 (final-review fix wave):
// Compose shares the same daily AI budget as Ask/InstantReplies, so an
// exhausted counter must block Compose with domain.ErrRateLimited and never
// reach port.AI.Complete -- no wasted prompt assembly work turns into a
// wasted model call.
func TestAIServiceComposeBudgetExhausted(t *testing.T) {
	const owner = "u1"
	accounts := newAccountRepo()
	usage := newAiUsageRepo()
	usage.calls[owner] = 1 // already at the limit

	ai := newAI()
	ai.text, ai.model = "should not be reached", "m"

	svc := NewAIService(AIServiceDeps{
		Subscriptions: newSubscriptionRepo(),
		Accounts:      accounts,
		Threads:       newThreadRepo(),
		Messages:      newMessageRepo(),
		Drafts:        newDraftRepo(accounts),
		Usage:         usage,
		AI:            ai,
		Clock:         newClock(time.Now()),
		DailyLimit:    1,
		SelfHosted:    true,
	})

	_, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{
		Action: domain.AiCompose,
		Prompt: "draft a follow-up",
	})
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want domain.ErrRateLimited", err)
	}
	if ai.lastSystem != "" || ai.lastUser != "" {
		t.Fatalf("AI was called (lastSystem=%q, lastUser=%q), want budget exhaustion to skip generation entirely", ai.lastSystem, ai.lastUser)
	}
}

// TestSystemPromptForEditingActions pins the exact system prompt suffix for
// each of the five text-editing actions (Task 12), including change_tone's
// interpolation of req.Tone.
func TestSystemPromptForEditingActions(t *testing.T) {
	const base = "You are Calendium's email assistant. Be concise, warm, and professional. Return only the requested text with no preamble."
	tests := []struct {
		req  domain.AiComposeRequest
		want string
	}{
		{domain.AiComposeRequest{Action: domain.AiImprove}, base + " Rewrite the draft to be clearer and more compelling. Preserve meaning, links, and facts. Return only the rewritten body."},
		{domain.AiComposeRequest{Action: domain.AiShorten}, base + " Rewrite the draft in at most half the words. Preserve every commitment and question. Return only the rewritten body."},
		{domain.AiComposeRequest{Action: domain.AiSimplify}, base + " Rewrite the draft in plain, simple language. Return only the rewritten body."},
		{domain.AiComposeRequest{Action: domain.AiFixGrammar}, base + " Fix spelling, grammar, and punctuation only; change nothing else. Return only the corrected body."},
		{domain.AiComposeRequest{Action: domain.AiChangeTone, Tone: "warmer"}, base + " Rewrite the draft with this tone: warmer. Preserve meaning. Return only the rewritten body."},
	}
	for _, tt := range tests {
		t.Run(string(tt.req.Action), func(t *testing.T) {
			if got := systemPromptFor(tt.req); got != tt.want {
				t.Fatalf("systemPromptFor(%+v) = %q, want %q", tt.req, got, tt.want)
			}
		})
	}
}

// TestAIServiceComposeEditingActions covers the five text-editing actions
// (improve/shorten/simplify/fix_grammar/change_tone): DraftID-or-Prompt
// validation, change_tone's additional Tone requirement, and that each
// action reaches port.AI.Complete with the system prompt systemPromptFor
// documents for it.
func TestAIServiceComposeEditingActions(t *testing.T) {
	const owner = "u1"
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	newSvc := func() (*AIService, *fakeAI) {
		accounts := newAccountRepo()
		accounts.byID["a1"] = domain.ConnectedAccount{ID: "a1", UserID: owner}
		drafts := newDraftRepo(accounts)
		drafts.byID["d1"] = domain.Draft{ID: "d1", AccountID: "a1", Subject: "Re: hi", BodyHTML: "<p>hey there</p>"}
		subs := newSubscriptionRepo()
		if err := subs.Upsert(context.Background(), domain.Subscription{UserID: owner, Status: domain.SubscriptionActive}); err != nil {
			t.Fatal(err)
		}
		ai := newAI()
		ai.text, ai.model = "edited", "m"
		svc := NewAIService(AIServiceDeps{
			Subscriptions: subs,
			Accounts:      accounts,
			Threads:       newThreadRepo(),
			Messages:      newMessageRepo(),
			Drafts:        drafts,
			Usage:         newAiUsageRepo(),
			AI:            ai,
			Clock:         newClock(base),
		})
		return svc, ai
	}

	editingActions := []domain.AiAction{domain.AiImprove, domain.AiShorten, domain.AiSimplify, domain.AiFixGrammar, domain.AiChangeTone}

	t.Run("draftId alone satisfies non-tone editing actions", func(t *testing.T) {
		for _, action := range editingActions {
			if action == domain.AiChangeTone {
				continue // change_tone additionally needs Tone; covered below
			}
			svc, ai := newSvc()
			_, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{Action: action, DraftID: "d1"})
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", action, err)
			}
			want := systemPromptFor(domain.AiComposeRequest{Action: action})
			if ai.lastSystem != want {
				t.Fatalf("%s: system prompt = %q, want %q", action, ai.lastSystem, want)
			}
		}
	})

	t.Run("prompt alone (no draftId) satisfies non-tone editing actions", func(t *testing.T) {
		svc, _ := newSvc()
		_, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{Action: domain.AiShorten, Prompt: "the text to shorten"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("editing action without draftId or prompt is rejected", func(t *testing.T) {
		for _, action := range editingActions {
			svc, _ := newSvc()
			req := domain.AiComposeRequest{Action: action}
			if action == domain.AiChangeTone {
				req.Tone = "warmer" // Tone alone must not satisfy the missing-text requirement
			}
			_, err := svc.Compose(context.Background(), owner, req)
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("%s: err = %v, want ErrValidation", action, err)
			}
		}
	})

	t.Run("change_tone requires Tone even with a draftId", func(t *testing.T) {
		svc, _ := newSvc()
		_, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{Action: domain.AiChangeTone, DraftID: "d1"})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("change_tone succeeds with draftId and tone", func(t *testing.T) {
		svc, ai := newSvc()
		_, err := svc.Compose(context.Background(), owner, domain.AiComposeRequest{Action: domain.AiChangeTone, DraftID: "d1", Tone: "more formal"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := systemPromptFor(domain.AiComposeRequest{Action: domain.AiChangeTone, Tone: "more formal"})
		if ai.lastSystem != want {
			t.Fatalf("system prompt = %q, want %q", ai.lastSystem, want)
		}
		if !strings.Contains(ai.lastSystem, "more formal") {
			t.Fatalf("system prompt %q does not mention the requested tone", ai.lastSystem)
		}
	})
}

// --- DeviceService.Register --------------------------------------------------

// TestDeviceServiceRegister covers platform/token validation and the happy
// path: a new ID is minted, CreatedAt comes from the clock, and the row is
// persisted through DeviceRepo.Upsert. Registration is deliberately NOT
// paywalled, so no subscription is wired.
func TestDeviceServiceRegister(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		platform domain.DevicePlatform
		token    string
		wantErr  error // nil = success
	}{
		{"valid ios registration", domain.PlatformIOS, "apns-token-123", nil},
		{"valid web push registration", domain.PlatformWeb, `{"endpoint":"https://push.example/x"}`, nil},
		{"unknown platform", domain.DevicePlatform("blackberry"), "tok", domain.ErrValidation},
		{"empty platform", domain.DevicePlatform(""), "tok", domain.ErrValidation},
		{"empty token", domain.PlatformAndroid, "", domain.ErrValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devices := newDeviceRepo()
			svc := NewDeviceService(devices, newClock(base))

			got, err := svc.Register(context.Background(), "u1", tt.platform, tt.token)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if len(devices.byID) != 0 {
					t.Fatalf("device rows = %d on invalid input, want 0", len(devices.byID))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID == "" {
				t.Fatal("Register did not mint an ID")
			}
			if got.UserID != "u1" {
				t.Fatalf("UserID = %q, want u1", got.UserID)
			}
			if got.Platform != tt.platform {
				t.Fatalf("Platform = %q, want %q", got.Platform, tt.platform)
			}
			if got.Token != tt.token {
				t.Fatalf("Token = %q, want %q", got.Token, tt.token)
			}
			if !got.CreatedAt.Equal(base) {
				t.Fatalf("CreatedAt = %v, want %v (clock.Now)", got.CreatedAt, base)
			}
			// Persisted through Upsert and readable by the minted ID.
			stored, err := devices.GetByID(context.Background(), got.ID)
			if err != nil {
				t.Fatalf("device not persisted: %v", err)
			}
			if stored.Token != tt.token {
				t.Fatalf("stored token = %q, want %q", stored.Token, tt.token)
			}
		})
	}
}

// --- DeviceService.Unregister ------------------------------------------------

// TestDeviceServiceUnregister covers the ownership invariant: a foreign
// deviceID reports ErrNotFound (never ErrUnauthorized) and never reaches
// Delete, proven by devices.byID retaining the row.
func TestDeviceServiceUnregister(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	newSvc := func() (*DeviceService, *fakeDeviceRepo) {
		devices := newDeviceRepo()
		if _, err := devices.Upsert(context.Background(), domain.NotificationDevice{ID: "d1", UserID: "u1", Platform: domain.PlatformIOS, Token: "t"}); err != nil {
			t.Fatal(err)
		}
		return NewDeviceService(devices, newClock(base)), devices
	}

	t.Run("owner unregisters own device", func(t *testing.T) {
		svc, devices := newSvc()
		if err := svc.Unregister(context.Background(), "u1", "d1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := devices.byID["d1"]; ok {
			t.Fatal("device row still present after Unregister")
		}
		if _, err := devices.GetByID(context.Background(), "d1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
		}
	})

	t.Run("foreign device is not found, not unauthorized", func(t *testing.T) {
		svc, devices := newSvc()
		err := svc.Unregister(context.Background(), "intruder", "d1")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if errors.Is(err, domain.ErrUnauthorized) {
			t.Fatal("err should not be ErrUnauthorized for a foreign device")
		}
		if _, ok := devices.byID["d1"]; !ok {
			t.Fatal("device row was deleted despite ownership mismatch")
		}
	})

	t.Run("missing device", func(t *testing.T) {
		svc, devices := newSvc()
		err := svc.Unregister(context.Background(), "u1", "ghost")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, ok := devices.byID["d1"]; !ok {
			t.Fatal("unrelated device row was deleted")
		}
	})
}

// --- SearchService ------------------------------------------------------------
//
// NOTE ON FAKE FIDELITY: the brief's SearchService cases assume fakeThreadRepo
// / fakeEventRepo record lastSearchUserID/lastSearchQuery/lastSearchLimit and
// expose a plural `searchResults` field. The real fakes_test.go (Task 1) has
// neither: the field is the singular `searchResult`, and
// `Search(_ context.Context, userID, query string, limit int)` ignores all
// three arguments and simply returns the programmed searchResult/searchErr.
// So "delegation" and "ordering" below are proven through the OBSERVABLE
// effects the real fakes DO support: which programmed result/error comes
// back, and the nil->empty-slice normalization performed by search.go itself
// -- not through recorded call arguments. This is a substitution, not a
// weaker test: every case in the brief is still covered by an equivalent
// assertion reachable through the real fakes.

// TestSearchServiceDelegatesToRepos pins that Search fans out to both repos
// and returns their rows unchanged (paywall bypassed via SelfHosted). Fake
// field is `searchResult` (singular); the fake does not record call args, so
// delegation is proven by the programmed rows coming back in the result.
func TestSearchServiceDelegatesToRepos(t *testing.T) {
	threads := newThreadRepo()
	threads.searchResult = []domain.Thread{{ID: "t1", Subject: "budget review"}}
	events := newEventRepo()
	events.searchResult = []domain.Event{{ID: "e1", Title: "budget sync"}}

	svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(time.Now()), true)

	res, err := svc.Search(context.Background(), "u1", "  budget  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Threads) != 1 || res.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want the repo's rows", res.Threads)
	}
	if len(res.Events) != 1 || res.Events[0].ID != "e1" {
		t.Fatalf("events = %+v, want the repo's rows", res.Events)
	}
	var _ port.SearchResult = res // result type is port.SearchResult
}

// TestSearchServiceSearch covers SearchService.Search validation, ordering,
// nil-normalization, and error propagation.
func TestSearchServiceSearch(t *testing.T) {
	now := time.Now()

	t.Run("empty query is rejected before delegation", func(t *testing.T) {
		threads := newThreadRepo()
		threads.searchResult = []domain.Thread{{ID: "t1"}} // would prove delegation happened if returned
		events := newEventRepo()
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), true)

		_, err := svc.Search(context.Background(), "u1", "")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("whitespace-only query is rejected", func(t *testing.T) {
		threads := newThreadRepo()
		events := newEventRepo()
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), true)

		_, err := svc.Search(context.Background(), "u1", "   \t ")
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("nil repo results normalize to empty slices", func(t *testing.T) {
		threads := newThreadRepo() // searchResult left nil
		events := newEventRepo()   // searchResult left nil
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), true)

		res, err := svc.Search(context.Background(), "u1", "x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Threads == nil || len(res.Threads) != 0 {
			t.Fatalf("Threads = %#v, want non-nil empty slice", res.Threads)
		}
		if res.Events == nil || len(res.Events) != 0 {
			t.Fatalf("Events = %#v, want non-nil empty slice", res.Events)
		}
	})

	t.Run("thread search error short-circuits before event search", func(t *testing.T) {
		errThread := errors.New("thread search boom")
		errEvent := errors.New("event search boom")
		threads := newThreadRepo()
		threads.searchErr = errThread
		events := newEventRepo()
		events.searchErr = errEvent // must NOT surface: events.Search should never run
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), true)

		_, err := svc.Search(context.Background(), "u1", "x")
		if !errors.Is(err, errThread) {
			t.Fatalf("err = %v, want %v (thread error, proving events.Search never ran)", err, errThread)
		}
		if errors.Is(err, errEvent) {
			t.Fatal("event search error leaked even though threads.Search should have short-circuited first")
		}
	})

	t.Run("event search error propagates", func(t *testing.T) {
		errBoom := errors.New("event search boom")
		threads := newThreadRepo()
		threads.searchResult = []domain.Thread{{ID: "t1"}}
		events := newEventRepo()
		events.searchErr = errBoom
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), true)

		_, err := svc.Search(context.Background(), "u1", "x")
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want %v", err, errBoom)
		}
	})

	t.Run("paywall short-circuits before empty-query validation", func(t *testing.T) {
		threads := newThreadRepo()
		events := newEventRepo()
		svc := NewSearchService(newSubscriptionRepo(), threads, events, newClock(now), false) // no sub seeded for "u1"

		_, err := svc.Search(context.Background(), "u1", "")
		if !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("err = %v, want ErrPaymentRequired (entitlement must run before validation)", err)
		}
		if errors.Is(err, domain.ErrValidation) {
			t.Fatal("empty-query validation ran before the paywall check")
		}
	})

	t.Run("paywall passes with an active subscription", func(t *testing.T) {
		threads := newThreadRepo()
		threads.searchResult = []domain.Thread{{ID: "t1", Subject: "team sync"}}
		events := newEventRepo()
		events.searchResult = []domain.Event{{ID: "e1", Title: "team offsite"}}
		subs := newSubscriptionRepo()
		if err := subs.Upsert(context.Background(), domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}); err != nil {
			t.Fatal(err)
		}
		svc := NewSearchService(subs, threads, events, newClock(now), false)

		res, err := svc.Search(context.Background(), "u1", "team")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Threads) != 1 || res.Threads[0].ID != "t1" {
			t.Fatalf("threads = %+v, want the repo's rows", res.Threads)
		}
		if len(res.Events) != 1 || res.Events[0].ID != "e1" {
			t.Fatalf("events = %+v, want the repo's rows", res.Events)
		}
	})
}

// --- UserService --------------------------------------------------------------

// TestUserServiceEnsureUser covers subject validation, pointer normalization
// (Name/AvatarURL only set when the source string is non-empty), the
// clock-derived CreatedAt, and that Upsert's echoed row is persisted.
func TestUserServiceEnsureUser(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("empty subject is unauthorized", func(t *testing.T) {
		users := newUserRepo()
		svc := NewUserService(users, newUserPreferencesRepo(), newClock(base))

		_, err := svc.EnsureUser(context.Background(), port.Identity{Subject: "", Email: "x@y.com"})
		if !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
		if len(users.byID) != 0 {
			t.Fatalf("byID = %v, want no Upsert attempted", users.byID)
		}
	})

	t.Run("full identity sets both pointers", func(t *testing.T) {
		users := newUserRepo()
		svc := NewUserService(users, newUserPreferencesRepo(), newClock(base))

		u, err := svc.EnsureUser(context.Background(), port.Identity{
			Subject:   "sub-1",
			Email:     "a@b.com",
			Name:      "Ada",
			AvatarURL: "https://img/a.png",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.ID != "sub-1" {
			t.Fatalf("ID = %q, want sub-1", u.ID)
		}
		if u.Email != "a@b.com" {
			t.Fatalf("Email = %q, want a@b.com", u.Email)
		}
		if u.Name == nil || *u.Name != "Ada" {
			t.Fatalf("Name = %v, want pointer to Ada", u.Name)
		}
		if u.AvatarURL == nil || *u.AvatarURL != "https://img/a.png" {
			t.Fatalf("AvatarURL = %v, want pointer to https://img/a.png", u.AvatarURL)
		}
		if !u.CreatedAt.Equal(base) {
			t.Fatalf("CreatedAt = %v, want %v", u.CreatedAt, base)
		}

		// Persistence: the row is retrievable afterwards, matching what was returned.
		stored, err := users.GetByID(context.Background(), "sub-1")
		if err != nil {
			t.Fatalf("user not persisted: %v", err)
		}
		if stored.ID != u.ID || stored.Email != u.Email {
			t.Fatalf("stored = %+v, want it to match the returned row %+v", stored, u)
		}
	})

	t.Run("sparse identity leaves pointers nil", func(t *testing.T) {
		users := newUserRepo()
		svc := NewUserService(users, newUserPreferencesRepo(), newClock(base))

		u, err := svc.EnsureUser(context.Background(), port.Identity{Subject: "sub-2", Email: "c@d.com"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.Name != nil {
			t.Fatalf("Name = %v, want nil", u.Name)
		}
		if u.AvatarURL != nil {
			t.Fatalf("AvatarURL = %v, want nil", u.AvatarURL)
		}
	})
}

// TestUserServicePreferences covers the named-theme preference document
// (M2.6 Task 13): default when unset, upsert round-trip, and validation.
func TestUserServicePreferences(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("missing prefs default to neutral", func(t *testing.T) {
		svc := NewUserService(newUserRepo(), newUserPreferencesRepo(), newClock(base))
		got, err := svc.GetPreferences(ctx, "u1")
		if err != nil {
			t.Fatalf("GetPreferences: %v", err)
		}
		if got.Theme != "neutral" {
			t.Fatalf("Theme = %q, want neutral", got.Theme)
		}
	})

	t.Run("update persists and second update overwrites", func(t *testing.T) {
		svc := NewUserService(newUserRepo(), newUserPreferencesRepo(), newClock(base))
		saved, err := svc.UpdatePreferences(ctx, "u1", port.UserPreferences{Theme: "ocean"})
		if err != nil {
			t.Fatalf("UpdatePreferences: %v", err)
		}
		if saved.Theme != "ocean" {
			t.Fatalf("saved.Theme = %q, want ocean", saved.Theme)
		}
		if _, err := svc.UpdatePreferences(ctx, "u1", port.UserPreferences{Theme: "sunset"}); err != nil {
			t.Fatalf("UpdatePreferences overwrite: %v", err)
		}
		got, err := svc.GetPreferences(ctx, "u1")
		if err != nil || got.Theme != "sunset" {
			t.Fatalf("GetPreferences = %+v, %v — want sunset", got, err)
		}
	})

	t.Run("unknown theme is a validation error", func(t *testing.T) {
		svc := NewUserService(newUserRepo(), newUserPreferencesRepo(), newClock(base))
		if _, err := svc.UpdatePreferences(ctx, "u1", port.UserPreferences{Theme: "bogus"}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
	})
}

// TestUserServiceGetUser is a straight passthrough to UserRepo.GetByID.
func TestUserServiceGetUser(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("existing user", func(t *testing.T) {
		users := newUserRepo()
		users.byID["sub-1"] = domain.User{ID: "sub-1", Email: "a@b.com"}
		svc := NewUserService(users, newUserPreferencesRepo(), newClock(base))

		got, err := svc.GetUser(context.Background(), "sub-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Email != "a@b.com" {
			t.Fatalf("Email = %q, want a@b.com", got.Email)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		users := newUserRepo()
		svc := NewUserService(users, newUserPreferencesRepo(), newClock(base))

		_, err := svc.GetUser(context.Background(), "nope")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// --- SettingsService -----------------------------------------------------------

// TestSettingsServiceGet_DefaultsOnMissingRow covers the missing-row default:
// the repo (real and fake) synthesizes TimeZone "UTC" with no working hours,
// and the service normalizes WorkingHours to an empty (never nil) slice.
func TestSettingsServiceGet_DefaultsOnMissingRow(t *testing.T) {
	svc := NewSettingsService(newUserSettingsRepo())

	got, err := svc.Get(context.Background(), "u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TimeZone != "UTC" {
		t.Fatalf("TimeZone = %q, want UTC", got.TimeZone)
	}
	if got.WorkingHours == nil || len(got.WorkingHours) != 0 {
		t.Fatalf("WorkingHours = %#v, want empty non-nil slice", got.WorkingHours)
	}
}

// TestSettingsServiceUpdate_RoundTrip covers a valid update persisting and
// echoing back through Get.
func TestSettingsServiceUpdate_RoundTrip(t *testing.T) {
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)
	ctx := context.Background()

	in := domain.UserSettings{
		TimeZone:        "America/New_York",
		WorkingLocation: "home",
		WorkingHours: []domain.AvailabilityWindow{
			{Weekday: 1, Start: "09:00", End: "17:00"},
		},
	}
	updated, err := svc.Update(ctx, "u1", in)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.UserID != "u1" {
		t.Fatalf("UserID = %q, want u1", updated.UserID)
	}

	got, err := svc.Get(ctx, "u1")
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.TimeZone != "America/New_York" || got.WorkingLocation != "home" {
		t.Fatalf("Get after Update = %+v, want America/New_York, home", got)
	}
	if len(got.WorkingHours) != 1 || got.WorkingHours[0].Start != "09:00" {
		t.Fatalf("WorkingHours after Update = %+v", got.WorkingHours)
	}
}

// TestSettingsServiceUpdate_InvalidTimeZone covers time zone rejection: an
// unloadable zone name is a validation error, not persisted — and so are ""
// and "Local", which time.LoadLocation alone would silently accept (as UTC
// and the process's own OS zone respectively) despite being ambiguous rather
// than genuine portable IANA zone names.
func TestSettingsServiceUpdate_InvalidTimeZone(t *testing.T) {
	for _, tz := range []string{"Not/AZone", "", "Local"} {
		t.Run(tz, func(t *testing.T) {
			repo := newUserSettingsRepo()
			svc := NewSettingsService(repo)

			_, err := svc.Update(context.Background(), "u1", domain.UserSettings{TimeZone: tz})
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation for TimeZone %q", err, tz)
			}
		})
	}

	t.Run("valid zone is accepted", func(t *testing.T) {
		repo := newUserSettingsRepo()
		svc := NewSettingsService(repo)
		if _, err := svc.Update(context.Background(), "u1", domain.UserSettings{TimeZone: "America/New_York"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	})
}

// TestSettingsServiceUpdate_InvalidWorkingHours covers AvailabilityWindow
// validation (end must be after start) rejecting the update before Upsert.
func TestSettingsServiceUpdate_InvalidWorkingHours(t *testing.T) {
	repo := newUserSettingsRepo()
	svc := NewSettingsService(repo)

	_, err := svc.Update(context.Background(), "u1", domain.UserSettings{
		TimeZone: "UTC",
		WorkingHours: []domain.AvailabilityWindow{
			{Weekday: 1, Start: "17:00", End: "09:00"},
		},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}
