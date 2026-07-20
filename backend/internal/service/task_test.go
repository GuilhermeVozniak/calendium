package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// taskHarness wires a TaskService over the in-memory fakes with active
// subscriptions for u1 and u2 and a deterministic clock.
func taskHarness(t *testing.T) (*TaskService, *fakeTaskRepo, *fakeClock) {
	t.Helper()
	subs := newSubscriptionRepo()
	for _, userID := range []string{"u1", "u2"} {
		if err := subs.Upsert(context.Background(), domain.Subscription{UserID: userID, Status: domain.SubscriptionActive}); err != nil {
			t.Fatal(err)
		}
	}
	tasks := newTaskRepo()
	clock := newClock(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	svc := NewTaskService(TaskServiceDeps{Subscriptions: subs, Tasks: tasks, Clock: clock})
	return svc, tasks, clock
}

func TestTaskServiceCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults: source local, position 1024, notes nil", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		got, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "Write brief"})
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if got.UserID != "u1" {
			t.Errorf("UserID = %q, want u1", got.UserID)
		}
		if got.Source != domain.TaskSourceLocal {
			t.Errorf("Source = %q, want local", got.Source)
		}
		if got.Position != 1024 {
			t.Errorf("Position = %v, want 1024", got.Position)
		}
		if got.Notes != nil {
			t.Errorf("Notes = %v, want nil", got.Notes)
		}
		if got.CompletedAt != nil {
			t.Errorf("CompletedAt = %v, want nil", got.CompletedAt)
		}
	})

	t.Run("position defaults to max+1024 across completed tasks too", func(t *testing.T) {
		svc, tasks, _ := taskHarness(t)
		now := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
		if _, err := tasks.Create(ctx, domain.Task{UserID: "u1", Title: "done", Position: 4096, CompletedAt: timePtr(now)}); err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.Create(ctx, domain.Task{UserID: "u2", Title: "other user", Position: 9000}); err != nil {
			t.Fatal(err)
		}
		got, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "next"})
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if got.Position != 4096+1024 {
			t.Errorf("Position = %v, want %v", got.Position, 4096+1024)
		}
	})

	t.Run("explicit position and notes respected", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		pos := 512.0
		got, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "t", Notes: "remember", Position: &pos})
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if got.Position != 512 {
			t.Errorf("Position = %v, want 512", got.Position)
		}
		if got.Notes == nil || *got.Notes != "remember" {
			t.Errorf("Notes = %v, want remember", got.Notes)
		}
	})

	t.Run("scheduled pair carried through", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		got, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "block", ScheduledStart: &start, ScheduledEnd: &end})
		if err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if !got.Scheduled() {
			t.Fatalf("Scheduled() = false, want true")
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
		tests := []struct {
			name string
			in   domain.TaskInput
		}{
			{"empty title", domain.TaskInput{Title: "   "}},
			{"scheduledStart without end", domain.TaskInput{Title: "t", ScheduledStart: &start}},
			{"allDayDue without due", domain.TaskInput{Title: "t", AllDayDue: true}},
		}
		for _, tt := range tests {
			if _, err := svc.CreateTask(ctx, "u1", tt.in); !errors.Is(err, domain.ErrValidation) {
				t.Errorf("%s: CreateTask() err = %v, want ErrValidation", tt.name, err)
			}
		}
	})

	t.Run("entitlement gate", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		if _, err := svc.CreateTask(ctx, "nobody", domain.TaskInput{Title: "t"}); !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("CreateTask() err = %v, want ErrPaymentRequired", err)
		}
	})
}

func TestTaskServiceUpdate(t *testing.T) {
	ctx := context.Background()
	due := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)

	seed := func(t *testing.T, svc *TaskService) domain.Task {
		t.Helper()
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "original", Due: &due})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	t.Run("partial patch leaves other fields untouched", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		task := seed(t, svc)
		got, err := svc.UpdateTask(ctx, "u1", task.ID, domain.TaskPatch{Title: strPtr("renamed")})
		if err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}
		if got.Title != "renamed" {
			t.Errorf("Title = %q, want renamed", got.Title)
		}
		if got.Due == nil || !got.Due.Equal(due) {
			t.Errorf("Due = %v, want %v (untouched)", got.Due, due)
		}
	})

	t.Run("double-pointer clears due and schedule", func(t *testing.T) {
		svc, tasks, _ := taskHarness(t)
		start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "block", Due: &due, ScheduledStart: &start, ScheduledEnd: &end})
		if err != nil {
			t.Fatal(err)
		}
		var clearTime *time.Time
		got, err := svc.UpdateTask(ctx, "u1", task.ID, domain.TaskPatch{
			Due:            &clearTime,
			ScheduledStart: &clearTime,
			ScheduledEnd:   &clearTime,
		})
		if err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}
		if got.Due != nil || got.ScheduledStart != nil || got.ScheduledEnd != nil {
			t.Errorf("cleared fields = %v/%v/%v, want all nil", got.Due, got.ScheduledStart, got.ScheduledEnd)
		}
		stored, err := tasks.GetByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Due != nil {
			t.Errorf("stored Due = %v, want nil", stored.Due)
		}
	})

	t.Run("double-pointer sets a new due", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		task := seed(t, svc)
		newDue := due.Add(48 * time.Hour)
		inner := &newDue
		got, err := svc.UpdateTask(ctx, "u1", task.ID, domain.TaskPatch{Due: &inner})
		if err != nil {
			t.Fatalf("UpdateTask() error = %v", err)
		}
		if got.Due == nil || !got.Due.Equal(newDue) {
			t.Errorf("Due = %v, want %v", got.Due, newDue)
		}
	})

	t.Run("patched task is revalidated", func(t *testing.T) {
		svc, tasks, _ := taskHarness(t)
		task := seed(t, svc)
		start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
		inner := &start
		if _, err := svc.UpdateTask(ctx, "u1", task.ID, domain.TaskPatch{ScheduledStart: &inner}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("UpdateTask() err = %v, want ErrValidation", err)
		}
		stored, err := tasks.GetByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ScheduledStart != nil {
			t.Errorf("invalid patch was persisted: ScheduledStart = %v", stored.ScheduledStart)
		}
	})

	t.Run("cross-user and unknown ids are 404", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		task := seed(t, svc)
		if _, err := svc.UpdateTask(ctx, "u2", task.ID, domain.TaskPatch{Title: strPtr("stolen")}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("cross-user UpdateTask() err = %v, want ErrNotFound", err)
		}
		if _, err := svc.UpdateTask(ctx, "u1", "missing", domain.TaskPatch{Title: strPtr("x")}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("unknown UpdateTask() err = %v, want ErrNotFound", err)
		}
	})
}

func TestTaskServiceCompleteReopen(t *testing.T) {
	ctx := context.Background()

	t.Run("complete stamps the clock and is idempotent", func(t *testing.T) {
		svc, _, clock := taskHarness(t)
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		first := clock.Now()
		done, err := svc.CompleteTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("CompleteTask() error = %v", err)
		}
		if done.CompletedAt == nil || !done.CompletedAt.Equal(first) {
			t.Fatalf("CompletedAt = %v, want %v", done.CompletedAt, first)
		}
		clock.Advance(2 * time.Hour)
		again, err := svc.CompleteTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("idempotent CompleteTask() error = %v", err)
		}
		if again.CompletedAt == nil || !again.CompletedAt.Equal(first) {
			t.Fatalf("idempotent CompletedAt = %v, want original %v", again.CompletedAt, first)
		}
	})

	t.Run("reopen clears and is idempotent", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteTask(ctx, "u1", task.ID); err != nil {
			t.Fatal(err)
		}
		reopened, err := svc.ReopenTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("ReopenTask() error = %v", err)
		}
		if reopened.CompletedAt != nil {
			t.Fatalf("CompletedAt = %v, want nil", reopened.CompletedAt)
		}
		again, err := svc.ReopenTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("idempotent ReopenTask() error = %v", err)
		}
		if again.CompletedAt != nil {
			t.Fatalf("idempotent CompletedAt = %v, want nil", again.CompletedAt)
		}
	})

	t.Run("cross-user complete/reopen/delete are 404", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteTask(ctx, "u2", task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("CompleteTask() err = %v, want ErrNotFound", err)
		}
		if _, err := svc.ReopenTask(ctx, "u2", task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ReopenTask() err = %v, want ErrNotFound", err)
		}
		if err := svc.DeleteTask(ctx, "u2", task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("DeleteTask() err = %v, want ErrNotFound", err)
		}
	})
}

// todoTaskHarness wires a TaskService with a Todoist TodoProvider and an
// active u1 connection (id conn-u1, access token tok-u1). u2 has an active
// subscription but NO Todoist connection.
func todoTaskHarness(t *testing.T) (*TaskService, *fakeTaskRepo, *fakeTodoProvider) {
	t.Helper()
	subs := newSubscriptionRepo()
	for _, userID := range []string{"u1", "u2"} {
		if err := subs.Upsert(context.Background(), domain.Subscription{UserID: userID, Status: domain.SubscriptionActive}); err != nil {
			t.Fatal(err)
		}
	}
	tasks := newTaskRepo()
	integrations := newIntegrationRepo()
	provider := newTodoProvider()
	clock := newClock(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	svc := NewTaskService(TaskServiceDeps{
		Subscriptions: subs,
		Tasks:         tasks,
		Clock:         clock,
		TodoProviders: map[domain.TaskSource]port.TodoProvider{domain.TaskSourceTodoist: provider},
		Integrations:  integrations,
	})
	if _, err := integrations.Create(context.Background(), domain.IntegrationConnection{
		ID: "conn-u1", UserID: "u1", Vendor: domain.IntegrationTodoist, Status: domain.IntegrationStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if err := integrations.SaveTokens(context.Background(), "conn-u1", port.TokenSet{AccessToken: "tok-u1"}); err != nil {
		t.Fatal(err)
	}
	return svc, tasks, provider
}

func TestCompleteTaskWritesThrough(t *testing.T) {
	ctx := context.Background()
	seedExternal := func(t *testing.T, tasks *fakeTaskRepo, userID string) domain.Task {
		t.Helper()
		task, err := tasks.Create(ctx, domain.Task{UserID: userID, Title: "mirrored", Source: domain.TaskSourceTodoist, ExternalID: "ext-1"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	t.Run("provider observes completion with token and external id, then local mark", func(t *testing.T) {
		svc, tasks, provider := todoTaskHarness(t)
		task := seedExternal(t, tasks, "u1")
		done, err := svc.CompleteTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("CompleteTask() error = %v", err)
		}
		if len(provider.completed) != 1 || provider.completed[0] != (todoProviderCall{"tok-u1", "ext-1"}) {
			t.Errorf("provider.completed = %v, want one call with tok-u1/ext-1", provider.completed)
		}
		if done.CompletedAt == nil {
			t.Error("CompletedAt = nil, want set after provider success")
		}
		// Idempotent re-complete never re-hits the vendor.
		if _, err := svc.CompleteTask(ctx, "u1", task.ID); err != nil {
			t.Fatal(err)
		}
		if len(provider.completed) != 1 {
			t.Errorf("provider.completed after idempotent call = %d, want still 1", len(provider.completed))
		}
	})

	t.Run("provider failure surfaces and leaves the task open", func(t *testing.T) {
		svc, tasks, provider := todoTaskHarness(t)
		task := seedExternal(t, tasks, "u1")
		provider.completeErr = errors.New("todoist 500")
		if _, err := svc.CompleteTask(ctx, "u1", task.ID); err == nil {
			t.Fatal("CompleteTask() = nil, want provider error surfaced")
		}
		stored, err := tasks.GetByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.CompletedAt != nil {
			t.Errorf("CompletedAt = %v, want nil (local task untouched)", stored.CompletedAt)
		}
		if tasks.updateCalls != 0 {
			t.Errorf("repo Update calls = %d, want 0 on provider failure", tasks.updateCalls)
		}
	})

	t.Run("reopen writes through; provider failure keeps it completed", func(t *testing.T) {
		svc, tasks, provider := todoTaskHarness(t)
		doneAt := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
		task, err := tasks.Create(ctx, domain.Task{UserID: "u1", Title: "done", Source: domain.TaskSourceTodoist, ExternalID: "ext-1", CompletedAt: &doneAt})
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := svc.ReopenTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("ReopenTask() error = %v", err)
		}
		if len(provider.reopened) != 1 || provider.reopened[0] != (todoProviderCall{"tok-u1", "ext-1"}) {
			t.Errorf("provider.reopened = %v, want one call with tok-u1/ext-1", provider.reopened)
		}
		if reopened.CompletedAt != nil {
			t.Errorf("CompletedAt = %v, want nil", reopened.CompletedAt)
		}

		// Failure path: re-complete locally, then a failing vendor reopen.
		if _, err := svc.CompleteTask(ctx, "u1", task.ID); err != nil {
			t.Fatal(err)
		}
		provider.reopenErr = errors.New("todoist 502")
		if _, err := svc.ReopenTask(ctx, "u1", task.ID); err == nil {
			t.Fatal("ReopenTask() = nil, want provider error surfaced")
		}
		stored, err := tasks.GetByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.CompletedAt == nil {
			t.Error("CompletedAt cleared despite provider failure")
		}
	})

	t.Run("missing connection surfaces ErrNotFound and leaves the task open", func(t *testing.T) {
		svc, tasks, provider := todoTaskHarness(t)
		task := seedExternal(t, tasks, "u2") // u2 has no Todoist connection
		if _, err := svc.CompleteTask(ctx, "u2", task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("CompleteTask() err = %v, want ErrNotFound (no connection)", err)
		}
		if len(provider.completed) != 0 {
			t.Errorf("provider.completed = %v, want none", provider.completed)
		}
		stored, err := tasks.GetByID(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.CompletedAt != nil {
			t.Errorf("CompletedAt = %v, want nil", stored.CompletedAt)
		}
	})

	t.Run("local tasks never call the provider", func(t *testing.T) {
		svc, _, provider := todoTaskHarness(t)
		task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "local"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteTask(ctx, "u1", task.ID); err != nil {
			t.Fatalf("CompleteTask() error = %v", err)
		}
		if len(provider.completed) != 0 {
			t.Errorf("provider.completed = %v, want none for a local task", provider.completed)
		}
	})

	t.Run("no provider configured degrades to local-only completion", func(t *testing.T) {
		svc, tasks, _ := taskHarness(t) // no TodoProviders wired
		task, err := tasks.Create(ctx, domain.Task{UserID: "u1", Title: "mirrored", Source: domain.TaskSourceTodoist, ExternalID: "ext-1"})
		if err != nil {
			t.Fatal(err)
		}
		done, err := svc.CompleteTask(ctx, "u1", task.ID)
		if err != nil {
			t.Fatalf("CompleteTask() error = %v", err)
		}
		if done.CompletedAt == nil {
			t.Error("CompletedAt = nil, want local-only completion when unconfigured")
		}
	})
}

func TestTaskServiceDelete(t *testing.T) {
	ctx := context.Background()
	svc, tasks, _ := taskHarness(t)
	task, err := svc.CreateTask(ctx, "u1", domain.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, "u1", task.ID); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if _, err := tasks.GetByID(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("task still present after delete: err = %v", err)
	}
}

func TestTaskServiceList(t *testing.T) {
	ctx := context.Background()

	t.Run("query passes through with the caller's user id forced", func(t *testing.T) {
		svc, tasks, _ := taskHarness(t)
		from := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 7)
		q := port.TaskQuery{
			UserID:           "someone-else",
			ScheduledFrom:    from,
			ScheduledTo:      to,
			IncludeCompleted: true,
			UnscheduledOnly:  true,
		}
		if _, err := svc.ListTasks(ctx, "u1", q); err != nil {
			t.Fatalf("ListTasks() error = %v", err)
		}
		got := tasks.lastQuery
		if got.UserID != "u1" {
			t.Errorf("repo query UserID = %q, want u1 (forced)", got.UserID)
		}
		if !got.ScheduledFrom.Equal(from) || !got.ScheduledTo.Equal(to) {
			t.Errorf("scheduled range = %v..%v, want %v..%v", got.ScheduledFrom, got.ScheduledTo, from, to)
		}
		if !got.IncludeCompleted || !got.UnscheduledOnly {
			t.Errorf("flags = %v/%v, want true/true", got.IncludeCompleted, got.UnscheduledOnly)
		}
	})

	t.Run("empty result is a non-nil empty slice", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		got, err := svc.ListTasks(ctx, "u1", port.TaskQuery{})
		if err != nil {
			t.Fatalf("ListTasks() error = %v", err)
		}
		if got == nil {
			t.Fatal("ListTasks() = nil, want empty slice")
		}
		if len(got) != 0 {
			t.Fatalf("len = %d, want 0", len(got))
		}
	})

	t.Run("entitlement gate", func(t *testing.T) {
		svc, _, _ := taskHarness(t)
		if _, err := svc.ListTasks(ctx, "nobody", port.TaskQuery{}); !errors.Is(err, domain.ErrPaymentRequired) {
			t.Fatalf("ListTasks() err = %v, want ErrPaymentRequired", err)
		}
	})
}
