// Main-queue dispatch shim for hotkeys_darwin.go: Carbon's
// RegisterEventHotKey must run on the main run loop, which Wails'
// NSApplication owns — so register/unregister hop over via dispatch_async_f.
#include <dispatch/dispatch.h>
#include <stdint.h>

#include "_cgo_export.h"

static void calendiumHotkeyTrampolineWrapper(void *ctx) {
	calendiumHotkeyTrampoline((uintptr_t)ctx);
}

void calendiumDispatchMain(uintptr_t handle) {
	dispatch_async_f(dispatch_get_main_queue(), (void *)handle,
	                 calendiumHotkeyTrampolineWrapper);
}
