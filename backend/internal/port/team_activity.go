package port

import (
	"context"

	"calendium/backend/internal/domain"
)

// --- Team read statuses / reply indicators (M2.7 Task 10) --------------------

// TeamThreadActivityRepo persists per-member conversation activity keyed by
// (team, user, conversation key). Implemented by internal/adapter/out/postgres.
// The conversation-key resolver lives here (not on MessageRepo) so the
// feature stays additive to the existing message port and its fakes.
type TeamThreadActivityRepo interface {
	// Upsert merges the row for (TeamID, UserID, ConversationKey): non-nil
	// OpenedAt/RepliedAt overwrite the stored value, nil ones preserve it.
	Upsert(ctx context.Context, a domain.TeamThreadActivity) error
	// ListByConversation returns the team's activity rows for one
	// conversation, FILTERED to members whose share_read_statuses opt-in is
	// currently set: a member who opted out (or later opts out) never
	// appears, even if rows were written while they were opted in.
	ListByConversation(ctx context.Context, teamID, conversationKey string) ([]domain.TeamThreadActivity, error)
	// EarliestRFCMessageID resolves a local thread to its cross-account
	// conversation key: the rfc_message_id of the thread's earliest message
	// carrying one. Returns "" with a nil error when the thread has none.
	EarliestRFCMessageID(ctx context.Context, threadID string) (string, error)
	// ListSharingTeamIDs returns the ids of every team where the user is a
	// member AND has share_read_statuses set — the write-side privacy gate:
	// activity is only ever recorded for these teams.
	ListSharingTeamIDs(ctx context.Context, userID string) ([]string, error)
}

// TeamActivityService serves GET /v1/mail/threads/{id}/team-activity:
// teammate read/replied indicators for the caller's thread.
type TeamActivityService interface {
	// TeamThreadActivity resolves the caller's thread → conversation key →
	// activity rows across every team the caller belongs to, filtered to
	// members with sharing on. A caller in no team, or a conversation with
	// no Message-ID, yields an empty slice — never an error.
	TeamThreadActivity(ctx context.Context, userID, threadID string) ([]domain.TeamThreadActivity, error)
}
