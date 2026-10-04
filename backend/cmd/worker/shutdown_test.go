package main

import (
	"sync"
	"testing"
	"time"
)

func TestWaitWithTimeout(t *testing.T) {
	t.Run("returns true when the group finishes in time", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			time.Sleep(20 * time.Millisecond)
			wg.Done()
		}()
		if !waitWithTimeout(&wg, time.Second) {
			t.Fatal("want true")
		}
	})
	t.Run("returns false when the group hangs", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1) // never Done
		start := time.Now()
		if waitWithTimeout(&wg, 50*time.Millisecond) {
			t.Fatal("want false")
		}
		if time.Since(start) > 500*time.Millisecond {
			t.Fatalf("took %v, want ~50ms", time.Since(start))
		}
		wg.Done()
	})
}
