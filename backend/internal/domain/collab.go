package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Comment is a team comment on a mail thread (M2.7). Comments piggyback on
// an explicit team share (or the caller owning the thread): a comment can
// never be the first thing that exposes a thread. Bodies are user text —
// stored verbatim, escaped render-side by the frontends.
type Comment struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	TeamID   string `json:"teamId"`
	AuthorID string `json:"authorId"`
	Body     string `json:"body"`
	// Mentions are the team-member user IDs resolved from @email tokens at
	// write time. Mentions of non-members fail softly (dropped) so the
	// mention surface can never enumerate users outside the team.
	Mentions  []string   `json:"mentions"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"-"`
	// AuthorName is display-identity enrichment (the author's name, else
	// their email) resolved service-side at read time, only after the
	// caller's team membership and thread visibility were verified. Not
	// persisted. Empty when unresolvable — clients fall back to the id.
	AuthorName string `json:"authorName"`
}

// MaxCommentBodyChars mirrors the thread_comments body CHECK constraint.
const MaxCommentBodyChars = 10000

// ValidateCommentBody enforces the comment-body invariant shared by
// add/edit: non-blank and at most MaxCommentBodyChars characters.
func ValidateCommentBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	if utf8.RuneCountInString(body) > MaxCommentBodyChars {
		return fmt.Errorf("%w: comment body must be at most %d characters", ErrValidation, MaxCommentBodyChars)
	}
	return nil
}

// mentionRe matches an @email mention token, e.g. "@ada@example.com".
var mentionRe = regexp.MustCompile(`@[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// ParseMentions resolves @email tokens in body against team membership and
// returns the mentioned members' user IDs, deduplicated, in order of first
// mention. memberIDByEmail maps each member's lower-cased email to their
// user ID (TeamMember itself carries no email; callers join via UserRepo).
// Tokens that resolve to no member are dropped silently — mentioning a
// non-member must never fail the comment nor enumerate users.
func ParseMentions(body string, memberIDByEmail map[string]string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, tok := range mentionRe.FindAllString(body, -1) {
		email := strings.ToLower(strings.TrimPrefix(tok, "@"))
		id, ok := memberIDByEmail[email]
		if !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
