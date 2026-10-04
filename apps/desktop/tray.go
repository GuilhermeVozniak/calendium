package main

// Menu-bar tray lifecycle (Task 10). Wails v2 has no built-in systray (it
// lands in v3), so the tray runs on github.com/energye/systray — the
// maintained fork whose external-loop mode coexists with Wails owning main.
// Started from OnStartup, stopped in OnShutdown (main.go).
//
// The tray is TEXT-ONLY by design: energye/systray menus are native text
// items, so a graphical mini month calendar (Fantastical-style NSPopover) is
// not possible under Wails v2 and is deferred to the Wails v3 migration (see
// docs/feature-map.md).

import (
	"context"
	_ "embed"
	"sync"
	"time"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// trayActionEvent is emitted to the frontend when a tray menu row needs the
// main window (payload: "open-calendar" | "compose").
const trayActionEvent = "tray-action"

// trayTickInterval refreshes the countdown title between event pushes.
const trayTickInterval = 30 * time.Second

// Monochrome template icon (macOS renders it correctly on dark menu bars via
// SetTemplateIcon).
//
//go:embed build/appicon-tray.png
var trayIconPNG []byte

// trayManager owns tray state and rendering. The systray hooks (setTitle,
// rebuild) stay nil until the tray is actually running, so state updates are
// safe (and unit-testable) before/without a real menu bar.
type trayManager struct {
	mu      sync.Mutex
	events  []TrayEvent
	openURL func(string)
	emit    func(action string)
	quit    func()

	setTitle func(string)
	rebuild  func()
	stopLoop func()
	stopTick chan struct{}
}

func newTrayManager() *trayManager {
	return &trayManager{}
}

// SetEvents replaces the upcoming-event feed and re-renders title + menu.
func (t *trayManager) SetEvents(evs []TrayEvent) {
	t.mu.Lock()
	t.events = append([]TrayEvent(nil), evs...)
	t.mu.Unlock()
	t.refresh()
}

func (t *trayManager) snapshot() (evs []TrayEvent, setTitle func(string), rebuild func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TrayEvent(nil), t.events...), t.setTitle, t.rebuild
}

// refresh re-renders the countdown title and the whole menu.
func (t *trayManager) refresh() {
	evs, setTitle, rebuild := t.snapshot()
	if setTitle != nil {
		setTitle(trayTitle(evs, time.Now()))
	}
	if rebuild != nil {
		rebuild()
	}
}

// tick updates just the countdown title (called every 30s by the ticker).
func (t *trayManager) tick(now time.Time) {
	evs, setTitle, _ := t.snapshot()
	if setTitle != nil {
		setTitle(trayTitle(evs, now))
	}
}

// onReady runs once the systray loop is up: sets the icon, wires the real
// systray render hooks, and paints the initial state.
func (t *trayManager) onReady() {
	if len(trayIconPNG) > 0 {
		systray.SetTemplateIcon(trayIconPNG, trayIconPNG)
	}
	systray.SetTooltip("Calendium")
	t.mu.Lock()
	t.setTitle = systray.SetTitle
	t.rebuild = t.rebuildMenu
	t.mu.Unlock()
	t.refresh()
}

// rebuildMenu repaints the native menu:
// [countdown, disabled] [next 5 events] [Join <title>] [Compose] [Open Calendar] [Quit]
func (t *trayManager) rebuildMenu() {
	t.mu.Lock()
	evs := append([]TrayEvent(nil), t.events...)
	openURL, emit, quit := t.openURL, t.emit, t.quit
	t.mu.Unlock()
	now := time.Now()

	systray.ResetMenu()

	head := trayTitle(evs, now)
	if head == "" {
		head = "No upcoming events"
	}
	headItem := systray.AddMenuItem(head, "")
	headItem.Disable()
	systray.AddSeparator()

	rows := menuRows(evs, now, trayMenuMaxEvents)
	for _, row := range rows {
		item := systray.AddMenuItem(row, "")
		item.Click(func() {
			if emit != nil {
				emit("open-calendar")
			}
		})
	}
	if len(rows) > 0 {
		systray.AddSeparator()
	}

	if ev, ok := nextJoinable(evs, now); ok {
		url := ev.JoinURL
		join := systray.AddMenuItem("Join "+ev.Title, url)
		join.Click(func() {
			if openURL != nil {
				openURL(url)
			}
		})
		systray.AddSeparator()
	}

	compose := systray.AddMenuItem("Compose", "")
	compose.Click(func() {
		if emit != nil {
			emit("compose")
		}
	})
	openCal := systray.AddMenuItem("Open Calendar", "")
	openCal.Click(func() {
		if emit != nil {
			emit("open-calendar")
		}
	})
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Calendium", "")
	quitItem.Click(func() {
		if quit != nil {
			quit()
		}
	})
}

// startTicker keeps the countdown title honest between event pushes.
func (t *trayManager) startTicker(interval time.Duration) {
	stop := make(chan struct{})
	t.mu.Lock()
	t.stopTick = stop
	t.mu.Unlock()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				t.tick(now)
			case <-stop:
				return
			}
		}
	}()
}

// stop tears the tray down (OnShutdown).
func (t *trayManager) stop() {
	t.mu.Lock()
	stopTick, stopLoop := t.stopTick, t.stopLoop
	t.stopTick, t.stopLoop = nil, nil
	t.setTitle, t.rebuild = nil, nil
	t.mu.Unlock()
	if stopTick != nil {
		close(stopTick)
	}
	if stopLoop != nil {
		stopLoop()
	}
}

// initDesktopExtras constructs the tray manager and auto-join scheduler
// (pure state — no systray yet). Called from NewApp (app.go).
func (a *App) initDesktopExtras() {
	a.tray = newTrayManager()
	a.autoJoin = newAutoJoinScheduler(time.Now, func(d time.Duration) <-chan time.Time {
		return time.After(d)
	})
}

// startDesktopExtras wires runtime-backed actions and starts the systray
// external loop + countdown ticker. Called from startup (app.go).
func (a *App) startDesktopExtras(ctx context.Context) {
	openURL := func(u string) { runtime.BrowserOpenURL(ctx, u) }
	t := a.tray
	t.mu.Lock()
	t.openURL = openURL
	t.emit = func(action string) { runtime.EventsEmit(ctx, trayActionEvent, action) }
	t.quit = func() { runtime.Quit(ctx) }
	t.mu.Unlock()
	a.autoJoin.setSinks(openURL, func(ev TrayEvent) {
		runtime.EventsEmit(ctx, autoJoinedEvent, ev)
	})

	start, stop := systray.RunWithExternalLoop(t.onReady, func() {})
	t.mu.Lock()
	t.stopLoop = stop
	t.mu.Unlock()
	start()
	t.startTicker(trayTickInterval)
}

// shutdown is the Wails OnShutdown hook (main.go): stops the tray loop,
// countdown ticker, any pending auto-join timer, and the update-check loop.
func (a *App) shutdown(_ context.Context) {
	a.tray.stop()
	a.autoJoin.SetConfig(false, 0)
	a.stopUpdateChecks()
}

// SetUpcomingEvents is bound to the frontend (window.go.main.App): it receives
// the JSON feed of upcoming events (frontend/src/lib/tray.ts pushes on every
// events refetch + a 60s interval) and fans it out to the tray menu and the
// auto-join scheduler. Bad JSON returns an error and leaves state unchanged.
func (a *App) SetUpcomingEvents(payload string) error {
	evs, err := decodeTrayEvents(payload)
	if err != nil {
		return err
	}
	a.tray.SetEvents(evs)
	a.autoJoin.SetEvents(evs)
	return nil
}
