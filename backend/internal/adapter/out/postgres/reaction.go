package postgres

import (
	"context"

	"calendium/backend/internal/domain"
)

// --- port.ReactionRepo --------------------------------------------------------

// Create is idempotent on (message_id, user_id, emoji): re-reacting returns
// the existing row rather than erroring. The ON CONFLICT DO UPDATE (a
// same-value no-op on emoji) is the standard trick to make RETURNING report
// the pre-existing row, since DO NOTHING has nothing to return.
func (r reactionRepo) Create(ctx context.Context, react domain.Reaction) (domain.Reaction, error) {
	if react.ID == "" {
		react.ID = newID()
	}
	if react.Delivery == "" {
		react.Delivery = "local"
	}
	err := r.q(ctx).QueryRowContext(ctx, `
		INSERT INTO message_reactions (id, message_id, user_id, emoji, delivery)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (message_id, user_id, emoji) DO UPDATE SET emoji = message_reactions.emoji
		RETURNING id, message_id, emoji, delivery, created_at`,
		react.ID, react.MessageID, react.UserID, react.Emoji, react.Delivery,
	).Scan(&react.ID, &react.MessageID, &react.Emoji, &react.Delivery, &react.CreatedAt)
	if err != nil {
		return domain.Reaction{}, err
	}
	return react, nil
}

// ListByMessages groups reactions by message id.
func (r reactionRepo) ListByMessages(ctx context.Context, messageIDs []string) (map[string][]domain.Reaction, error) {
	result := map[string][]domain.Reaction{}
	if len(messageIDs) == 0 {
		return result, nil
	}
	ids, err := jsonArray(messageIDs)
	if err != nil {
		return nil, err
	}
	rows, err := r.q(ctx).QueryContext(ctx, `
		SELECT id, message_id, user_id, emoji, delivery, created_at
		FROM message_reactions
		WHERE message_id IN (SELECT jsonb_array_elements_text($1::jsonb))
		ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var react domain.Reaction
		if err := rows.Scan(&react.ID, &react.MessageID, &react.UserID, &react.Emoji, &react.Delivery, &react.CreatedAt); err != nil {
			return nil, err
		}
		result[react.MessageID] = append(result[react.MessageID], react)
	}
	return result, rows.Err()
}

// DeleteByEmoji removes only the caller's reaction, returning
// domain.ErrNotFound when absent.
func (r reactionRepo) DeleteByEmoji(ctx context.Context, messageID, userID, emoji string) error {
	return mustAffect(r.q(ctx).ExecContext(ctx, `
		DELETE FROM message_reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3`,
		messageID, userID, emoji))
}
