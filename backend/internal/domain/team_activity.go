package domain

import "time"

// TeamThreadActivity is one teammate's read/reply state on a shared
// conversation. Conversations are correlated ACROSS accounts by the RFC 5322
// Message-ID of the thread's earliest message (per-account provider thread
// ids differ), so teammates mirroring the same conversation as different
// local threads land on the same ConversationKey.
//
// PRIVACY: rows are written and served ONLY for members whose
// team_members.share_read_statuses opt-in is set — writes are gated on the
// flag and reads re-filter on it, so opting out hides a member immediately.
type TeamThreadActivity struct {
	TeamID          string     `json:"teamId"`
	UserID          string     `json:"userId"`
	ConversationKey string     `json:"conversationKey"`
	OpenedAt        *time.Time `json:"openedAt"`
	RepliedAt       *time.Time `json:"repliedAt"`
}
