package main

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

// handleURL / startup implement a small pre-startup deep-link buffer: a
// calendium:// URL that arrives before the WebView is ready (cold launch via
// URL) is held in pendingURL and flushed once startup() supplies a real
// Wails context.
//
// LIMITATION: the actual flush — runtime.EventsEmit(ctx, deepLinkEvent, url)
// — cannot be exercised in a unit test here. Wails' runtime package reads its
// Events implementation off ctx.Value("events") (see
// github.com/wailsapp/wails/v2/pkg/runtime/runtime.go's getEvents), which is
// only populated by wails.Run() during a real application lifecycle. When
// it's absent, getEvents calls log.Fatalf, which os.Exit(1)s the *entire test
// binary* — not a reportable test failure, an unrecoverable process abort.
// Duck-typing a fake frontend.Events value into the context would sidestep
// that (Go interfaces are satisfied structurally, and frontend.Events is
// unexported but not un-satisfiable from outside its package), but doing so
// is exactly "stub the entire Wails runtime," which this task says not to
// do — so that half is intentionally left uncovered.
//
// What IS covered below is the runtime-independent half: the pendingURL
// buffer/flag logic in handleURL and startup. Beyond lower-casing the scheme
// (normalizeDeepLink) handleURL treats the deep link as an opaque string
// (buffered, then forwarded verbatim); the tests below confirm it round-trips
// a realistic calendium:// URL unchanged.

func TestHandleURL_BuffersBeforeStartup(t *testing.T) {
	a := NewApp()
	const link = "calendium://auth/callback?ott=abc123"
	a.handleURL(link)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx != nil {
		t.Fatalf("ctx = %v, want nil (handleURL must not touch ctx before startup)", a.ctx)
	}
	if a.pendingURL != link {
		t.Fatalf("pendingURL = %q, want %q", a.pendingURL, link)
	}
}

func TestHandleURL_LastLinkWinsBeforeStartup(t *testing.T) {
	// pendingURL is a single string slot, not a queue: a second deep link
	// arriving before startup overwrites the first rather than accumulating.
	a := NewApp()
	a.handleURL("calendium://first")
	a.handleURL("calendium://second")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingURL != "calendium://second" {
		t.Fatalf("pendingURL = %q, want the most recently buffered link", a.pendingURL)
	}
}

func TestHandleURL_NoBufferingWithoutACall(t *testing.T) {
	a := NewApp()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingURL != "" {
		t.Fatalf("pendingURL = %q, want empty for a freshly constructed App", a.pendingURL)
	}
}

func TestStartup_SetsContextAndSkipsEmitWhenNothingIsPending(t *testing.T) {
	a := NewApp()
	ctx := context.Background()
	// No deep link is pending, so startup must NOT reach
	// runtime.EventsEmit(ctx, ...) — ctx here carries none of Wails' internal
	// state, and that call would os.Exit(1) the test binary. Not crashing,
	// and pendingURL staying empty, is itself the assertion that the
	// pending-URL-empty short-circuit in startup works.
	a.startup(ctx)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx != ctx {
		t.Fatalf("startup did not save the context")
	}
	if a.pendingURL != "" {
		t.Fatalf("pendingURL = %q, want empty", a.pendingURL)
	}
}

func TestGetAppVersion_ReturnsTheLinkerStampedVariable(t *testing.T) {
	prev := version
	version = "1.2.3"
	t.Cleanup(func() { version = prev })
	a := NewApp()
	if got := a.GetAppVersion(); got != "1.2.3" {
		t.Fatalf("GetAppVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestVersion_DefaultsToDevForSourceBuilds(t *testing.T) {
	if version != "dev" {
		t.Fatalf("version = %q, want \"dev\" (release.yml stamps it with -ldflags \"-X main.version=X.Y.Z\")", version)
	}
}

func TestGetUpdateStatus_DefaultBeforeAnyCheck(t *testing.T) {
	a := NewApp()
	got := a.GetUpdateStatus()
	want := UpdateInfo{Available: false, Current: version}
	if got != want {
		t.Fatalf("GetUpdateStatus() = %+v, want %+v", got, want)
	}
}

func TestStartStopUpdateChecks_DevBuildIsInertAndStoppable(t *testing.T) {
	// version is "dev" under `go test`, so the loop must exit without ever
	// touching the network or emitting; stop must be safe to call twice.
	a := NewApp()
	a.startUpdateChecks(context.Background())
	a.mu.Lock()
	cancel := a.updateCancel
	a.mu.Unlock()
	if cancel == nil {
		t.Fatal("startUpdateChecks did not record a cancel func")
	}
	a.stopUpdateChecks()
	a.stopUpdateChecks()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.updateCancel != nil {
		t.Fatal("stopUpdateChecks must clear the cancel func")
	}
}

func TestSetUpdateChecksEnabled_GatesTheChecker(t *testing.T) {
	// The host cannot read the frontend's demo-mode flag (localStorage), so
	// update checks stay paused until the frontend opts in outside demo mode.
	a := NewApp()
	if !a.updates.isPaused() {
		t.Fatal("a fresh App must hold update checks until the frontend opts in")
	}
	a.SetUpdateChecksEnabled(true)
	if a.updates.isPaused() {
		t.Fatal("SetUpdateChecksEnabled(true) must resume update checks")
	}
	a.SetUpdateChecksEnabled(false)
	if !a.updates.isPaused() {
		t.Fatal("SetUpdateChecksEnabled(false) (demo mode) must pause update checks")
	}
}

func TestDeepLinkFromArgs_PicksTheFirstCalendiumURLFromMixedArgv(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"url after flags", []string{"--no-sandbox", "calendium://auth/callback?ott=abc123"}, "calendium://auth/callback?ott=abc123"},
		{"first of two", []string{"calendium://first", "calendium://second"}, "calendium://first"},
		{"scheme is case-insensitive and normalized", []string{"CALENDIUM://accounts/connected?status=ok"}, "calendium://accounts/connected?status=ok"},
		{"mixed-case scheme", []string{"Calendium://auth/callback?ott=AbC"}, "calendium://auth/callback?ott=AbC"},
		{"other schemes rejected", []string{"calendiumx://auth", "https://calendium://x", "javascript:calendium://x"}, ""},
		{"no link", []string{"https://example.com", "-x"}, ""},
		{"embedded, not a bare arg", []string{"--url=calendium://x"}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		if got := deepLinkFromArgs(tc.args); got != tc.want {
			t.Errorf("%s: deepLinkFromArgs(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestConsumeArgs_BuffersAColdLaunchURLBeforeStartup(t *testing.T) {
	a := NewApp()
	a.consumeArgs([]string{"--flag", "calendium://auth/callback?ott=abc123"})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingURL != "calendium://auth/callback?ott=abc123" {
		t.Fatalf("pendingURL = %q", a.pendingURL)
	}
}

func TestOnSecondInstance_ForwardsTheLinkAndIgnoresPlainRelaunches(t *testing.T) {
	// Pre-startup (ctx nil) so handleURL buffers instead of reaching
	// runtime.EventsEmit, and WindowShow is skipped (no Wails context).
	a := NewApp()
	a.onSecondInstance(options.SecondInstanceData{Args: []string{"Calendium.exe", "calendium://accounts/connected?status=ok"}, WorkingDirectory: "C:\\"})
	a.mu.Lock()
	got := a.pendingURL
	a.mu.Unlock()
	if got != "calendium://accounts/connected?status=ok" {
		t.Fatalf("pendingURL = %q", got)
	}

	b := NewApp()
	b.onSecondInstance(options.SecondInstanceData{Args: []string{"Calendium.exe"}})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingURL != "" {
		t.Fatalf("a relaunch without a link must not buffer anything, got %q", b.pendingURL)
	}
}

func TestHandleURL_NormalizesTheSchemeAndDropsOtherSchemes(t *testing.T) {
	a := NewApp()
	a.handleURL("CALENDIUM://auth/callback?ott=AbC")
	a.mu.Lock()
	got := a.pendingURL
	a.mu.Unlock()
	if got != "calendium://auth/callback?ott=AbC" {
		t.Fatalf("pendingURL = %q, want the scheme lower-cased and the rest verbatim", got)
	}

	b := NewApp()
	for _, other := range []string{"https://example.com", "javascript:alert(1)", "calendiumx://auth", ""} {
		b.handleURL(other)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingURL != "" {
		t.Fatalf("a non-calendium URL must be dropped, got %q", b.pendingURL)
	}
}
