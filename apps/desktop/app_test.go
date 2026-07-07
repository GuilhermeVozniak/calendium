package main

import (
	"context"
	"testing"
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
// buffer/flag logic in handleURL and startup. There is no separate
// URL-parsing step to unit test either — handleURL treats the deep link as
// an opaque string throughout (buffered, then forwarded verbatim); the tests
// below confirm it round-trips a realistic calendium:// URL unchanged.

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

func TestGetAppVersion(t *testing.T) {
	a := NewApp()
	if got := a.GetAppVersion(); got != appVersion {
		t.Fatalf("GetAppVersion() = %q, want %q", got, appVersion)
	}
}
