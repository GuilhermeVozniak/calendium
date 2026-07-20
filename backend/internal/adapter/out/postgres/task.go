package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.TaskRepo -----------------------------------------------------------

// Tasks returns the task repo. The accessor lives here rather than in
// store.go to keep the M2.8 wave purely additive per file.
func (s *Store) Tasks() port.TaskRepo { return taskRepo{s} }

type taskRepo struct{ *Store }

var _ port.TaskRepo = taskRepo{}

const taskCols = `id, user_id, title, notes, due, all_day_due, scheduled_start,
	scheduled_end, completed_at, source, external_id, source_url, position,
	created_at, updated_at`

func scanTask(r rowScanner) (domain.Task, error) {
	var t domain.Task
	var notes, sourceURL sql.NullString
	var due, schedStart, schedEnd, completed sql.NullTime
	var source string
	if err := r.Scan(&t.ID, &t.UserID, &t.Title, &notes, &due, &t.AllDayDue,
		&schedStart, &schedEnd, &completed, &source, &t.ExternalID, &sourceURL,
		&t.Position, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return domain.Task{}, notFound(err)
	}
	t.Notes = strPtr(notes)
	t.Due = timePtr(due)
	t.ScheduledStart = timePtr(schedStart)
	t.ScheduledEnd = timePtr(schedEnd)
	t.CompletedAt = timePtr(completed)
	t.Source = domain.TaskSource(source)
	t.SourceURL = strPtr(sourceURL)
	return t, nil
}

// Create inserts the task. A second mirrored row for the same
// (user, source, external_id) — tasks_external_idx — maps to
// domain.ErrConflict; local tasks never collide.
func (r taskRepo) Create(ctx context.Context, t domain.Task) (domain.Task, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	if t.Source == "" {
		t.Source = domain.TaskSourceLocal
	}
	row := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO tasks (id, user_id, title, notes, due, all_day_due,
			scheduled_start, scheduled_end, completed_at, source, external_id,
			source_url, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+taskCols,
		t.ID, t.UserID, t.Title, nullStrPtr(t.Notes), nullTimePtr(t.Due), t.AllDayDue,
		nullTimePtr(t.ScheduledStart), nullTimePtr(t.ScheduledEnd), nullTimePtr(t.CompletedAt),
		string(t.Source), t.ExternalID, nullStrPtr(t.SourceURL), t.Position)
	created, err := scanTask(row)
	if err != nil {
		if isConflictSQLState(err) {
			return domain.Task{}, fmt.Errorf("%w: task %s/%s already mirrored", domain.ErrConflict, t.Source, t.ExternalID)
		}
		return domain.Task{}, fmt.Errorf("postgres: create task: %w", err)
	}
	return created, nil
}

func (r taskRepo) GetByID(ctx context.Context, id string) (domain.Task, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE id = $1`, id)
	return scanTask(row)
}

func (r taskRepo) GetByExternalID(ctx context.Context, userID string, source domain.TaskSource, externalID string) (domain.Task, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE user_id = $1 AND source = $2 AND external_id = $3`,
		userID, string(source), externalID)
	return scanTask(row)
}

func (r taskRepo) List(ctx context.Context, q port.TaskQuery) ([]domain.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE user_id = $1`
	args := []any{q.UserID}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if q.Source != "" {
		query += ` AND source = ` + arg(string(q.Source))
	}
	if !q.IncludeCompleted {
		query += ` AND completed_at IS NULL`
	}
	// Overlap with [ScheduledFrom, ScheduledTo): NULL comparisons are falsy,
	// so either bound implicitly restricts to scheduled tasks.
	if !q.ScheduledFrom.IsZero() {
		query += ` AND scheduled_end > ` + arg(q.ScheduledFrom)
	}
	if !q.ScheduledTo.IsZero() {
		query += ` AND scheduled_start < ` + arg(q.ScheduledTo)
	}
	if !q.DueFrom.IsZero() {
		query += ` AND due >= ` + arg(q.DueFrom)
	}
	if !q.DueTo.IsZero() {
		query += ` AND due < ` + arg(q.DueTo)
	}
	if q.UnscheduledOnly {
		query += ` AND scheduled_start IS NULL`
	}
	query += ` ORDER BY position, created_at, id`
	if q.Limit > 0 {
		query += ` LIMIT ` + arg(q.Limit)
	}

	rows, err := r.q(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := []domain.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// Update rewrites the mutable fields. Identity (user_id, source,
// external_id) is immutable after Create.
func (r taskRepo) Update(ctx context.Context, t domain.Task) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE tasks SET
			title           = $2,
			notes           = $3,
			due             = $4,
			all_day_due     = $5,
			scheduled_start = $6,
			scheduled_end   = $7,
			completed_at    = $8,
			source_url      = $9,
			position        = $10,
			updated_at      = now()
		WHERE id = $1`,
		t.ID, t.Title, nullStrPtr(t.Notes), nullTimePtr(t.Due), t.AllDayDue,
		nullTimePtr(t.ScheduledStart), nullTimePtr(t.ScheduledEnd),
		nullTimePtr(t.CompletedAt), nullStrPtr(t.SourceURL), t.Position))
}

func (r taskRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id))
}

// DeleteBySource removes every mirrored task of one source for a user
// (integration disconnect). Zero rows is a clean no-op.
func (r taskRepo) DeleteBySource(ctx context.Context, userID string, source domain.TaskSource) error {
	_, err := r.q(ctx).ExecContext(ctx,
		`DELETE FROM tasks WHERE user_id = $1 AND source = $2`, userID, string(source))
	return err
}
