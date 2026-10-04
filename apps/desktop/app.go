package main

import (
	"context"
	"log"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// version is the desktop build version. release.yml stamps it with
// -ldflags "-X main.version=X.Y.Z" (a plain release semver) or
// 0.0.0-dev.<sha7> for dry runs; source builds report "dev". update.go only
// checks for updates when this is a plain X.Y.Z.
var version = "dev"

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

	// --- Task 9: global shortcuts (hotkeys.go) ---
	hotkeys *hotkeyManager
	// --- end Task 9 ---
	// Menu-bar tray + auto-join (M2.6 Tasks 10-11; tray.go / scheduler.go).
	tray     *trayManager
	autoJoin *autoJoinScheduler
}

// NewApp creates a new App application struct.
func NewApp() *App {
	a := &App{}
	// --- Task 9: global shortcuts (hotkeys.go) ---
	// Created eagerly so SetGlobalShortcutsEnabled is always safe to call;
	// nothing touches the OS until Start().
	a.hotkeys = newHotkeyManager(a.emitGlobalShortcut)
	// --- end Task 9 ---
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

// GetAppVersion returns the desktop app version (see `version`).
func (a *App) GetAppVersion() string {
	return version
}

// --- Task 9: global shortcuts (hotkeys.go) ---

// SetGlobalShortcutsEnabled starts or stops the system-wide hotkeys
// (Cmd/Ctrl+Shift+C compose, Cmd/Ctrl+Shift+K search). The persisted toggle
// lives in the frontend's localStorage, so the frontend calls this on boot
// with the stored value (that is how "startup starts it when enabled"
// happens — the host can't read localStorage) and again whenever the
// Settings toggle flips. Registration failures (combo taken by another app)
// are logged and degrade gracefully — see hotkeys.go.
func (a *App) SetGlobalShortcutsEnabled(enabled bool) {
	if enabled {
		if err := a.hotkeys.Start(); err != nil {
			log.Printf("global shortcuts: %v", err)
		}
		return
	}
	a.hotkeys.Stop()
}

// emitGlobalShortcut forwards a fired hotkey to the frontend. A press that
// races app startup (before the WebView context exists) is dropped — there
// is no UI to focus yet.
func (a *App) emitGlobalShortcut(action string) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, globalShortcutEvent, map[string]string{"action": action})
}

// --- end Task 9 ---
