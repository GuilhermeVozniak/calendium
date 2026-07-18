package postgres

import (
	"context"

	"calendium/backend/internal/domain"
)

// --- port.ClassifierRepo -------------------------------------------------------

const classifierCols = `id, user_id, name, prompt, target_split, label_name, enabled`

func scanClassifier(r rowScanner) (domain.AiClassifier, error) {
	var c domain.AiClassifier
	var targetSplit string
	if err := r.Scan(&c.ID, &c.UserID, &c.Name, &c.Prompt, &targetSplit, &c.LabelName, &c.Enabled); err != nil {
		return domain.AiClassifier{}, notFound(err)
	}
	c.TargetSplit = domain.InboxSplit(targetSplit)
	return c, nil
}

func (r classifierRepo) Create(ctx context.Context, c domain.AiClassifier) (domain.AiClassifier, error) {
	if c.ID == "" {
		c.ID = newID()
	}
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO ai_classifiers (id, user_id, name, prompt, target_split, label_name, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.ID, c.UserID, c.Name, c.Prompt, string(c.TargetSplit), c.LabelName, c.Enabled)
	if err != nil {
		return domain.AiClassifier{}, err
	}
	return c, nil
}

func (r classifierRepo) GetByID(ctx context.Context, id string) (domain.AiClassifier, error) {
	row := r.q(ctx).QueryRowContext(ctx,
		`SELECT `+classifierCols+` FROM ai_classifiers WHERE id = $1`, id)
	return scanClassifier(row)
}

func (r classifierRepo) ListByUser(ctx context.Context, userID string) ([]domain.AiClassifier, error) {
	return r.listWhere(ctx, "user_id = $1 ORDER BY name", userID)
}

func (r classifierRepo) ListEnabledByUser(ctx context.Context, userID string) ([]domain.AiClassifier, error) {
	return r.listWhere(ctx, "user_id = $1 AND enabled ORDER BY name", userID)
}

func (r classifierRepo) listWhere(ctx context.Context, where string, userID string) ([]domain.AiClassifier, error) {
	rows, err := r.q(ctx).QueryContext(ctx,
		`SELECT `+classifierCols+` FROM ai_classifiers WHERE `+where, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	classifiers := []domain.AiClassifier{}
	for rows.Next() {
		c, err := scanClassifier(rows)
		if err != nil {
			return nil, err
		}
		classifiers = append(classifiers, c)
	}
	return classifiers, rows.Err()
}

func (r classifierRepo) Update(ctx context.Context, c domain.AiClassifier) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		UPDATE ai_classifiers SET
			name          = $2,
			prompt        = $3,
			target_split  = $4,
			label_name    = $5,
			enabled       = $6,
			updated_at    = now()
		WHERE id = $1`,
		c.ID, c.Name, c.Prompt, string(c.TargetSplit), c.LabelName, c.Enabled))
}

func (r classifierRepo) Delete(ctx context.Context, id string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `DELETE FROM ai_classifiers WHERE id = $1`, id))
}
