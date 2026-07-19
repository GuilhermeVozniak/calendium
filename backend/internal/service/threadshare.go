package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// CollabServiceDeps wires a CollabService (M2.7 Task 7: shared
// conversations; grown by comments/activity in Tasks 9-10).
type CollabServiceDeps struct {
	Shares   port.ThreadShareRepo
	Teams    port.TeamRepo
	Threads  port.ThreadRepo
	Messages port.MessageRepo
	Accounts port.AccountRepo
	Subs     port.SubscriptionRepo
	Clock    port.Clock
	// SelfHost unlocks the paywall (open-core self-hosted mode).
	SelfHost bool
}

// CollabService implements port.CollabService. Authorization doctrine:
// sharing/listing/revoking require thread ownership (foreign threads are
// indistinguishable from missing ones — 404, never 403); team shares
// require the sharer's membership of the target team (non-members get
// ErrNotFound so team existence never leaks); viewers of unknown, revoked,
// or expired tokens uniformly get ErrNotFound (no oracle).
type CollabService struct {
	ent      entitlement
	shares   port.ThreadShareRepo
	teams    port.TeamRepo
	threads  port.ThreadRepo
	messages port.MessageRepo
	accounts port.AccountRepo
	clock    port.Clock
}

var _ port.CollabService = (*CollabService)(nil)

func NewCollabService(d CollabServiceDeps) *CollabService {
	return &CollabService{
		ent:      entitlement{subs: d.Subs, clock: d.Clock, selfHost: d.SelfHost},
		shares:   d.Shares,
		teams:    d.Teams,
		threads:  d.Threads,
		messages: d.Messages,
		accounts: d.Accounts,
		clock:    d.Clock,
	}
}

// shareTokenBytes sizes the raw link token (32 random bytes, 64 hex chars —
// the team-invitation precedent).
const shareTokenBytes = 32

// hashShareToken derives the stored lookup key: shares persist only the
// SHA-256 hex of the raw token, which itself appears once, in the create
// response.
func hashShareToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *CollabService) ShareThread(ctx context.Context, userID, threadID string, in port.ShareThreadInput) (domain.ThreadShare, string, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return domain.ThreadShare{}, "", err
	}
	if _, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID); err != nil {
		return domain.ThreadShare{}, "", err
	}
	audience, err := domain.ParseShareAudience(string(in.Audience))
	if err != nil {
		return domain.ThreadShare{}, "", err
	}
	var teamID *string
	switch audience {
	case domain.ShareAudienceTeam:
		tid := strings.TrimSpace(in.TeamID)
		if tid == "" {
			return domain.ThreadShare{}, "", fmt.Errorf("%w: teamId is required for a team share", domain.ErrValidation)
		}
		// Sharer must be a member of the target team; non-members get
		// ErrNotFound (membership check first, existence never leaked).
		if _, err := membership(ctx, s.teams, userID, tid); err != nil {
			return domain.ThreadShare{}, "", err
		}
		teamID = &tid
	case domain.ShareAudienceExternal:
		if strings.TrimSpace(in.TeamID) != "" {
			return domain.ThreadShare{}, "", fmt.Errorf("%w: teamId is only valid for a team share", domain.ErrValidation)
		}
	}
	now := s.clock.Now()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return domain.ThreadShare{}, "", fmt.Errorf("%w: expiresAt must be in the future", domain.ErrValidation)
	}
	raw := randomToken(shareTokenBytes)
	share, err := s.shares.Create(ctx, domain.ThreadShare{
		ID:        newID(),
		ThreadID:  threadID,
		CreatedBy: userID,
		Audience:  audience,
		TeamID:    teamID,
		TokenHash: hashShareToken(raw),
		ExpiresAt: in.ExpiresAt,
		CreatedAt: now,
	})
	if err != nil {
		return domain.ThreadShare{}, "", err
	}
	return share, raw, nil
}

func (s *CollabService) ListThreadShares(ctx context.Context, userID, threadID string) ([]domain.ThreadShare, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if _, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID); err != nil {
		return nil, err
	}
	shares, err := s.shares.ListByThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if shares == nil {
		shares = []domain.ThreadShare{}
	}
	return shares, nil
}

func (s *CollabService) RevokeThreadShare(ctx context.Context, userID, threadID, shareID string) error {
	if err := s.ent.require(ctx, userID); err != nil {
		return err
	}
	if _, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID); err != nil {
		return err
	}
	share, err := s.shares.GetByID(ctx, shareID)
	if err != nil {
		return err
	}
	if share.ThreadID != threadID {
		return domain.ErrNotFound
	}
	return s.shares.Revoke(ctx, share.ID, s.clock.Now())
}

// authorizeShare resolves a raw token to a live share the viewer may open.
// Every failure — unknown token, revoked, expired, team share without a
// viewer, viewer not a member — collapses to ErrNotFound (no oracle).
func (s *CollabService) authorizeShare(ctx context.Context, rawToken string, viewerUserID *string) (domain.ThreadShare, error) {
	share, err := s.shares.GetByTokenHash(ctx, hashShareToken(rawToken))
	if err != nil {
		return domain.ThreadShare{}, domain.ErrNotFound
	}
	if !share.Live(s.clock.Now()) {
		return domain.ThreadShare{}, domain.ErrNotFound
	}
	if share.Audience == domain.ShareAudienceTeam {
		if viewerUserID == nil || share.TeamID == nil {
			return domain.ThreadShare{}, domain.ErrNotFound
		}
		if _, err := membership(ctx, s.teams, *viewerUserID, *share.TeamID); err != nil {
			return domain.ThreadShare{}, err
		}
	}
	return share, nil
}

func (s *CollabService) GetSharedThread(ctx context.Context, rawToken string, viewerUserID *string) (port.SharedThreadView, error) {
	share, err := s.authorizeShare(ctx, rawToken, viewerUserID)
	if err != nil {
		return port.SharedThreadView{}, err
	}
	thread, err := s.threads.GetByID(ctx, share.ThreadID)
	if err != nil {
		return port.SharedThreadView{}, domain.ErrNotFound
	}
	msgs, err := s.messages.ListByThread(ctx, share.ThreadID)
	if err != nil {
		return port.SharedThreadView{}, err
	}
	return port.SharedThreadView{
		Subject:   thread.Subject,
		Audience:  share.Audience,
		Messages:  projectSharedMessages(msgs),
		UpdatedAt: thread.LastMessageAt,
	}, nil
}

func (s *CollabService) ResolveShare(ctx context.Context, rawToken string, viewerUserID *string) (string, error) {
	share, err := s.authorizeShare(ctx, rawToken, viewerUserID)
	if err != nil {
		return "", err
	}
	return share.ID, nil
}

// projectSharedMessages builds the viewer-safe message list: drafts are
// dropped (unsent content never leaves), Bcc recipients are stripped for
// every audience, and owner-private read receipts / reaction attribution
// are withheld (the brief's field list is the ceiling, not the floor).
func projectSharedMessages(msgs []domain.Message) []domain.Message {
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.IsDraft {
			continue
		}
		m.Bcc = []domain.EmailAddress{}
		m.OpenedAt = nil
		m.Reactions = []domain.Reaction{}
		out = append(out, m)
	}
	return out
}

// --- SyncService share fan-out (M2.7 Task 7) ---------------------------------

// publishShareUpdates emits {Topic: "share:<id>", Type: "share.updated"}
// for every live share on each thread that just received messages. Ids
// only — never message content — cross the bus; viewers refetch through
// the token-authorized share endpoints. Best-effort and nil-safe: a
// worker wired without shares/bus skips it entirely, and repo errors
// never fail a sync pass.
func (s *SyncService) publishShareUpdates(ctx context.Context, threadIDs map[string]struct{}) {
	if s.bus == nil || s.shares == nil || len(threadIDs) == 0 {
		return
	}
	now := s.clock.Now()
	for threadID := range threadIDs {
		shares, err := s.shares.ListByThread(ctx, threadID)
		if err != nil {
			continue
		}
		for _, share := range shares {
			if !share.Live(now) {
				continue
			}
			s.bus.Publish(port.CollabEvent{Topic: "share:" + share.ID, Type: "share.updated"})
		}
	}
}
