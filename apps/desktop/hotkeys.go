package main

import (
	"log"
	"runtime"
	"sync"

	"golang.design/x/hotkey"
)

// globalShortcutEvent is emitted to the frontend when a system-wide hotkey
// fires, payload: {"action":"compose"|"search"}. The frontend brings the
// window forward and opens the composer / command palette (App.tsx).
const globalShortcutEvent = "global-shortcut"

// shortcutSpec binds one system-wide key chord to a frontend action.
type shortcutSpec struct {
	Action string // "compose" | "search"
	Mods   []hotkey.Modifier
	Key    hotkey.Key
}

// defaultShortcuts is the registration table: Cmd+Shift+C / Cmd+Shift+K on
// macOS, Ctrl+Shift+C / Ctrl+Shift+K elsewhere. The per-OS primary modifier
// (defaultModifiers) lives in the build-tagged shim files because
// hotkey.ModCmd only exists on darwin builds.
func defaultShortcuts() []shortcutSpec {
	return []shortcutSpec{
		{Action: "compose", Mods: defaultModifiers(), Key: hotkey.KeyC},
		{Action: "search", Mods: defaultModifiers(), Key: hotkey.KeyK},
	}
}

// registerFn / unregisterFn indirect every OS-touching call through the
// platform shim (hotkeys_darwin.go dispatches onto the main GCD queue;
// hotkeys_default.go calls straight through). They are vars so hotkeys_test.go
// can inject fakes and never register a real system hotkey.
var (
	registerFn   = platformRegister
	unregisterFn = platformUnregister
)

// hotkeyManager owns the lifecycle of the system-wide shortcuts: one listen
// goroutine per spec, all torn down together by Stop. Start/Stop are
// idempotent and restartable (the Settings toggle flips them live).
type hotkeyManager struct {
	emit  func(action string)
	specs []shortcutSpec

	mu   sync.Mutex
	stop chan struct{} // non-nil while running
	wg   sync.WaitGroup
}

func newHotkeyManager(emit func(action string)) *hotkeyManager {
	return &hotkeyManager{emit: emit, specs: defaultShortcuts()}
}

// Start registers every default shortcut and begins pumping key events.
// Registration happens asynchronously on the listen goroutines (macOS
// round-trips through the main GCD queue, and blocking Wails' startup on
// that would deadlock if startup itself runs on the main thread). A combo
// already taken by another app is logged and skipped — never fatal.
// Serialized with Stop under m.mu — including Stop's wg.Wait — so a rapid
// toggle can never wg.Add while a previous run's Wait is still in flight
// (WaitGroup reuse panics).
func (m *hotkeyManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		return nil // already running
	}
	m.stop = make(chan struct{})
	for _, spec := range m.specs {
		m.wg.Add(1)
		go m.listen(spec, m.stop)
	}
	return nil
}

// Stop unregisters every successfully registered shortcut and waits for the
// listen goroutines to exit. Safe to call when not running, and safe to call
// twice. Holds m.mu across wg.Wait so a concurrent Start blocks until every
// listen goroutine has fully exited — never reusing the WaitGroup mid-Wait.
//
// Teardown note (macOS): the listen goroutines' unregister path dispatches
// synchronously onto the main GCD queue (runOnMainQueue). They never take
// m.mu, so holding it here cannot deadlock them — but Stop itself must
// never be called FROM the main GCD queue, or the unregister round-trip it
// waits on could never be serviced. Wails method bindings run off that
// queue, so SetGlobalShortcutsEnabled is safe.
func (m *hotkeyManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop == nil {
		return
	}
	close(m.stop)
	m.wg.Wait()
	m.stop = nil
}

// listen registers one shortcut and pumps its Keydown events until stop
// closes. LockOSThread: on Windows, RegisterHotKey and its message pump must
// live on the same OS thread (x/hotkey pumps internally, but pinning the
// registering goroutine is the documented-safe pattern; harmless elsewhere).
func (m *hotkeyManager) listen(spec shortcutSpec, stop <-chan struct{}) {
	defer m.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hk := hotkey.New(spec.Mods, spec.Key)
	if err := registerFn(hk); err != nil {
		// Graceful degradation, surfaced honestly: most likely another app
		// owns this combo. The other shortcuts keep working.
		log.Printf("global shortcut %q (%s) not registered: %v", spec.Action, hk, err)
		return
	}
	log.Printf("global shortcut %q registered (%s)", spec.Action, hk)
	for {
		select {
		case <-hk.Keydown():
			m.emit(spec.Action)
		case <-stop:
			if err := unregisterFn(hk); err != nil {
				log.Printf("global shortcut %q: unregister failed: %v", spec.Action, err)
			}
			return
		}
	}
}
