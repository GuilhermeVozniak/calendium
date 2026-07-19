package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.CommentRepo --------------------------------------------------------

var _ port.CommentRepo = (*CommentRepo)(nil)

// CommentRepo persists team thread-comments (table thread_comments).
// Soft-deleted rows (deleted_at set) are filtered from every read, so a
// soft-deleted id is indistinguishable from a missing one.
type CommentRepo struct{ s *Store }

// NewCommentRepo returns the postgres port.CommentRepo backed by s.
func NewCommentRepo(s *Store) *CommentRepo { return &CommentRepo{s: s} }

const commentCols = `id, thread_id, team_id, author_id, body,
	array_to_json(mentions)::text, created_at, updated_at, deleted_at`

// mentionsExpr converts a JSON string-array parameter into text[].
func mentionsExpr(param string) string {
	return `(SELECT coalesce(array_agg(x), '{}'::text[]) FROM jsonb_array_elements_text(` + param + `::jsonb) x)`
}

func scanComment(r rowScanner) (domain.Comment, error) {
	var c domain.Comment
	var mentions string
	var deleted sql.NullTime
	if err := r.Scan(&c.ID, &c.ThreadID, &c.TeamID, &c.AuthorID, &c.Body,
		&mentions, &c.CreatedAt, &c.UpdatedAt, &deleted); err != nil {
		return domain.Comment{}, notFound(err)
	}
	if err := unmarshalInto([]byte(mentions), &c.Mentions); err != nil {
		return domain.Comment{}, err
	}
	if c.Mentions == nil {
		c.Mentions = []string{}
	}
	c.DeletedAt = timePtr(deleted)
	return c, nil
}

func (r *CommentRepo) Create(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	mentions, err := jsonArray(c.Mentions)
	if err != nil {
		return domain.Comment{}, err
	}
	_, err = r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO thread_comments (id, thread_id, team_id, author_id, body, mentions, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, `+mentionsExpr("$6")+`, $7, $8)`,
		c.ID, c.ThreadID, c.TeamID, c.AuthorID, c.Body, mentions, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return domain.Comment{}, fmt.Errorf("postgres: create comment: %w", err)
	}
	if c.Mentions == nil {
		c.Mentions = []string{}
	}
	return c, nil
}

func (r *CommentRepo) GetByID(ctx context.Context, id string) (domain.Comment, error) {
	row := r.s.q(ctx).QueryRowContext(ctx, `
		SELECT `+commentCols+` FROM thread_comments
		WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanComment(row)
}

func (r *CommentRepo) ListByThreadTeam(ctx context.Context, threadID, teamID string) ([]domain.Comment, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, `
		SELECT `+commentCols+` FROM thread_comments
		WHERE thread_id = $1 AND team_id = $2 AND deleted_at IS NULL
		ORDER BY created_at, id`, threadID, teamID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CommentRepo) Update(ctx context.Context, c domain.Comment) error {
	mentions, err := jsonArray(c.Mentions)
	if err != nil {
		return err
	}
	return mustAffect(r.s.q(ctx).ExecContext(ctx, `
		UPDATE thread_comments SET
			body       = $2,
			mentions   = `+mentionsExpr("$3")+`,
			updated_at = $4
		WHERE id = $1 AND deleted_at IS NULL`,
		c.ID, c.Body, mentions, c.UpdatedAt))
}

func (r *CommentRepo) SoftDelete(ctx context.Context, id string, at time.Time) error {
	return mustAffect(r.s.q(ctx).ExecContext(ctx, `
		UPDATE thread_comments SET deleted_at = $2
		WHERE id = $1 AND deleted_at IS NULL`, id, at))
}
