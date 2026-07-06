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
	clock port.Clock
}

var _ port.UserService = (*UserService)(nil)

func NewUserService(users port.UserRepo, clock port.Clock) *UserService {
	return &UserService{users: users, clock: clock}
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
