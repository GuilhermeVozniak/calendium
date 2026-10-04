package main

import (
	"context"
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	// Cold launch via URL on Windows/Linux: the OS passes the calendium:// link
	// as an argument; buffer it until a view takes it with TakePendingDeepLink
	// (macOS uses OnUrlOpen, which buffers the same way).
	app.consumeArgs(os.Args[1:])

	err := wails.Run(&options.App{
		Title:     "Calendium",
		Width:     1280,
		Height:    800,
		MinWidth:  980,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			// Menu-bar tray + auto-join (tray.go / scheduler.go). Started here
			// rather than inside startup so unit tests exercising startup never
			// touch the native systray loop.
			app.startDesktopExtras(ctx)
			// Daily GitHub release check (update.go); inert on dev builds and
			// paused until the frontend opts in outside demo mode.
			app.startUpdateChecks(ctx)
		},
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		// One running instance per user: a second launch (how Windows/Linux
		// deliver calendium:// links to an already-open app) forwards its argv
		// to onSecondInstance instead of opening a second window.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "app.calendium.desktop",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarHiddenInset(),
			Appearance: mac.DefaultAppearance,
			// Forward calendium:// deep links (OAuth OTT handoff, mailbox-connect
			// return) from the OS into the WebView as a "deep-link" event.
			OnUrlOpen: app.handleURL,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
