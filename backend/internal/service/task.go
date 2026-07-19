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
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// TaskService implements port.TaskService: user-scoped CRUD over first-class
// tasks. Ownership is enforced by UserID match — another user's task is
// domain.ErrNotFound, never an oracle. Completion of external
// (Source != local) tasks is local-only until Task 10 wires TodoProvider
// write-through.
type TaskService struct {
	ent   entitlement
	tasks port.TaskRepo
	clock port.Clock
}

var _ port.TaskService = (*TaskService)(nil)

func NewTaskService(d TaskServiceDeps) *TaskService {
	return &TaskService{
		ent:   entitlement{subs: d.Subscriptions, clock: d.Clock, selfHost: d.SelfHosted},
		tasks: d.Tasks,
		clock: d.Clock,
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
	// Source != local: Task 10 adds TodoProvider write-through here (provider
	// first, local mirror rolled back on failure). Until then completion of a
	// mirrored task is local-only.
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
