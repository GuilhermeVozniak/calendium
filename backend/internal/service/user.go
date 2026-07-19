package service

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// UserService implements port.UserService.
type UserService struct {
	users port.UserRepo
	prefs port.UserPreferencesRepo
	clock port.Clock
}

var _ port.UserService = (*UserService)(nil)

func NewUserService(users port.UserRepo, prefs port.UserPreferencesRepo, clock port.Clock) *UserService {
	return &UserService{users: users, prefs: prefs, clock: clock}
}

func (s *UserService) EnsureUser(ctx context.Context, id port.Identity) (domain.User, error) {
	if id.Subject == "" {
		return domain.User{}, fmt.Errorf("%w: identity has no subject", domain.ErrUnauthorized)
	}
	u := domain.User{
		ID:        id.Subject,
		Email:     id.Email,
		CreatedAt: s.clock.Now(),
	}
	if id.Name != "" {
		u.Name = ptr(id.Name)
	}
	if id.AvatarURL != "" {
		u.AvatarURL = ptr(id.AvatarURL)
	}
	return s.users.Upsert(ctx, u)
}

func (s *UserService) GetUser(ctx context.Context, userID string) (domain.User, error) {
	return s.users.GetByID(ctx, userID)
}

// GetPreferences returns the cross-device preference document (named theme),
// defaulting to port.DefaultTheme when the user has never saved one.
func (s *UserService) GetPreferences(ctx context.Context, userID string) (port.UserPreferences, error) {
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return port.UserPreferences{}, err
	}
	if p.Theme == "" {
		p.Theme = port.DefaultTheme
	}
	return p, nil
}

// UpdatePreferences validates the theme against the allowed set and upserts
// the document. No paywall — themes are polish, not product value.
func (s *UserService) UpdatePreferences(ctx context.Context, userID string, p port.UserPreferences) (port.UserPreferences, error) {
	if !port.ValidTheme(p.Theme) {
		return port.UserPreferences{}, fmt.Errorf("%w: unknown theme %q", domain.ErrValidation, p.Theme)
	}
	if err := s.prefs.Put(ctx, userID, p); err != nil {
		return port.UserPreferences{}, err
	}
	return p, nil
}
