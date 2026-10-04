package service

import (
	"context"
	"fmt"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// DeviceService implements port.DeviceService. Registration is deliberately
// not paywalled: trial/paywall state changes are themselves delivered by push.
type DeviceService struct {
	devices port.DeviceRepo
	clock   port.Clock
}

var _ port.DeviceService = (*DeviceService)(nil)

func NewDeviceService(devices port.DeviceRepo, clock port.Clock) *DeviceService {
	return &DeviceService{devices: devices, clock: clock}
}

func (s *DeviceService) Register(ctx context.Context, userID string, platform domain.DevicePlatform, token string) (domain.NotificationDevice, error) {
	if _, err := domain.ParseDevicePlatform(string(platform)); err != nil {
		return domain.NotificationDevice{}, err
	}
	if token == "" {
		return domain.NotificationDevice{}, fmt.Errorf("%w: token is required", domain.ErrValidation)
	}
	if platform.UsesWebPush() {
		if err := domain.ValidateWebPushToken(token); err != nil {
			return domain.NotificationDevice{}, err
		}
	}
	return s.devices.Upsert(ctx, domain.NotificationDevice{
		ID:        newID(),
		UserID:    userID,
		Platform:  platform,
		Token:     token,
		CreatedAt: s.clock.Now(),
	})
}

func (s *DeviceService) Unregister(ctx context.Context, userID, deviceID string) error {
	d, err := s.devices.GetByID(ctx, deviceID)
	if err != nil {
		return err
	}
	if d.UserID != userID {
		return domain.ErrNotFound
	}
	return s.devices.Delete(ctx, d.ID)
}
