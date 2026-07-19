package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// appVersion is stamped here for now; a real release pipeline would inject it
// via -ldflags.
const appVersion = "0.1.0"

// deepLinkEvent is emitted to the frontend (runtime.EventsOn) whenever the OS
// opens a calendium:// URL — the OAuth one-time-token handoff after social
// sign-in in the system browser, and the mailbox-connect return.
const deepLinkEvent = "deep-link"

// App is the Wails-bound application struct. Its exported methods are exposed
// to the frontend as window.go.main.App.* (see frontend/src/lib/wails.ts).
type App struct {
	ctx context.Context

	mu         sync.Mutex
	pendingURL string

	// Menu-bar tray + auto-join (M2.6 Tasks 10-11; tray.go / scheduler.go).
	tray     *trayManager
	autoJoin *autoJoinScheduler
}

// NewApp creates a new App application struct.
func NewApp() *App {
	a := &App{}
	a.initDesktopExtras()
	return a
}

// startup is called when the app starts; the context is saved so runtime
// methods can be called. A deep link can arrive before the WebView is ready
// (cold launch via URL), so we flush anything buffered once the context exists.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	pending := a.pendingURL
	a.pendingURL = ""
	a.mu.Unlock()
	if pending != "" {
		runtime.EventsEmit(ctx, deepLinkEvent, pending)
	}
}

// handleURL is the macOS URL-scheme handler (wired as mac.Options.OnUrlOpen).
// It forwards the opened calendium:// URL to the frontend as a "deep-link"
// event, buffering it when the WebView context isn't ready yet. Unexported so
// it isn't bound into the JS surface — the OS invokes it, not the frontend.
func (a *App) handleURL(url string) {
	a.mu.Lock()
	ctx := a.ctx
	if ctx == nil {
		a.pendingURL = url
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	runtime.EventsEmit(ctx, deepLinkEvent, url)
}

// OpenExternal opens the given URL in the system default browser.
//
// Used for the Spotify-style web billing flow (docs/payments.md) and to hand
// social sign-in / mailbox OAuth off to the user's real browser (Google blocks
// OAuth inside embedded WebViews). For social sign-in the browser round-trips
// back via a calendium:// deep link (see HandleURL).
func (a *App) OpenExternal(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// GetAppVersion returns the desktop app version.
func (a *App) GetAppVersion() string {
	return appVersion
}
