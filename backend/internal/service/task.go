package service

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// positionStep is the gap left between appended tasks so drag-reorder can
// bisect neighbors fractionally without rewriting them.
const positionStep = 1024

// TaskServiceDeps wires a TaskService.
type TaskServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Tasks         port.TaskRepo
	Clock         port.Clock
	// TodoProviders + Integrations power completion write-through on external
	// (mirrored) tasks (M2.8 Task 10). Leave nil on instances without any
	// todo vendor configured — completion then degrades to local-only.
	TodoProviders map[domain.TaskSource]port.TodoProvider
	Integrations  port.IntegrationRepo
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// TaskService implements port.TaskService: user-scoped CRUD over first-class
// tasks. Ownership is enforced by UserID match — another user's task is
// domain.ErrNotFound, never an oracle. Completing/reopening an external
// (Source != local) task writes through to its TodoProvider FIRST; the local
// mirror changes only after observed provider success (honesty policy — the
// rail never claims vendor state the vendor hasn't confirmed).
type TaskService struct {
	ent           entitlement
	tasks         port.TaskRepo
	clock         port.Clock
	todoProviders map[domain.TaskSource]port.TodoProvider
	integrations  port.IntegrationRepo
}

var _ port.TaskService = (*TaskService)(nil)

func NewTaskService(d TaskServiceDeps) *TaskService {
	return &TaskService{
		ent:           entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		tasks:         d.Tasks,
		clock:         d.Clock,
		todoProviders: d.TodoProviders,
		integrations:  d.Integrations,
	}
}

// owned fetches a task and enforces user-scoped ownership.
func (s *TaskService) owned(ctx context.Context, userID, taskID string) (domain.Task, error) {
	t, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if t.UserID != userID {
		return domain.Task{}, fmt.Errorf("%w: task %s", domain.ErrNotFound, taskID)
	}
	return t, nil
}

func (s *TaskService) ListTasks(ctx context.Context, userID string, q port.TaskQuery) ([]domain.Task, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	q.UserID = userID // never trust a caller-supplied scope
	tasks, err := s.tasks.List(ctx, q)
	if err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []domain.Task{}
	}
	return tasks, nil
}

func (s *TaskService) CreateTask(ctx context.Context, userID string, in domain.TaskInput) (domain.Task, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Task{}, err
	}
	t := domain.Task{
		UserID:         userID,
		Title:          in.Title,
		Due:            in.Due,
		AllDayDue:      in.AllDayDue,
		ScheduledStart: in.ScheduledStart,
		ScheduledEnd:   in.ScheduledEnd,
		Source:         domain.TaskSourceLocal,
	}
	if in.Notes != "" {
		notes := in.Notes
		t.Notes = &notes
	}
	if in.Position != nil {
		t.Position = *in.Position
	} else {
		// Append to the rail: max position over ALL of the user's tasks
		// (completed included, so reopening never collides) + one step.
		existing, err := s.tasks.List(ctx, port.TaskQuery{UserID: userID, IncludeCompleted: true})
		if err != nil {
			return domain.Task{}, err
		}
		max := 0.0
		for _, e := range existing {
			if e.Position > max {
				max = e.Position
			}
		}
		t.Position = max + positionStep
	}
	if err := t.Validate(); err != nil {
		return domain.Task{}, err
	}
	return s.tasks.Create(ctx, t)
}

func (s *TaskService) UpdateTask(ctx context.Context, userID, taskID string, patch domain.TaskPatch) (domain.Task, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Task{}, err
	}
	t, err := s.owned(ctx, userID, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if patch.Title != nil {
		t.Title = *patch.Title
	}
	if patch.Notes != nil {
		t.Notes = *patch.Notes
	}
	if patch.Due != nil {
		t.Due = *patch.Due
	}
	if patch.AllDayDue != nil {
		t.AllDayDue = *patch.AllDayDue
	}
	if patch.ScheduledStart != nil {
		t.ScheduledStart = *patch.ScheduledStart
	}
	if patch.ScheduledEnd != nil {
		t.ScheduledEnd = *patch.ScheduledEnd
	}
	if patch.Position != nil {
		t.Position = *patch.Position
	}
	if err := t.Validate(); err != nil {
		return domain.Task{}, err
	}
	t.UpdatedAt = s.clock.Now()
	if err := s.tasks.Update(ctx, t); err != nil {
		return domain.Task{}, err
	}
	return t, nil
}

func (s *TaskService) CompleteTask(ctx context.Context, userID, taskID string) (domain.Task, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Task{}, err
	}
	t, err := s.owned(ctx, userID, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if t.Completed() {
		return t, nil // idempotent: keep the original completion timestamp
	}
	// Write-through (M2.8 Task 10): the provider observes the completion
	// FIRST; a provider failure surfaces to the caller and leaves the local
	// task untouched (open).
	if err := s.writeThrough(ctx, t, port.TodoProvider.CompleteTask); err != nil {
		return domain.Task{}, err
	}
	now := s.clock.Now()
	t.CompletedAt = &now
	t.UpdatedAt = now
	if err := s.tasks.Update(ctx, t); err != nil {
		return domain.Task{}, err
	}
	return t, nil
}

func (s *TaskService) ReopenTask(ctx context.Context, userID, taskID string) (domain.Task, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.Task{}, err
	}
	t, err := s.owned(ctx, userID, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if !t.Completed() {
		return t, nil // idempotent
	}
	// Same write-through discipline as CompleteTask: the provider reopens the
	// item first; on failure the local task stays completed.
	if err := s.writeThrough(ctx, t, port.TodoProvider.ReopenTask); err != nil {
		return domain.Task{}, err
	}
	t.CompletedAt = nil
	t.UpdatedAt = s.clock.Now()
	if err := s.tasks.Update(ctx, t); err != nil {
		return domain.Task{}, err
	}
	return t, nil
}

func (s *TaskService) DeleteTask(ctx context.Context, userID, taskID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	if _, err := s.owned(ctx, userID, taskID); err != nil {
		return err
	}
	return s.tasks.Delete(ctx, taskID)
}

// writeThrough runs call against the task's TodoProvider using the owning
// user's connection tokens. Local tasks and instances without the vendor
// adapter configured are no-ops (local-only degradation); once a provider is
// configured, a missing connection or a provider failure surfaces as an
// error so the local mirror is never marked ahead of the vendor.
func (s *TaskService) writeThrough(ctx context.Context, t domain.Task, call func(p port.TodoProvider, ctx context.Context, accessToken, externalID string) error) error {
	if t.Source == domain.TaskSourceLocal {
		return nil
	}
	provider, ok := s.todoProviders[t.Source]
	if !ok || s.integrations == nil {
		return nil
	}
	vendor, ok := vendorForTaskSource(t.Source)
	if !ok {
		return nil
	}
	conn, err := s.integrations.GetByVendor(ctx, t.UserID, vendor)
	if err != nil {
		return fmt.Errorf("resolve %s connection: %w", vendor, err)
	}
	tokens, err := s.integrations.GetTokens(ctx, conn.ID)
	if err != nil {
		return fmt.Errorf("load %s tokens: %w", vendor, err)
	}
	return call(provider, ctx, tokens.AccessToken, t.ExternalID)
}
