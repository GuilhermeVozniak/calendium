package service

import (
	"context"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// PrefsService implements port.PrefsService: cheap per-user client
// preferences. No paywall — prefs are layout, not product value.
type PrefsService struct {
	prefs port.PrefsRepo
}

var _ port.PrefsService = (*PrefsService)(nil)

func NewPrefsService(prefs port.PrefsRepo) *PrefsService {
	return &PrefsService{prefs: prefs}
}

func (s *PrefsService) GetPrefs(ctx context.Context, userID string) (domain.UserPrefs, error) {
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	return p, nil
}

func (s *PrefsService) UpdatePrefs(ctx context.Context, userID string, p domain.UserPrefs) (domain.UserPrefs, error) {
	if err := p.Validate(); err != nil {
		return domain.UserPrefs{}, err
	}
	if p.SplitOrder == nil {
		p.SplitOrder = []domain.InboxSplit{}
	}
	if err := s.prefs.Save(ctx, userID, p); err != nil {
		return domain.UserPrefs{}, err
	}
	return p, nil
}
