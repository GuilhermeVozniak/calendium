package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestTaskRepoCreateGetList(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	repo := st.Tasks()

	notes := "2% not whole"
	due := time.Date(2026, 7, 21, 17, 0, 0, 0, time.UTC)
	created, err := repo.Create(ctx, domain.Task{
		UserID: "u1", Title: "buy milk", Notes: &notes, Due: &due,
		AllDayDue: true, Position: 1.5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Create returned incomplete row: %+v", created)
	}
	if created.Source != domain.TaskSourceLocal {
		t.Fatalf("Source = %q, want default %q", created.Source, domain.TaskSourceLocal)
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.UserID != "u1" || got.Title != "buy milk" || got.Notes == nil || *got.Notes != notes {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Due == nil || !got.Due.Equal(due) || !got.AllDayDue {
		t.Fatalf("due mismatch: %+v", got)
	}
	if got.Position != 1.5 || got.CompletedAt != nil || got.Scheduled() {
		t.Fatalf("field mismatch: %+v", got)
	}

	if _, err := repo.GetByID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID(missing) = %v, want ErrNotFound", err)
	}

	list, err := repo.List(ctx, port.TaskQuery{UserID: "u1"})
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List(u1) = %+v, %v; want the created task", list, err)
	}
	other, err := repo.List(ctx, port.TaskQuery{UserID: "u2"})
	if err != nil || len(other) != 0 {
		t.Fatalf("List(u2) = %+v, %v; want empty", other, err)
	}
}

func TestTaskRepoGetByExternalID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	url := "https://todoist.com/task/42"
	created, err := repo.Create(ctx, domain.Task{
		UserID: "u1", Title: "mirrored", Source: domain.TaskSourceTodoist,
		ExternalID: "ext-42", SourceURL: &url,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByExternalID(ctx, "u1", domain.TaskSourceTodoist, "ext-42")
	if err != nil {
		t.Fatalf("GetByExternalID: %v", err)
	}
	if got.ID != created.ID || got.SourceURL == nil || *got.SourceURL != url {
		t.Fatalf("GetByExternalID mismatch: %+v", got)
	}

	if _, err := repo.GetByExternalID(ctx, "u1", domain.TaskSourceTodoist, "never-synced"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByExternalID(miss) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetByExternalID(ctx, "u2", domain.TaskSourceTodoist, "ext-42"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByExternalID(wrong user) = %v, want ErrNotFound", err)
	}
}

func TestTaskRepoExternalConflict(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	mirror := domain.Task{
		UserID: "u1", Title: "mirrored", Source: domain.TaskSourceTodoist, ExternalID: "ext-1",
	}
	if _, err := repo.Create(ctx, mirror); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := repo.Create(ctx, mirror); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate external Create = %v, want ErrConflict", err)
	}

	// Local tasks all carry ('local', '') — the partial unique index must
	// never treat them as duplicates.
	for i := 0; i < 2; i++ {
		if _, err := repo.Create(ctx, domain.Task{UserID: "u1", Title: "local"}); err != nil {
			t.Fatalf("local Create %d: %v", i, err)
		}
	}
}

func TestTaskRepoListScheduledRange(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	block := func(title string, start, end time.Time) domain.Task {
		task, err := repo.Create(ctx, domain.Task{
			UserID: "u1", Title: title, ScheduledStart: &start, ScheduledEnd: &end,
		})
		if err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
		return task
	}
	morning := block("morning", day.Add(9*time.Hour), day.Add(10*time.Hour))
	noon := block("noon", day.Add(12*time.Hour), day.Add(13*time.Hour))
	block("tomorrow", day.Add(33*time.Hour), day.Add(34*time.Hour))
	rail, err := repo.Create(ctx, domain.Task{UserID: "u1", Title: "rail"})
	if err != nil {
		t.Fatalf("Create(rail): %v", err)
	}

	// Overlap query: [09:30, 12:30) touches morning and noon only.
	got, err := repo.List(ctx, port.TaskQuery{
		UserID:        "u1",
		ScheduledFrom: day.Add(9*time.Hour + 30*time.Minute),
		ScheduledTo:   day.Add(12*time.Hour + 30*time.Minute),
	})
	if err != nil {
		t.Fatalf("List(range): %v", err)
	}
	if len(got) != 2 || got[0].ID != morning.ID || got[1].ID != noon.ID {
		t.Fatalf("List(range) = %+v, want [morning noon]", titles(got))
	}

	// Half-open bounds: a block ending exactly at From or starting exactly
	// at To does not overlap.
	got, err = repo.List(ctx, port.TaskQuery{
		UserID:        "u1",
		ScheduledFrom: day.Add(10 * time.Hour),
		ScheduledTo:   day.Add(12 * time.Hour),
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("List(half-open) = %+v, %v; want empty", titles(got), err)
	}

	got, err = repo.List(ctx, port.TaskQuery{UserID: "u1", UnscheduledOnly: true})
	if err != nil || len(got) != 1 || got[0].ID != rail.ID {
		t.Fatalf("List(unscheduled) = %+v, %v; want [rail]", titles(got), err)
	}
}

func TestTaskRepoCompletedAndSourceFilters(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	open, err := repo.Create(ctx, domain.Task{UserID: "u1", Title: "open", Position: 1})
	if err != nil {
		t.Fatalf("Create(open): %v", err)
	}
	done, err := repo.Create(ctx, domain.Task{UserID: "u1", Title: "done", Position: 2})
	if err != nil {
		t.Fatalf("Create(done): %v", err)
	}
	if _, err := repo.Create(ctx, domain.Task{
		UserID: "u1", Title: "mirrored", Source: domain.TaskSourceTodoist, ExternalID: "e1", Position: 3,
	}); err != nil {
		t.Fatalf("Create(mirrored): %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	done.CompletedAt = &now
	if err := repo.Update(ctx, done); err != nil {
		t.Fatalf("Update(complete): %v", err)
	}

	got, err := repo.List(ctx, port.TaskQuery{UserID: "u1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("List(default) = %+v, %v; want completed excluded", titles(got), err)
	}
	got, err = repo.List(ctx, port.TaskQuery{UserID: "u1", IncludeCompleted: true})
	if err != nil || len(got) != 3 {
		t.Fatalf("List(IncludeCompleted) = %+v, %v; want 3", titles(got), err)
	}
	if got[0].ID != open.ID || got[1].ID != done.ID {
		t.Fatalf("List order = %+v, want position order", titles(got))
	}
	got, err = repo.List(ctx, port.TaskQuery{UserID: "u1", Source: domain.TaskSourceTodoist})
	if err != nil || len(got) != 1 || got[0].Title != "mirrored" {
		t.Fatalf("List(source) = %+v, %v; want [mirrored]", titles(got), err)
	}
	got, err = repo.List(ctx, port.TaskQuery{UserID: "u1", IncludeCompleted: true, Limit: 1})
	if err != nil || len(got) != 1 || got[0].ID != open.ID {
		t.Fatalf("List(limit) = %+v, %v; want [open]", titles(got), err)
	}
}

func TestTaskRepoDueWindow(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	mk := func(title string, due *time.Time) {
		if _, err := repo.Create(ctx, domain.Task{UserID: "u1", Title: title, Due: due}); err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
	}
	morning := day.Add(9 * time.Hour)
	evening := day.Add(20 * time.Hour)
	tomorrow := day.Add(24 * time.Hour)
	mk("today-morning", &morning)
	mk("today-evening", &evening)
	mk("tomorrow", &tomorrow)
	mk("undated", nil)

	got, err := repo.List(ctx, port.TaskQuery{UserID: "u1", DueFrom: day, DueTo: day.Add(24 * time.Hour)})
	if err != nil || len(got) != 2 {
		t.Fatalf("List(due today) = %+v, %v; want 2", titles(got), err)
	}
	// DueTo is exclusive: tomorrow 00:00 falls in the next window.
	got, err = repo.List(ctx, port.TaskQuery{UserID: "u1", DueFrom: tomorrow, DueTo: tomorrow.Add(24 * time.Hour)})
	if err != nil || len(got) != 1 || got[0].Title != "tomorrow" {
		t.Fatalf("List(due tomorrow) = %+v, %v; want [tomorrow]", titles(got), err)
	}
}

func TestTaskRepoUpdateDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	repo := st.Tasks()

	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	task, err := repo.Create(ctx, domain.Task{
		UserID: "u1", Title: "before", ScheduledStart: &start, ScheduledEnd: &end,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	notes := "edited"
	task.Title = "after"
	task.Notes = &notes
	task.ScheduledStart, task.ScheduledEnd = nil, nil
	task.Position = 7.25
	if err := repo.Update(ctx, task); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != "after" || got.Notes == nil || *got.Notes != notes || got.Scheduled() || got.Position != 7.25 {
		t.Fatalf("Update not persisted: %+v", got)
	}
	if !got.UpdatedAt.After(got.CreatedAt) {
		t.Fatalf("UpdatedAt %v not bumped past CreatedAt %v", got.UpdatedAt, got.CreatedAt)
	}

	if err := repo.Update(ctx, domain.Task{ID: "missing", Title: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID(deleted) = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete(missing) = %v, want ErrNotFound", err)
	}
}

func TestTaskRepoDeleteBySource(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	repo := st.Tasks()

	mk := func(userID, title string, source domain.TaskSource, externalID string) {
		if _, err := repo.Create(ctx, domain.Task{
			UserID: userID, Title: title, Source: source, ExternalID: externalID,
		}); err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
	}
	mk("u1", "m1", domain.TaskSourceTodoist, "e1")
	mk("u1", "m2", domain.TaskSourceTodoist, "e2")
	mk("u1", "keep", domain.TaskSourceLocal, "")
	mk("u2", "other", domain.TaskSourceTodoist, "e1")

	if err := repo.DeleteBySource(ctx, "u1", domain.TaskSourceTodoist); err != nil {
		t.Fatalf("DeleteBySource: %v", err)
	}
	got, err := repo.List(ctx, port.TaskQuery{UserID: "u1"})
	if err != nil || len(got) != 1 || got[0].Title != "keep" {
		t.Fatalf("after disconnect List(u1) = %+v, %v; want [keep]", titles(got), err)
	}
	got, err = repo.List(ctx, port.TaskQuery{UserID: "u2"})
	if err != nil || len(got) != 1 || got[0].Title != "other" {
		t.Fatalf("after disconnect List(u2) = %+v, %v; want [other]", titles(got), err)
	}
	// Disconnecting an already-clean source is a no-op, not an error.
	if err := repo.DeleteBySource(ctx, "u1", domain.TaskSourceTodoist); err != nil {
		t.Fatalf("DeleteBySource(again): %v", err)
	}
}

func titles(tasks []domain.Task) []string {
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = task.Title
	}
	return out
}
