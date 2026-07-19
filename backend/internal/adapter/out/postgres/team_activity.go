package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- port.TeamThreadActivityRepo ---------------------------------------------

var _ port.TeamThreadActivityRepo = (*TeamThreadActivityRepo)(nil)

// TeamThreadActivityRepo persists team read/reply activity (table
// team_thread_activity, migration 0014) plus the messages.rfc_message_id
// conversation-key resolver.
type TeamThreadActivityRepo struct{ s *Store }

// NewTeamThreadActivityRepo returns the postgres port.TeamThreadActivityRepo
// backed by s.
func NewTeamThreadActivityRepo(s *Store) *TeamThreadActivityRepo {
	return &TeamThreadActivityRepo{s: s}
}

// Upsert merges activity for (team, user, conversation): non-nil timestamps
// overwrite, nil ones preserve the stored value.
func (r *TeamThreadActivityRepo) Upsert(ctx context.Context, a domain.TeamThreadActivity) error {
	_, err := r.s.q(ctx).ExecContext(ctx, `
		INSERT INTO team_thread_activity (team_id, user_id, conversation_key, opened_at, replied_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (team_id, user_id, conversation_key) DO UPDATE SET
			opened_at  = COALESCE(EXCLUDED.opened_at,  team_thread_activity.opened_at),
			replied_at = COALESCE(EXCLUDED.replied_at, team_thread_activity.replied_at),
			updated_at = now()`,
		a.TeamID, a.UserID, a.ConversationKey, nullTimePtr(a.OpenedAt), nullTimePtr(a.RepliedAt))
	if err != nil {
		return fmt.Errorf("postgres: upsert team thread activity: %w", err)
	}
	return nil
}

// ListByConversation returns the team's rows for one conversation. The JOIN
// on team_members.share_read_statuses is the read-side privacy gate: a
// member who opted out never appears, even if rows were written while they
// were opted in.
func (r *TeamThreadActivityRepo) ListByConversation(ctx context.Context, teamID, conversationKey string) ([]domain.TeamThreadActivity, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, `
		SELECT a.team_id, a.user_id, a.conversation_key, a.opened_at, a.replied_at
		FROM team_thread_activity a
		JOIN team_members m
		  ON m.team_id = a.team_id AND m.user_id = a.user_id AND m.share_read_statuses
		WHERE a.team_id = $1 AND a.conversation_key = $2
		ORDER BY a.user_id`,
		teamID, conversationKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	acts := []domain.TeamThreadActivity{}
	for rows.Next() {
		var a domain.TeamThreadActivity
		var opened, replied sql.NullTime
		if err := rows.Scan(&a.TeamID, &a.UserID, &a.ConversationKey, &opened, &replied); err != nil {
			return nil, err
		}
		a.OpenedAt = timePtr(opened)
		a.RepliedAt = timePtr(replied)
		acts = append(acts, a)
	}
	return acts, rows.Err()
}

// EarliestRFCMessageID resolves the thread's cross-account conversation key:
// the rfc_message_id of its earliest message carrying one ("" when none).
func (r *TeamThreadActivityRepo) EarliestRFCMessageID(ctx context.Context, threadID string) (string, error) {
	var id string
	err := r.s.q(ctx).QueryRowContext(ctx, `
		SELECT rfc_message_id FROM messages
		WHERE thread_id = $1 AND rfc_message_id IS NOT NULL
		ORDER BY sent_at, id
		LIMIT 1`, threadID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// ListSharingTeamIDs is the write-side privacy gate: the teams where the
// user is a member with share_read_statuses set.
func (r *TeamThreadActivityRepo) ListSharingTeamIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.s.q(ctx).QueryContext(ctx, `
		SELECT team_id FROM team_members
		WHERE user_id = $1 AND share_read_statuses
		ORDER BY team_id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
