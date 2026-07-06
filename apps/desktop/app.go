package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// appVersion is stamped here for now; a real release pipeline would inject it
// via -ldflags.
const appVersion = "0.1.0"

// App is the Wails-bound application struct. Its exported methods are exposed
// to the frontend as window.go.main.App.* (see frontend/src/lib/wails.ts).
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts; the context is saved so runtime
// methods can be called.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// OpenExternal opens the given URL in the system default browser.
//
// This is the desktop half of the Spotify-style billing flow
// (docs/payments.md): Stripe Checkout / the billing portal are never rendered
// in-app — the frontend calls this with the checkout URL and then polls
// GET /v1/billing/subscription until the webhook lands.
func (a *App) OpenExternal(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// GetAppVersion returns the desktop app version.
func (a *App) GetAppVersion() string {
	return appVersion
}

// NotifyBadge sets the unread-count badge on the dock / taskbar icon.
//
// Stub: platform badge APIs (NSApp dockTile on macOS, overlay icons on
// Windows) land later; keeping the method bound so the frontend contract is
// stable.
func (a *App) NotifyBadge(count int) {
	_ = count
}
