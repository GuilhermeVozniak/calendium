package service

// ai_test.go covers AIService.Compose's personal-voice injection (Task 11):
// compose/reply carry the learned VoiceProfile in the system prompt when one
// exists; summarize/ask/editing actions never do; a missing profile (no
// VoiceProfiles repo wired, or none learned yet) leaves the prompt
// unchanged either way.

import (
	"context"
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
	}{
		{domain.AiSummarize, ""},
		{domain.AiAsk, "what's the deadline?"},
		{domain.AiImprove, "improve this"},
		{domain.AiShorten, "shorten this"},
		{domain.AiSimplify, "simplify this"},
		{domain.AiFixGrammar, "fix this"},
		{domain.AiChangeTone, "make it warmer"},
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
			})
			if err != nil {
				t.Fatalf("Compose() error = %v, want nil", err)
			}
			if strings.Contains(ai.lastSystem, profile) {
				t.Fatalf("action %s: system prompt = %q, want it to NOT carry the voice profile", tt.action, ai.lastSystem)
			}
			if ai.lastSystem != systemPromptFor(tt.action) {
				t.Fatalf("action %s: system prompt = %q, want unchanged base prompt %q", tt.action, ai.lastSystem, systemPromptFor(tt.action))
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
	if ai.lastSystem != systemPromptFor(domain.AiCompose) {
		t.Fatalf("system prompt = %q, want unchanged base prompt %q", ai.lastSystem, systemPromptFor(domain.AiCompose))
	}
}

func TestAIServiceComposeNilVoiceProfilesRepoSkipsInjection(t *testing.T) {
	ai := newAI()
	svc := NewAIService(AIServiceDeps{
		Accounts:   newAccountRepo(),
		Threads:    newThreadRepo(),
		Messages:   newMessageRepo(),
		Drafts:     newDraftRepo(newAccountRepo()),
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
	if ai.lastSystem != systemPromptFor(domain.AiCompose) {
		t.Fatalf("system prompt = %q, want unchanged base prompt %q", ai.lastSystem, systemPromptFor(domain.AiCompose))
	}
}
