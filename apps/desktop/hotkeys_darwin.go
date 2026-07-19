//go:build darwin

package main

/*
#include <stdint.h>

// Defined in hotkeys_darwin.c: dispatch_async_f onto the main GCD queue,
// bouncing back into Go via the exported trampoline below.
void calendiumDispatchMain(uintptr_t handle);
*/
import "C"

import (
	"runtime/cgo"

	"golang.design/x/hotkey"
)

// macOS uses Cmd+Shift as the primary chord (Superhuman-style).
func defaultModifiers() []hotkey.Modifier {
	return []hotkey.Modifier{hotkey.ModCmd, hotkey.ModShift}
}

//export calendiumHotkeyTrampoline
func calendiumHotkeyTrampoline(h C.uintptr_t) {
	handle := cgo.Handle(h)
	handle.Value().(func())()
	handle.Delete()
}

// runOnMainQueue runs fn on the main GCD queue and waits for it to finish.
//
// Why: x/hotkey on macOS uses Carbon's RegisterEventHotKey, which must be
// driven from the main run loop. x/hotkey/mainthread cannot be used because
// Wails owns the main thread (wails.Run starts NSApplication's loop), so we
// dispatch_async the register/unregister calls onto the main GCD queue, which
// that already-running NSApplication loop services.
func runOnMainQueue(fn func()) {
	done := make(chan struct{})
	h := cgo.NewHandle(func() {
		fn()
		close(done)
	})
	C.calendiumDispatchMain(C.uintptr_t(h))
	<-done
}

func platformRegister(hk *hotkey.Hotkey) error {
	var err error
	runOnMainQueue(func() { err = hk.Register() })
	return err
}

func platformUnregister(hk *hotkey.Hotkey) error {
	var err error
	runOnMainQueue(func() { err = hk.Unregister() })
	return err
}
