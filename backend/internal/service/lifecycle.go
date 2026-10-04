package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// UserLifecycleDeps wires the account-deletion service.
type UserLifecycleDeps struct {
	Users         port.UserRepo
	Teams         port.TeamRepo
	Delegations   port.DelegationRepo
	Subscriptions port.SubscriptionRepo
	// Payments cancels a live billing subscription before the rows go; nil
	// on self-hosted instances (no biller), which skips the cancel step.
	Payments port.Payments
	Tx       port.TxRunner
	Clock    port.Clock
	Logger   *slog.Logger // optional; defaults to slog.Default()
}

// UserLifecycleService implements port.UserLifecycleService: account
// deletion behind DELETE /v1/internal/users/{id}. Order: team-ownership
// check → payment-provider cancel (not transactional; a failure aborts
// before anything is deleted; the provider's customer record is retained by
// the merchant of record) → one transaction that re-checks teams, revokes
// delegations both ways, deletes sole-member teams, writes the deleted-user
// tombstone (so requireAuth answers 401 rather than re-provisioning) and
// deletes the users row —
// every other owned table cascades (migration 0029 + TestPurgeCascadeCoverage).
// Provider OAuth tokens die with their rows; there is no provider-side
// revocation.
type UserLifecycleService struct {
	d UserLifecycleDeps
}

var _ port.UserLifecycleService = (*UserLifecycleService)(nil)

func NewUserLifecycleService(d UserLifecycleDeps) *UserLifecycleService {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &UserLifecycleService{d: d}
}

// teamPlan is the outcome of classifying one user's memberships.
type teamPlan struct {
	soleMember []string         // teams the user is the only member of → deleted with the account
	blocking   []domain.TeamRef // teams where the user is the only owner but others remain → refuse
}

func (s *UserLifecycleService) planTeams(ctx context.Context, userID string) (teamPlan, error) {
	var plan teamPlan
	memberships, err := s.d.Teams.ListMemberships(ctx, userID)
	if err != nil {
		return plan, err
	}
	for _, m := range memberships {
		members, err := s.d.Teams.ListMembers(ctx, m.TeamID)
		if err != nil {
			return plan, err
		}
		if len(members) <= 1 {
			plan.soleMember = append(plan.soleMember, m.TeamID)
			continue
		}
		if m.Role != domain.TeamRoleOwner {
			continue
		}
		otherOwners := 0
		for _, other := range members {
			if other.UserID != userID && other.Role == domain.TeamRoleOwner {
				otherOwners++
			}
		}
		if otherOwners > 0 {
			continue
		}
		team, err := s.d.Teams.GetByID(ctx, m.TeamID)
		if err != nil {
			return plan, err
		}
		plan.blocking = append(plan.blocking, domain.TeamRef{ID: team.ID, Name: team.Name})
	}
	return plan, nil
}

// subscriptionNeedsCancel: a provider subscription exists (id present) and
// is in a state the provider would keep billing or could resume.
func subscriptionNeedsCancel(sub domain.Subscription) bool {
	if sub.BillingSubscriptionID == "" {
		return false
	}
	switch sub.Status {
	case domain.SubscriptionActive, domain.SubscriptionPastDue, domain.SubscriptionPaused, domain.SubscriptionTrialing:
		return true
	}
	return false
}

// Purge implements port.UserLifecycleService.
func (s *UserLifecycleService) Purge(ctx context.Context, userID string) (port.PurgeReport, error) {
	var report port.PurgeReport
	if _, err := s.d.Users.GetByID(ctx, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return report, nil // already purged (or never provisioned): idempotent
		}
		return report, err
	}
	plan, err := s.planTeams(ctx, userID)
	if err != nil {
		return report, err
	}
	if len(plan.blocking) > 0 {
		return report, &domain.OwnsTeamsError{Teams: plan.blocking}
	}
	if s.d.Payments != nil && s.d.Subscriptions != nil {
		sub, err := s.d.Subscriptions.GetByUserID(ctx, userID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			// never subscribed
		case err != nil:
			return report, err
		case subscriptionNeedsCancel(sub):
			if err := s.d.Payments.CancelSubscription(ctx, sub.BillingSubscriptionID, true); err != nil {
				return report, fmt.Errorf("%w: cancel subscription: %v", domain.ErrBillingUnavailable, err)
			}
			report.SubscriptionCanceled = true
		}
	}
	err = s.d.Tx.RunInTx(ctx, func(ctx context.Context) error {
		// Race guard: a co-owner may have left between the check and the tx.
		plan, err := s.planTeams(ctx, userID)
		if err != nil {
			return err
		}
		if len(plan.blocking) > 0 {
			return &domain.OwnsTeamsError{Teams: plan.blocking}
		}
		grants, err := s.d.Delegations.ListByUser(ctx, userID)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now().UTC()
		for _, g := range grants {
			if g.Status == domain.DelegationRevoked {
				continue
			}
			g.Status = domain.DelegationRevoked
			g.RevokedAt = &now
			if err := s.d.Delegations.Update(ctx, g); err != nil {
				return err
			}
		}
		for _, teamID := range plan.soleMember {
			if err := s.d.Teams.Delete(ctx, teamID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			report.TeamsDeleted++
		}
		// Tombstone first, same tx: requireAuth's upsert can never
		// re-create the row from a still-valid access token.
		if err := s.d.Users.Tombstone(ctx, userID); err != nil {
			return err
		}
		return s.d.Users.Delete(ctx, userID)
	})
	if err != nil {
		return port.PurgeReport{}, err
	}
	s.d.Logger.Info("user purged",
		"userId", userID, "teamsDeleted", report.TeamsDeleted, "subscriptionCanceled", report.SubscriptionCanceled)
	return report, nil
}
