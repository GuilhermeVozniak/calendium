package domain

import (
	"fmt"
	"time"
)

// DevicePlatform identifies the push transport for a registered device.
type DevicePlatform string

const (
	PlatformIOS     DevicePlatform = "ios"
	PlatformAndroid DevicePlatform = "android"
	PlatformWeb     DevicePlatform = "web"
	PlatformMacOS   DevicePlatform = "macos"
	PlatformWindows DevicePlatform = "windows"
	PlatformLinux   DevicePlatform = "linux"
)

// ParseDevicePlatform validates a platform body parameter.
func ParseDevicePlatform(s string) (DevicePlatform, error) {
	switch DevicePlatform(s) {
	case PlatformIOS, PlatformAndroid, PlatformWeb, PlatformMacOS, PlatformWindows, PlatformLinux:
		return DevicePlatform(s), nil
	}
	return "", fmt.Errorf("%w: unknown device platform %q", ErrValidation, s)
}

// NotificationDevice is a registered push target. Token is an APNs device
// token, an FCM registration token, or a Web Push subscription JSON blob.
type NotificationDevice struct {
	ID        string         `json:"id"`
	UserID    string         `json:"-"`
	Platform  DevicePlatform `json:"platform"`
	Token     string         `json:"token"`
	CreatedAt time.Time      `json:"createdAt"`
}
