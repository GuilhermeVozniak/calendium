package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
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

// UsesWebPush reports whether the platform's token is a Web Push
// subscription: the browser, and the desktop shells that register one.
func (p DevicePlatform) UsesWebPush() bool {
	return p == PlatformWeb || p == PlatformWindows || p == PlatformLinux
}

// ValidateWebPushToken checks a Web Push device token: the browser's
// PushSubscription JSON with an absolute https endpoint. Push services are
// public https origins; anything else (http, file:, a relative URL) is
// refused at registration so it is never stored as a delivery target.
func ValidateWebPushToken(token string) error {
	var sub struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(token), &sub); err != nil {
		return fmt.Errorf("%w: web push token must be a PushSubscription JSON", ErrValidation)
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("%w: web push endpoint must be an https URL", ErrValidation)
	}
	return nil
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
