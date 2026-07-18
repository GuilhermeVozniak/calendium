package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// maxGuestFreeBusyEmails and maxGuestFreeBusySpan bound GuestFreeBusy queries
// (the Find-a-Time grid): enough for a small-group scheduling poll without
// turning one request into an unbounded provider-API fan-out.
const (
	maxGuestFreeBusyEmails = 20
	maxGuestFreeBusySpan   = 14 * 24 * time.Hour
)

// GuestFreeBusy answers the Find-a-Time grid: for each of userID's connected
// provider accounts, ask the provider for busy intervals of req.Emails
// between req.From and req.To, merging results across accounts/providers
// (result keys are lowercased emails). A connected account whose token can't
// be resolved or whose provider call fails is skipped rather than failing
// the whole request — one broken grant shouldn't blank the entire grid.
func (s *SchedulingService) GuestFreeBusy(ctx context.Context, userID string, req port.FreeBusyRequest) (map[string][]domain.BusyInterval, error) {
	if err := s.ent.require(ctx, userID); err != nil {
		return nil, err
	}
	if len(req.Emails) == 0 || len(req.Emails) > maxGuestFreeBusyEmails {
		return nil, fmt.Errorf("%w: emails must include between 1 and %d addresses", domain.ErrValidation, maxGuestFreeBusyEmails)
	}
	if !req.To.After(req.From) {
		return nil, fmt.Errorf("%w: to must be after from", domain.ErrValidation)
	}
	if req.To.Sub(req.From) > maxGuestFreeBusySpan {
		return nil, fmt.Errorf("%w: span must not exceed %d days", domain.ErrValidation, int(maxGuestFreeBusySpan/(24*time.Hour)))
	}

	accounts, err := s.accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]domain.BusyInterval, len(req.Emails))
	for _, acct := range accounts {
		provider, ok := s.cal[acct.Provider]
		if !ok {
			continue
		}
		token, err := s.tokens.accessToken(ctx, acct)
		if err != nil {
			continue // best-effort: a broken grant shouldn't fail the whole grid
		}
		busy, err := provider.FreeBusy(ctx, token, req.Emails, req.From, req.To)
		if err != nil {
			continue
		}
		for email, intervals := range busy {
			key := strings.ToLower(email)
			out[key] = append(out[key], intervals...)
		}
	}
	return out, nil
}
