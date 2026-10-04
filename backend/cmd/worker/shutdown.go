package main

import (
	"errors"
	"sync"
	"time"
)

// errShutdownTimeout makes main exit 1 when a loop ignores its cancelled
// context past SHUTDOWN_TIMEOUT.
var errShutdownTimeout = errors.New("worker: shutdown timed out")

// waitWithTimeout waits for wg at most d; false means it is still running.
func waitWithTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
