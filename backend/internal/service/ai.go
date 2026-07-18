package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const (
	aiContextMessages = 10   // newest messages included as context
	aiContextBodyMax  = 2000 // per-message body budget, bytes
)

// AIServiceDeps wires an AIService.
type AIServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Accounts      port.AccountRepo
	Threads       port.ThreadRepo
	Messages      port.MessageRepo
	Drafts        port.DraftRepo
	Classifiers   port.ClassifierRepo
	AI            port.AI
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// AIService implements port.AIService (OpenRouter-backed
// compose/reply/summarize/ask, plus classifier CRUD).
type AIService struct {
	ent         entitlement
	accounts    port.AccountRepo
	threads     port.ThreadRepo
	messages    port.MessageRepo
	drafts      port.DraftRepo
	classifiers port.ClassifierRepo
	ai          port.AI
}

var _ port.AIService = (*AIService)(nil)

func NewAIService(d AIServiceDeps) *AIService {
	return &AIService{
		ent:         entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		accounts:    d.Accounts,
		threads:     d.Threads,
		messages:    d.Messages,
		drafts:      d.Drafts,
		classifiers: d.Classifiers,
		ai:          d.AI,
	}
}

func (s *AIService) Compose(ctx context.Context, userID string, req domain.AiComposeRequest) (domain.AiComposeResponse, error) {
	var zero domain.AiComposeResponse
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	if _, err := domain.ParseAiAction(string(req.Action)); err != nil {
		return zero, err
	}
	if strings.TrimSpace(req.Prompt) == "" && req.Action != domain.AiSummarize {
		return zero, fmt.Errorf("%w: prompt is required", domain.ErrValidation)
	}

	var b strings.Builder
	if req.ThreadID != "" {
		t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, req.ThreadID)
		if err != nil {
			return zero, err
		}
		msgs, err := s.messages.ListByThread(ctx, t.ID)
		if err != nil {
			return zero, err
		}
		fmt.Fprintf(&b, "Conversation subject: %s\n\n", t.Subject)
		start := 0
		if len(msgs) > aiContextMessages {
			start = len(msgs) - aiContextMessages
		}
		for _, m := range msgs[start:] {
			body := m.BodyText
			if body == "" {
				body = m.BodyHTML
			}
			fmt.Fprintf(&b, "From %s at %s:\n%s\n\n", m.From.Email, m.SentAt.Format(time.RFC3339), truncate(body, aiContextBodyMax))
		}
	}
	if req.DraftID != "" {
		d, _, err := ownedDraft(ctx, s.drafts, s.accounts, userID, req.DraftID)
		if err != nil {
			return zero, err
		}
		fmt.Fprintf(&b, "Current draft (subject %q):\n%s\n\n", d.Subject, truncate(d.BodyHTML, aiContextBodyMax))
	}
	if req.Prompt != "" {
		fmt.Fprintf(&b, "Instruction: %s", req.Prompt)
	}

	text, model, err := s.ai.Complete(ctx, systemPromptFor(req.Action), strings.TrimSpace(b.String()))
	if err != nil {
		return zero, err
	}
	return domain.AiComposeResponse{Text: text, Model: model}, nil
}

// maxClassifiersPerUser caps how many custom natural-language classifiers a
// user may define, keeping buildClassifySystem's per-message prompt (every
// enabled rule is sent on every classify job) bounded.
const maxClassifiersPerUser = 20

// --- Classifier CRUD (Task 9) ---

func (s *AIService) ListClassifiers(ctx context.Context, userID string) ([]domain.AiClassifier, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	cs, err := s.classifiers.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		cs = []domain.AiClassifier{}
	}
	return cs, nil
}

func (s *AIService) CreateClassifier(ctx context.Context, userID string, in port.ClassifierInput) (domain.AiClassifier, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.AiClassifier{}, err
	}
	c, err := validateClassifierInput(in)
	if err != nil {
		return domain.AiClassifier{}, err
	}
	existing, err := s.classifiers.ListByUser(ctx, userID)
	if err != nil {
		return domain.AiClassifier{}, err
	}
	if len(existing) >= maxClassifiersPerUser {
		return domain.AiClassifier{}, fmt.Errorf("%w: maximum of %d classifiers per user", domain.ErrValidation, maxClassifiersPerUser)
	}
	c.ID = newID()
	c.UserID = userID
	return s.classifiers.Create(ctx, c)
}

func (s *AIService) UpdateClassifier(ctx context.Context, userID, classifierID string, in port.ClassifierInput) (domain.AiClassifier, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.AiClassifier{}, err
	}
	existing, err := s.classifiers.GetByID(ctx, classifierID)
	if err != nil {
		return domain.AiClassifier{}, err
	}
	if existing.UserID != userID {
		return domain.AiClassifier{}, domain.ErrNotFound
	}
	c, err := validateClassifierInput(in)
	if err != nil {
		return domain.AiClassifier{}, err
	}
	c.ID = existing.ID
	c.UserID = userID
	if err := s.classifiers.Update(ctx, c); err != nil {
		return domain.AiClassifier{}, err
	}
	return c, nil
}

func (s *AIService) DeleteClassifier(ctx context.Context, userID, classifierID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	existing, err := s.classifiers.GetByID(ctx, classifierID)
	if err != nil {
		return err
	}
	if existing.UserID != userID {
		return domain.ErrNotFound
	}
	return s.classifiers.Delete(ctx, classifierID)
}

// validateClassifierInput enforces: name and prompt required (trimmed
// non-empty); TargetSplit, when set, must be a known domain.InboxSplit; at
// least one of TargetSplit/LabelName must be set (otherwise a match would do
// nothing). It returns a domain.AiClassifier with everything but ID/UserID
// populated; callers set those.
func validateClassifierInput(in port.ClassifierInput) (domain.AiClassifier, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.AiClassifier{}, fmt.Errorf("%w: name is required", domain.ErrValidation)
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return domain.AiClassifier{}, fmt.Errorf("%w: prompt is required", domain.ErrValidation)
	}
	if in.TargetSplit != "" {
		if _, err := domain.ParseInboxSplit(string(in.TargetSplit)); err != nil {
			return domain.AiClassifier{}, err
		}
	}
	labelName := strings.TrimSpace(in.LabelName)
	if in.TargetSplit == "" && labelName == "" {
		return domain.AiClassifier{}, fmt.Errorf("%w: at least one of targetSplit or labelName is required", domain.ErrValidation)
	}
	return domain.AiClassifier{
		Name:        name,
		Prompt:      prompt,
		TargetSplit: in.TargetSplit,
		LabelName:   labelName,
		Enabled:     in.Enabled,
	}, nil
}

func systemPromptFor(a domain.AiAction) string {
	const base = "You are Calendium's email assistant. Be concise, warm, and professional. Return only the requested text with no preamble."
	switch a {
	case domain.AiReply:
		return base + " Write a reply to the conversation, matching its tone."
	case domain.AiSummarize:
		return base + " Summarize the conversation in a few crisp bullet points."
	case domain.AiAsk:
		return base + " Answer the user's question, using the conversation as context when provided."
	default: // compose
		return base + " Write a complete email following the user's instruction."
	}
}
