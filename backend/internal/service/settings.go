package service

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// SettingsService implements port.SettingsService: per-user scheduling
// preferences (time zone, working hours, working location). No paywall —
// settings are configuration, not product value gated by subscription.
type SettingsService struct {
	settings port.UserSettingsRepo
}

var _ port.SettingsService = (*SettingsService)(nil)

func NewSettingsService(settings port.UserSettingsRepo) *SettingsService {
	return &SettingsService{settings: settings}
}

// Get returns userID's settings, defaulting WorkingHours to an empty (never
// nil) slice for JSON callers. The repo itself already synthesizes a
// TimeZone: "UTC" default when no row exists.
func (s *SettingsService) Get(ctx context.Context, userID string) (domain.UserSettings, error) {
	settings, err := s.settings.Get(ctx, userID)
	if err != nil {
		return domain.UserSettings{}, err
	}
	if settings.WorkingHours == nil {
		settings.WorkingHours = []domain.AvailabilityWindow{}
	}
	return settings, nil
}

// Update validates TimeZone (must be a loadable IANA zone) and every
// WorkingHours window, then upserts.
func (s *SettingsService) Update(ctx context.Context, userID string, in domain.UserSettings) (domain.UserSettings, error) {
	if !validIANATimeZone(in.TimeZone) {
		return domain.UserSettings{}, fmt.Errorf("%w: invalid time zone %q", domain.ErrValidation, in.TimeZone)
	}
	for _, w := range in.WorkingHours {
		if err := w.Validate(); err != nil {
			return domain.UserSettings{}, err
		}
	}
	in.UserID = userID
	if in.WorkingHours == nil {
		in.WorkingHours = []domain.AvailabilityWindow{}
	}
	if err := s.settings.Upsert(ctx, in); err != nil {
		return domain.UserSettings{}, err
	}
	return in, nil
}
