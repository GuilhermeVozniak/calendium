package main

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/options"
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

	// Update notifications (update.go): background GitHub release check.
	updates      *updateChecker
	updateCancel context.CancelFunc
}

// NewApp creates a new App application struct.
func NewApp() *App {
	a := &App{}
	// --- Task 9: global shortcuts (hotkeys.go) ---
	// Created eagerly so SetGlobalShortcutsEnabled is always safe to call;
	// nothing touches the OS until Start().
	a.hotkeys = newHotkeyManager(a.emitGlobalShortcut)
	// --- end Task 9 ---
	a.updates = newUpdateChecker(version)
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
	url, ok := normalizeDeepLink(url)
	if !ok {
		return // only the calendium scheme is ours to forward
	}
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

// deepLinkScheme prefixes every URL the OS hands us for the calendium scheme.
const deepLinkScheme = "calendium://"

// normalizeDeepLink accepts a calendium:// URL in any case (URL schemes are
// case-insensitive, and Windows/Linux argv may carry CALENDIUM://) and returns
// it with the scheme lower-cased so the frontend's calendium://auth and
// calendium://accounts routes match. The rest is forwarded verbatim (OTTs are
// case-sensitive). Any other scheme is rejected.
func normalizeDeepLink(raw string) (string, bool) {
	if len(raw) < len(deepLinkScheme) || !strings.EqualFold(raw[:len(deepLinkScheme)], deepLinkScheme) {
		return "", false
	}
	return deepLinkScheme + raw[len(deepLinkScheme):], true
}

// deepLinkFromArgs returns the first bare calendium:// argument, scheme
// normalized. Windows (NSIS registers "Calendium.exe" "%1") and Linux
// (.desktop Exec=Calendium %u) pass the opened URL as argv; macOS delivers it
// via OnUrlOpen and never does.
func deepLinkFromArgs(args []string) string {
	for _, arg := range args {
		if u, ok := normalizeDeepLink(arg); ok {
			return u
		}
	}
	return ""
}

// consumeArgs handles a cold launch via URL: main() calls it before wails.Run,
// so the link lands in pendingURL and startup flushes it once the WebView is up.
func (a *App) consumeArgs(args []string) {
	if u := deepLinkFromArgs(args); u != "" {
		a.handleURL(u)
	}
}

// onSecondInstance is the SingleInstanceLock callback: the OS started a second
// Calendium process (typically to open a calendium:// URL). Forward the link
// to this running instance and bring its window forward; a plain relaunch
// with no link just surfaces the window.
func (a *App) onSecondInstance(data options.SecondInstanceData) {
	if u := deepLinkFromArgs(data.Args); u != "" {
		a.handleURL(u)
	}
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		runtime.WindowUnminimise(ctx)
		runtime.WindowShow(ctx)
	}
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

// GetUpdateStatus returns the last update-check result. A late-mounted UI
// reads this instead of waiting up to 24 h for the next update-available
// event; before any check it is {Available: false, Current: version}.
func (a *App) GetUpdateStatus() UpdateInfo {
	return a.updates.status()
}

// SetUpdateChecksEnabled lets the frontend gate the update check: it calls
// this on boot and whenever demo mode flips, with false in demo mode (demo
// never dials out). The checker stays paused until the first true, so the
// host — which cannot read the demo flag in localStorage — never phones home
// on its own.
func (a *App) SetUpdateChecksEnabled(enabled bool) {
	a.updates.setPaused(!enabled)
}

// startUpdateChecks wires the event sink and starts the background loop
// (first check after 10 s so launch is never delayed, then every 24 h).
// Called from OnStartup (main.go); a dev build returns immediately inside run.
func (a *App) startUpdateChecks(ctx context.Context) {
	a.updates.setEmit(func(info UpdateInfo) {
		runtime.EventsEmit(ctx, updateAvailableEvent, info)
	})
	runCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.updateCancel = cancel
	a.mu.Unlock()
	go a.updates.run(runCtx, updateInitialDelay, updateInterval)
}

// stopUpdateChecks ends the loop (OnShutdown). Safe to call repeatedly.
func (a *App) stopUpdateChecks() {
	a.mu.Lock()
	cancel := a.updateCancel
	a.updateCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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
