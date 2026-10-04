// Package push implements port.PushSender: a Dispatcher that routes a
// notification to APNs (HTTP/2 + ES256 provider-token JWT), FCM v1
// (service-account JWT → OAuth2 access token), or Web Push (VAPID ES256 +
// RFC 8291 aes128gcm payload encryption) based on the device platform —
// all with stdlib crypto only (docs/architecture.md).
package push

import (
	"context"
	"fmt"
	"net/http"

	"calendium/backend/internal/config"
	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// Dispatcher fans a notification out to one device via the transport that
// matches its platform.
type Dispatcher struct {
	apns    *apnsSender
	fcm     *fcmSender
	webpush *webPushSender
}

var _ port.PushSender = (*Dispatcher)(nil)

// NewDispatcher wires the configured transports. Transports with missing
// config are left nil and fail with a descriptive error at send time, so
// partial deployments (e.g. web-push only) still boot. hc serves APNs and
// FCM (operator-fixed hosts); it may be nil, in which case
// http.DefaultClient is used (its TLS transport negotiates the HTTP/2 APNs
// requires via ALPN). Web Push never uses hc: its endpoints are
// user-registered URLs, so it gets its own netguard-dialled client.
func NewDispatcher(cfg config.Push, hc *http.Client) *Dispatcher {
	return newDispatcher(cfg, hc, newGuardedWebPushClient())
}

// newDispatcher is NewDispatcher with an injectable Web Push client, so
// package tests can deliver to loopback httptest servers.
func newDispatcher(cfg config.Push, hc, webHC *http.Client) *Dispatcher {
	if hc == nil {
		hc = http.DefaultClient
	}
	d := &Dispatcher{}
	if cfg.APNs.KeyID != "" && cfg.APNs.TeamID != "" && cfg.APNs.KeyP8 != "" {
		d.apns = newAPNsSender(cfg.APNs, hc)
	}
	if cfg.FCM.ServiceAccountJSON != "" {
		d.fcm = newFCMSender(cfg.FCM, hc)
	}
	if cfg.VAPID.PublicKey != "" && cfg.VAPID.PrivateKey != "" {
		d.webpush = newWebPushSender(cfg.VAPID, webHC)
	}
	return d
}

// Send routes by platform: iOS/macOS → APNs, Android → FCM, everything else
// (web, and the desktop shells, which register Web Push subscriptions) →
// Web Push.
func (d *Dispatcher) Send(ctx context.Context, device domain.NotificationDevice, title, body string, data map[string]string) error {
	switch device.Platform {
	case domain.PlatformIOS, domain.PlatformMacOS:
		if d.apns == nil {
			return fmt.Errorf("push: apns transport not configured (APNS_KEY_ID/APNS_TEAM_ID/APNS_KEY_P8)")
		}
		return d.apns.send(ctx, device.Token, title, body, data)
	case domain.PlatformAndroid:
		if d.fcm == nil {
			return fmt.Errorf("push: fcm transport not configured (FCM_SERVICE_ACCOUNT_JSON)")
		}
		return d.fcm.send(ctx, device.Token, title, body, data)
	case domain.PlatformWeb, domain.PlatformWindows, domain.PlatformLinux:
		if d.webpush == nil {
			return fmt.Errorf("push: web push transport not configured (VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY)")
		}
		return d.webpush.send(ctx, device.Token, title, body, data)
	default:
		return fmt.Errorf("%w: unknown device platform %q", domain.ErrValidation, device.Platform)
	}
}
