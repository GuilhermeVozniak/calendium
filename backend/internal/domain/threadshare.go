package domain

import (
	"fmt"
	"time"
)

// ShareAudience scopes who may open a thread share link.
type ShareAudience string

const (
	// ShareAudienceTeam limits viewers to authenticated members of the
	// share's team.
	ShareAudienceTeam ShareAudience = "team"
	// ShareAudienceExternal admits anyone holding the link (unauthenticated).
	ShareAudienceExternal ShareAudience = "external"
)

// ParseShareAudience validates an audience body parameter.
func ParseShareAudience(s string) (ShareAudience, error) {
	switch ShareAudience(s) {
	case ShareAudienceTeam, ShareAudienceExternal:
		return ShareAudience(s), nil
	}
	return "", fmt.Errorf("%w: unknown share audience %q", ErrValidation, s)
}

// ThreadShare is a tokenized live link to a mail thread (M2.7 shared
// conversations). The raw token appears exactly once, in the create
// response; only its SHA-256 hex is persisted (TokenHash is never
// serialized). Revocation and optional expiry both close the link;
// closed/unknown tokens are indistinguishable to viewers (no oracle).
type ThreadShare struct {
	ID        string        `json:"id"`
	ThreadID  string        `json:"threadId"`
	CreatedBy string        `json:"createdBy"`
	Audience  ShareAudience `json:"audience"`
	// TeamID scopes a team-audience share; nil for external shares.
	TeamID    *string    `json:"teamId"`
	TokenHash string     `json:"-"`
	RevokedAt *time.Time `json:"revokedAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

// Live reports whether the share still admits viewers at now: not revoked
// and not past its optional expiry.
func (s ThreadShare) Live(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	return s.ExpiresAt == nil || now.Before(*s.ExpiresAt)
}
