package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// This file is M2.7 Task 10 (team read statuses / reply indicators): the
// TeamActivityService query surface plus the MailService/SyncService hooks
// that record activity. All writes are gated on the member's
// share_read_statuses opt-in (via TeamThreadActivityRepo.ListSharingTeamIDs)
// and are best-effort: they never fail the open or the send.

// TeamActivityServiceDeps wires a TeamActivityService.
type TeamActivityServiceDeps struct {
	Subscriptions port.SubscriptionRepo
	Users         port.UserRepo // anchors the entitlement gate's lazy trial grant
	Accounts      port.AccountRepo
	Threads       port.ThreadRepo
	Teams         port.TeamRepo
	Activity      port.TeamThreadActivityRepo
	Clock         port.Clock
	// SelfHosted unlocks the paywall (open-core self-hosted mode).
	SelfHosted bool
}

// TeamActivityService implements port.TeamActivityService.
type TeamActivityService struct {
	ent      entitlement
	accounts port.AccountRepo
	threads  port.ThreadRepo
	teams    port.TeamRepo
	activity port.TeamThreadActivityRepo
}

var _ port.TeamActivityService = (*TeamActivityService)(nil)

func NewTeamActivityService(d TeamActivityServiceDeps) *TeamActivityService {
	return &TeamActivityService{
		ent:      entitlement{subs: d.Subscriptions, users: d.Users, clock: d.Clock, selfHost: d.SelfHosted},
		accounts: d.Accounts,
		threads:  d.Threads,
		teams:    d.Teams,
		activity: d.Activity,
	}
}

// TeamThreadActivity resolves the caller's thread → conversation key →
// activity rows across the caller's teams. Ownership is enforced first
// (foreign threads are a 404, cross-tenant doctrine); teams the caller does
// not belong to are never queried; the repo filters rows to members with
// sharing on. No Message-ID or no teams → empty slice, never an error.
func (s *TeamActivityService) TeamThreadActivity(ctx context.Context, userID, threadID string) ([]domain.TeamThreadActivity, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	t, _, err := ownedThread(ctx, s.threads, s.accounts, userID, threadID)
	if err != nil {
		return nil, err
	}
	acts := []domain.TeamThreadActivity{}
	key, err := s.activity.EarliestRFCMessageID(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return acts, nil
	}
	teams, err := s.teams.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		rows, err := s.activity.ListByConversation(ctx, team.ID, key)
		if err != nil {
			return nil, err
		}
		acts = append(acts, rows...)
	}
	return acts, nil
}

// rfcMessageIDFromHeaders extracts the RFC 5322 Message-ID from sync headers.
// Keys arrive in canonical MIME form ("Message-Id"), but common variants are
// tolerated since providers differ.
func rfcMessageIDFromHeaders(headers map[string]string) string {
	for _, k := range []string{"Message-Id", "Message-ID", "message-id"} {
		if v, ok := headers[k]; ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// activityUpdatedPayload is the ids-only wire payload for activity.updated
// collab events. Per the SSE doctrine, events carry identifiers, never the
// row: subscribers (see web's TeamActivityChips) refetch through the normal
// authorized GET, so timestamps and any future row fields never ride the
// broadcast channel.
type activityUpdatedPayload struct {
	TeamID          string `json:"teamId"`
	UserID          string `json:"userId"`
	ConversationKey string `json:"conversationKey"`
}

// publishActivityUpdated fans one activity notification out on the team's
// topic — ids only, never the activity row itself.
func publishActivityUpdated(bus port.EventBus, a domain.TeamThreadActivity) {
	if bus == nil {
		return
	}
	payload, err := json.Marshal(activityUpdatedPayload{
		TeamID:          a.TeamID,
		UserID:          a.UserID,
		ConversationKey: a.ConversationKey,
	})
	if err != nil {
		return
	}
	bus.Publish(port.CollabEvent{Topic: "team:" + a.TeamID, Type: "activity.updated", Payload: payload})
}

// recordOpenActivity records opened_at for every team where the caller has
// share_read_statuses set. Best-effort by contract: any failure is logged
// and discarded — recording activity must never fail the open.
func (s *MailService) recordOpenActivity(ctx context.Context, userID string, t domain.Thread) {
	if s.activity == nil {
		return
	}
	key, err := s.activity.EarliestRFCMessageID(ctx, t.ID)
	if err != nil || key == "" {
		return
	}
	teamIDs, err := s.activity.ListSharingTeamIDs(ctx, userID)
	if err != nil {
		s.logger.Warn("team activity: list sharing teams", "error", err)
		return
	}
	now := s.clock.Now()
	for _, teamID := range teamIDs {
		a := domain.TeamThreadActivity{TeamID: teamID, UserID: userID, ConversationKey: key, OpenedAt: &now}
		if err := s.activity.Upsert(ctx, a); err != nil {
			s.logger.Warn("team activity: record open", "team", teamID, "error", err)
			continue
		}
		publishActivityUpdated(s.bus, a)
	}
}

// recordReplyActivity records replied_at after a delivered send for every
// team where the sender has share_read_statuses set. Best-effort: a failure
// never fails the delivery.
func (s *SyncService) recordReplyActivity(ctx context.Context, userID, threadID string, at time.Time) {
	if s.activity == nil {
		return
	}
	key, err := s.activity.EarliestRFCMessageID(ctx, threadID)
	if err != nil || key == "" {
		return
	}
	teamIDs, err := s.activity.ListSharingTeamIDs(ctx, userID)
	if err != nil {
		return
	}
	for _, teamID := range teamIDs {
		a := domain.TeamThreadActivity{TeamID: teamID, UserID: userID, ConversationKey: key, RepliedAt: &at}
		if err := s.activity.Upsert(ctx, a); err != nil {
			continue
		}
		publishActivityUpdated(s.bus, a)
	}
}
