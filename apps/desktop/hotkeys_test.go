package main

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.design/x/hotkey"
)

// The OS-touching half of the hotkey feature (Carbon/RegisterHotKey/X11
// registration) is not unit-testable — these tests swap the registerFn /
// unregisterFn shim vars for fakes so nothing here ever registers a real
// system hotkey. What IS covered: the defaultShortcuts table, and the
// manager's Start/Stop lifecycle (idempotence, graceful degradation on
// registration failure, restartability).

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func swapShims(t *testing.T, reg func(*hotkey.Hotkey) error, unreg func(*hotkey.Hotkey) error) {
	t.Helper()
	origReg, origUnreg := registerFn, unregisterFn
	registerFn, unregisterFn = reg, unreg
	t.Cleanup(func() { registerFn, unregisterFn = origReg, origUnreg })
}

func TestDefaultShortcuts_Table(t *testing.T) {
	specs := defaultShortcuts()
	if len(specs) != 2 {
		t.Fatalf("defaultShortcuts() returned %d specs, want 2", len(specs))
	}

	byAction := map[string]shortcutSpec{}
	for _, s := range specs {
		byAction[s.Action] = s
	}

	compose, ok := byAction["compose"]
	if !ok {
		t.Fatalf("no %q spec in %+v", "compose", specs)
	}
	if compose.Key != hotkey.KeyC {
		t.Errorf("compose key = %v, want hotkey.KeyC", compose.Key)
	}

	search, ok := byAction["search"]
	if !ok {
		t.Fatalf("no %q spec in %+v", "search", specs)
	}
	if search.Key != hotkey.KeyK {
		t.Errorf("search key = %v, want hotkey.KeyK", search.Key)
	}

	// Both shortcuts share the per-OS default modifier chord (Cmd+Shift on
	// macOS, Ctrl+Shift elsewhere — the primary modifier constant lives in
	// the build-tagged shim files, so assert shape + Shift here and Ctrl
	// explicitly where the constant is portable).
	for action, spec := range byAction {
		if len(spec.Mods) != 2 {
			t.Errorf("%s: %d modifiers, want 2 (primary+Shift)", action, len(spec.Mods))
		}
		hasShift := false
		for _, m := range spec.Mods {
			if m == hotkey.ModShift {
				hasShift = true
			}
		}
		if !hasShift {
			t.Errorf("%s: modifiers %v missing ModShift", action, spec.Mods)
		}
		if runtime.GOOS != "darwin" {
			hasCtrl := false
			for _, m := range spec.Mods {
				if m == hotkey.ModCtrl {
					hasCtrl = true
				}
			}
			if !hasCtrl {
				t.Errorf("%s: modifiers %v missing ModCtrl on %s", action, spec.Mods, runtime.GOOS)
			}
		}
	}
}

func TestHotkeyManager_StartIsIdempotent_AndStopUnregisters(t *testing.T) {
	var regs, unregs atomic.Int32
	swapShims(t,
		func(*hotkey.Hotkey) error { regs.Add(1); return nil },
		func(*hotkey.Hotkey) error { unregs.Add(1); return nil },
	)

	m := newHotkeyManager(func(string) {})
	want := int32(len(m.specs))

	if err := m.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := m.Start(); err != nil { // second Start must be a no-op
		t.Fatalf("second Start() = %v", err)
	}

	waitUntil(t, "all hotkeys to register", func() bool { return regs.Load() >= want })
	// Let any (incorrect) duplicate registrations from the second Start land.
	time.Sleep(50 * time.Millisecond)
	if got := regs.Load(); got != want {
		t.Fatalf("registered %d hotkeys, want exactly %d (double Start must not re-register)", got, want)
	}

	m.Stop()
	if got := unregs.Load(); got != want {
		t.Fatalf("unregistered %d hotkeys, want %d", got, want)
	}
	m.Stop() // second Stop must not panic or block
	if got := unregs.Load(); got != want {
		t.Fatalf("second Stop unregistered again: %d, want %d", got, want)
	}
}

func TestHotkeyManager_RegistrationFailureDegradesGracefully(t *testing.T) {
	// One combo is "taken by another app": that hotkey must be skipped
	// (logged, not fatal) while the other keeps working, and Stop must only
	// unregister what actually registered — and must not hang on the failed one.
	var attempts, unregs atomic.Int32
	swapShims(t,
		func(hk *hotkey.Hotkey) error {
			attempts.Add(1)
			if attempts.Load() == 1 {
				return errors.New("RegisterEventHotKey failed: combo already in use")
			}
			return nil
		},
		func(*hotkey.Hotkey) error { unregs.Add(1); return nil },
	)

	m := newHotkeyManager(func(string) {})
	if err := m.Start(); err != nil {
		t.Fatalf("Start() = %v (a failed registration must not fail Start)", err)
	}
	waitUntil(t, "both registration attempts", func() bool {
		return attempts.Load() == int32(len(m.specs))
	})

	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung after a failed registration")
	}
	if got := unregs.Load(); got != 1 {
		t.Fatalf("unregistered %d hotkeys, want 1 (only the successfully registered one)", got)
	}
}

func TestHotkeyManager_RapidConcurrentToggleNeverRacesWaitGroup(t *testing.T) {
	// A rapid Settings toggle (or concurrent bindings) must never let a new
	// Start reuse the WaitGroup while a previous Stop is still in wg.Wait —
	// that reuse panics ("WaitGroup is reused before previous Wait has
	// returned"). Slow unregister widens the old race window.
	swapShims(t,
		func(*hotkey.Hotkey) error { return nil },
		func(*hotkey.Hotkey) error { time.Sleep(2 * time.Millisecond); return nil },
	)

	m := newHotkeyManager(func(string) {})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				_ = m.Start()
				m.Stop()
			}
		}()
	}
	wg.Wait()
	m.Stop()
}

func TestHotkeyManager_StopBeforeStartIsSafe_AndRestartWorks(t *testing.T) {
	var regs atomic.Int32
	swapShims(t,
		func(*hotkey.Hotkey) error { regs.Add(1); return nil },
		func(*hotkey.Hotkey) error { return nil },
	)

	m := newHotkeyManager(func(string) {})
	m.Stop() // never started: must be a no-op

	want := int32(len(m.specs))
	if err := m.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	waitUntil(t, "first registration round", func() bool { return regs.Load() == want })
	m.Stop()

	// The Settings toggle can flip off and back on: Start after Stop must
	// register everything again.
	if err := m.Start(); err != nil {
		t.Fatalf("restart Start() = %v", err)
	}
	waitUntil(t, "re-registration after restart", func() bool { return regs.Load() == 2*want })
	m.Stop()
}
