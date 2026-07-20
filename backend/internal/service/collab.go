package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// CollabServiceDeps wires NewCollabService (M2.7: thread shares from Task 7
// + team comments from Task 9).
type CollabServiceDeps struct {
	Shares   port.ThreadShareRepo
	Comments port.CommentRepo
	Teams    port.TeamRepo
	Threads  port.ThreadRepo
	Messages port.MessageRepo
	Accounts port.AccountRepo
	Users    port.UserRepo
	Devices  port.DeviceRepo
	Push     port.PushSender // optional; nil disables mention pushes
	Bus      port.EventBus   // optional; nil disables realtime events
	Subs     port.SubscriptionRepo
	Clock    port.Clock
	// SelfHost unlocks the paywall (open-core self-hosted mode).
	SelfHost bool
}

// CollabService implements port.CollabService: tokenized live thread
// shares plus team comments with @mention resolution, push fan-out, and
// bus events. Authorization doctrine: sharing/listing/revoking require
// thread ownership (foreign threads are indistinguishable from missing
// ones — 404, never 403); team shares require the sharer's membership of
// the target team; viewers of unknown, revoked, or expired tokens
// uniformly get ErrNotFound (no oracle). Commenting requires team
// membership plus thread visibility (ownership or a live team-audience
// share). Every mutation persists first; notifications and events are
// best-effort after.
type CollabService struct {
	ent      entitlement
	shares   port.ThreadShareRepo
	comments port.CommentRepo
	teams    port.TeamRepo
	threads  port.ThreadRepo
	messages port.MessageRepo
	accounts port.AccountRepo
	users    port.UserRepo
	devices  port.DeviceRepo
	push     port.PushSender
	bus      port.EventBus
	clock    port.Clock
}

var _ port.CollabService = (*CollabService)(nil)

func NewCollabService(d CollabServiceDeps) *CollabService {
	return &CollabService{
		ent:      entitlement{subs: d.Subs, clock: d.Clock, selfHost: d.SelfHost},
		shares:   d.Shares,
		comments: d.Comments,
		teams:    d.Teams,
		threads:  d.Threads,
		messages: d.Messages,
		accounts: d.Accounts,
		users:    d.Users,
		devices:  d.Devices,
		push:     d.Push,
		bus:      d.Bus,
		clock:    d.Clock,
	}
}

func (s *CollabService) ListComments(ctx context.Context, userID, threadID, teamID string) ([]domain.Comment, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if _, err := membership(ctx, s.teams, userID, teamID); err != nil {
		return nil, err
	}
	if err := s.threadVisibleToTeam(ctx, userID, threadID, teamID); err != nil {
		return nil, err
	}
	cs, err := s.comments.ListByThreadTeam(ctx, threadID, teamID)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		cs = []domain.Comment{}
	}
	// Display-identity enrichment happens only here — after membership and
	// thread visibility passed — so author names never resolve for callers
	// outside the team.
	names := map[string]string{}
	for i := range cs {
		id := cs[i].AuthorID
		if _, ok := names[id]; !ok {
			names[id] = s.displayIdentity(ctx, id)
		}
		cs[i].AuthorName = names[id]
	}
	return cs, nil
}

func (s *CollabService) AddComment(ctx context.Context, userID, threadID string, in port.CommentInput) (domain.Comment, error) {
	var zero domain.Comment
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	if _, err := membership(ctx, s.teams, userID, in.TeamID); err != nil {
		return zero, err
	}
	if err := domain.ValidateCommentBody(in.Body); err != nil {
		return zero, err
	}
	if err := s.threadVisibleToTeam(ctx, userID, threadID, in.TeamID); err != nil {
		return zero, err
	}

	now := s.clock.Now()
	c := domain.Comment{
		ID:        newID(),
		ThreadID:  threadID,
		TeamID:    in.TeamID,
		AuthorID:  userID,
		Body:      in.Body,
		Mentions:  domain.ParseMentions(in.Body, s.memberIDsByEmail(ctx, in.TeamID)),
		CreatedAt: now,
		UpdatedAt: now,
	}
	c, err := s.comments.Create(ctx, c)
	if err != nil {
		return zero, err
	}

	// Best-effort after persist: realtime event + mention notifications.
	s.publishComment("comment.created", c)
	s.notifyMentions(ctx, c, c.Mentions)
	c.AuthorName = s.displayIdentity(ctx, c.AuthorID)
	return c, nil
}

func (s *CollabService) UpdateComment(ctx context.Context, userID, commentID, body string) (domain.Comment, error) {
	var zero domain.Comment
	if err := s.ent.require(ctx, userID); err != nil {
		return zero, err
	}
	c, err := s.comments.GetByID(ctx, commentID)
	if err != nil {
		return zero, err
	}
	if _, err := membership(ctx, s.teams, userID, c.TeamID); err != nil {
		return zero, err
	}
	if c.AuthorID != userID {
		// Known member, insufficient rights: admins may delete, never edit.
		return zero, fmt.Errorf("%w: only the author may edit a comment", domain.ErrForbidden)
	}
	if err := domain.ValidateCommentBody(body); err != nil {
		return zero, err
	}

	prev := map[string]struct{}{}
	for _, id := range c.Mentions {
		prev[id] = struct{}{}
	}
	c.Body = body
	c.Mentions = domain.ParseMentions(body, s.memberIDsByEmail(ctx, c.TeamID))
	c.UpdatedAt = s.clock.Now()
	if err := s.comments.Update(ctx, c); err != nil {
		return zero, err
	}

	s.publishComment("comment.updated", c)
	// Notify only members newly mentioned by this edit.
	added := []string{}
	for _, id := range c.Mentions {
		if _, ok := prev[id]; !ok {
			added = append(added, id)
		}
	}
	s.notifyMentions(ctx, c, added)
	c.AuthorName = s.displayIdentity(ctx, c.AuthorID)
	return c, nil
}

func (s *CollabService) DeleteComment(ctx context.Context, userID, commentID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	c, err := s.comments.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	m, err := membership(ctx, s.teams, userID, c.TeamID)
	if err != nil {
		return err
	}
	if c.AuthorID != userID && !m.Role.AtLeast(domain.TeamRoleAdmin) {
		return fmt.Errorf("%w: only the author or a team admin may delete a comment", domain.ErrForbidden)
	}
	if err := s.comments.SoftDelete(ctx, c.ID, s.clock.Now()); err != nil {
		return err
	}
	s.publishComment("comment.deleted", c)
	return nil
}

// threadVisibleToTeam enforces the comment-visibility rule: the caller owns
// the thread, or an unrevoked, unexpired team-audience share for teamID
// exists on it. Anything else — including a missing thread — is
// ErrNotFound, so foreign threads are indistinguishable from missing ones.
// Fail-closed: a nil shares repo means no share can exist, so only thread
// owners pass.
func (s *CollabService) threadVisibleToTeam(ctx context.Context, userID, threadID, teamID string) error {
	th, err := s.threads.GetByID(ctx, threadID)
	if err != nil {
		return domain.ErrNotFound
	}
	acct, err := s.accounts.GetByID(ctx, th.AccountID)
	if err == nil && acct.UserID == userID {
		return nil // the owner is always allowed on their own thread
	}
	if s.shares != nil {
		shares, err := s.shares.ListByThread(ctx, threadID)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		for _, sh := range shares {
			if sh.Audience == domain.ShareAudienceTeam && sh.TeamID != nil && *sh.TeamID == teamID && sh.Live(now) {
				return nil
			}
		}
	}
	return domain.ErrNotFound
}

// memberIDsByEmail joins the team roster with UserRepo into the
// lower-cased email -> userID map ParseMentions resolves against. Lookup
// failures drop that member from mention resolution (best-effort).
func (s *CollabService) memberIDsByEmail(ctx context.Context, teamID string) map[string]string {
	out := map[string]string{}
	members, err := s.teams.ListMembers(ctx, teamID)
	if err != nil {
		return out
	}
	for _, m := range members {
		u, err := s.users.GetByID(ctx, m.UserID)
		if err != nil || u.Email == "" {
			continue
		}
		out[strings.ToLower(u.Email)] = m.UserID
	}
	return out
}

// commentEventPayload is the id-only bus payload (never full bodies).
type commentEventPayload struct {
	CommentID string `json:"commentId"`
	ThreadID  string `json:"threadId"`
	TeamID    string `json:"teamId"`
	AuthorID  string `json:"authorId"`
}

func (s *CollabService) publishComment(evType string, c domain.Comment) {
	if s.bus == nil {
		return
	}
	payload, _ := json.Marshal(commentEventPayload{
		CommentID: c.ID, ThreadID: c.ThreadID, TeamID: c.TeamID, AuthorID: c.AuthorID,
	})
	s.bus.Publish(port.CollabEvent{Topic: "team:" + c.TeamID, Type: evType, Payload: payload})
}

// notifyMentions fans one push per registered device out to every
// mentioned member (self-mentions excluded) and publishes a per-user
// "mention" bus event. Strictly best-effort: failures are swallowed — the
// comment is already persisted.
func (s *CollabService) notifyMentions(ctx context.Context, c domain.Comment, mentionees []string) {
	if len(mentionees) == 0 {
		return
	}
	payload, _ := json.Marshal(commentEventPayload{
		CommentID: c.ID, ThreadID: c.ThreadID, TeamID: c.TeamID, AuthorID: c.AuthorID,
	})
	title := s.authorDisplay(ctx, c.AuthorID) + " mentioned you"
	for _, uid := range mentionees {
		if uid == c.AuthorID {
			continue // never notify yourself
		}
		if s.bus != nil {
			s.bus.Publish(port.CollabEvent{Topic: "user:" + uid, Type: "mention", Payload: payload})
		}
		if s.push == nil || s.devices == nil {
			continue
		}
		devices, err := s.devices.ListByUser(ctx, uid)
		if err != nil {
			continue
		}
		for _, d := range devices {
			_ = s.push.Send(ctx, d, title, truncate(c.Body, 140), map[string]string{
				"threadId":  c.ThreadID,
				"commentId": c.ID,
				"teamId":    c.TeamID,
			})
		}
	}
}

// displayIdentity resolves a comment author's API-facing display identity:
// name, else email, else empty. Honest by construction — an unresolvable
// user yields "" and clients fall back to the id; nothing is fabricated.
func (s *CollabService) displayIdentity(ctx context.Context, userID string) string {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return ""
	}
	if u.Name != nil && *u.Name != "" {
		return *u.Name
	}
	return u.Email
}

// authorDisplay is the push-title author name: display name, else email,
// else a neutral fallback.
func (s *CollabService) authorDisplay(ctx context.Context, userID string) string {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "A teammate"
	}
	if u.Name != nil && *u.Name != "" {
		return *u.Name
	}
	if u.Email != "" {
		return u.Email
	}
	return "A teammate"
}
