package main

import (
	"context"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

// handleURL / TakePendingDeepLink implement a pull-based cold-start buffer: a
// calendium:// URL that arrives before the frontend has subscribed (cold
// launch via URL on any platform) is held in pendingURL until a view takes it
// with TakePendingDeepLink right after subscribing to the deep-link event.
// Wails v2 runs OnStartup before the page loads and its EventsEmit has no
// queue, so pushing the link at startup would lose it.
//
// LIMITATION: the warm path — runtime.EventsEmit(ctx, deepLinkEvent, url),
// taken once a view has called TakePendingDeepLink and a real Wails context
// exists — cannot be exercised here. Wails' runtime reads its Events
// implementation off ctx.Value("events"), populated only by wails.Run(); when
// it is absent, getEvents calls log.Fatalf and aborts the test binary. The
// tests below cover the runtime-independent half: buffering, consume-once and
// the route filter.

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

func TestStartup_KeepsThePendingLinkForTheFrontendToTake(t *testing.T) {
	// startup must not push the buffered link: the page is not loaded yet, so
	// an emitted event would be dropped. (Reaching runtime.EventsEmit with
	// this bare context would also abort the test binary.)
	a := NewApp()
	const link = "calendium://auth/callback?ott=abc123"
	a.handleURL(link)
	ctx := context.Background()
	a.startup(ctx)

	a.mu.Lock()
	if a.ctx != ctx {
		a.mu.Unlock()
		t.Fatalf("startup did not save the context")
	}
	a.mu.Unlock()
	if got := a.TakePendingDeepLink("auth"); got != link {
		t.Fatalf("TakePendingDeepLink(auth) = %q, want %q", got, link)
	}
}

func TestHandleURL_BuffersAfterStartupUntilTheFrontendSubscribes(t *testing.T) {
	// macOS can deliver the cold-launch URL after OnStartup set the context
	// but before any view subscribed; it must still be buffered.
	a := NewApp()
	a.startup(context.Background())
	const link = "calendium://accounts/connected?status=ok"
	a.handleURL(link)
	if got := a.TakePendingDeepLink("accounts"); got != link {
		t.Fatalf("TakePendingDeepLink(accounts) = %q, want %q", got, link)
	}
}

func TestTakePendingDeepLink_ConsumesOnce(t *testing.T) {
	a := NewApp()
	const link = "calendium://auth/callback?ott=abc123"
	a.consumeArgs([]string{link})
	if got := a.TakePendingDeepLink("auth"); got != link {
		t.Fatalf("first take = %q, want %q", got, link)
	}
	if got := a.TakePendingDeepLink("auth"); got != "" {
		t.Fatalf("second take = %q, want empty (consume-once)", got)
	}
}

func TestTakePendingDeepLink_LeavesOtherRoutesForTheirView(t *testing.T) {
	// SignInView may mount first on a cold start; a pending mailbox-connect
	// link must survive until SettingsView takes it.
	a := NewApp()
	const link = "calendium://accounts/connected?status=ok"
	a.handleURL(link)
	if got := a.TakePendingDeepLink("auth"); got != "" {
		t.Fatalf("TakePendingDeepLink(auth) = %q, want empty for an accounts link", got)
	}
	if got := a.TakePendingDeepLink("accounts"); got != link {
		t.Fatalf("TakePendingDeepLink(accounts) = %q, want %q", got, link)
	}
}

func TestTakePendingDeepLink_RejectsUnknownOrEmptyRoutes(t *testing.T) {
	a := NewApp()
	a.handleURL("calendium://auth/callback?ott=abc123")
	for _, route := range []string{"", "evil", "auth/../accounts"} {
		if got := a.TakePendingDeepLink(route); got != "" {
			t.Fatalf("TakePendingDeepLink(%q) = %q, want empty", route, got)
		}
	}
	if got := a.TakePendingDeepLink("auth"); got == "" {
		t.Fatalf("the link must still be pending after rejected takes")
	}
}

func TestTakePendingDeepLink_EmptyWhenNothingIsPending(t *testing.T) {
	a := NewApp()
	if got := a.TakePendingDeepLink("auth"); got != "" {
		t.Fatalf("TakePendingDeepLink(auth) = %q, want empty", got)
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
