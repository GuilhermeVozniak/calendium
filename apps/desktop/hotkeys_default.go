//go:build !darwin

package main

import "golang.design/x/hotkey"

// Windows/Linux use Ctrl+Shift as the primary chord.
func defaultModifiers() []hotkey.Modifier {
	return []hotkey.Modifier{hotkey.ModCtrl, hotkey.ModShift}
}

// On Windows/Linux x/hotkey registers fine from any (OS-thread-locked)
// goroutine — no main-loop hop needed.
func platformRegister(hk *hotkey.Hotkey) error { return hk.Register() }

func platformUnregister(hk *hotkey.Hotkey) error { return hk.Unregister() }
