package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/adapter/in/httpapi"
)

// startServe boots serve() on a random loopback port with the real httpapi
// handler (for /readyz) plus a /slow route that blocks until release is
// closed. It returns the base URL, the cancel that simulates SIGTERM, and
// a channel that yields serve's return value.
func startServe(t *testing.T, opts serveOptions, release <-chan struct{}, entered chan<- struct{}) (string, context.CancelFunc, chan struct{}, <-chan error) {
	t.Helper()
	drain := make(chan struct{})
	api := httpapi.New(httpapi.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Drain:  drain,
		Ready:  func(context.Context) error { return nil },
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})
	mux.Handle("/", api)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, ln, drain, opts)
	}()
	return "http://" + ln.Addr().String(), cancel, drain, errCh
}

func TestGracefulShutdown(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	base, cancel, drain, errCh := startServe(t, serveOptions{shutdownTimeout: 5 * time.Second}, release, entered)

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b)}
	}()
	<-entered // the request is in flight

	cancel() // SIGTERM
	select {
	case <-drain:
	case <-time.After(time.Second):
		t.Fatal("drain channel was not closed after the signal")
	}
	select {
	case err := <-errCh:
		t.Fatalf("serve returned %v while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release) // the handler finishes
	res := <-resCh
	if res.err != nil || res.status != http.StatusOK || res.body != "done" {
		t.Fatalf("in-flight request = %+v, want 200 done", res)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after the last request completed")
	}
}

func TestServeReadyzDrainingDuringDrainDelay(t *testing.T) {
	release := make(chan struct{})
	close(release)
	entered := make(chan struct{}, 1)
	base, cancel, drain, errCh := startServe(t, serveOptions{shutdownTimeout: 5 * time.Second, drainDelay: 500 * time.Millisecond}, release, entered)

	resp, err := http.Get(base + "/readyz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz before shutdown: %v %v", err, resp)
	}
	_ = resp.Body.Close()

	cancel()
	<-drain // listeners stay open for drainDelay after this
	resp, err = http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("readyz during drain delay: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "{\"status\":\"draining\"}\n" {
		t.Fatalf("readyz = %d %q, want 503 draining", resp.StatusCode, body)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("serve returned %v", err)
	}
}

func TestServeForcesCloseAfterShutdownTimeout(t *testing.T) {
	release := make(chan struct{}) // never closed: the handler hangs forever
	entered := make(chan struct{}, 1)
	base, cancel, _, errCh := startServe(t, serveOptions{shutdownTimeout: 200 * time.Millisecond}, release, entered)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	start := time.Now()
	cancel()
	select {
	case <-errCh:
		if time.Since(start) > 2*time.Second {
			t.Fatalf("serve took %v, want ~200ms then Close()", time.Since(start))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve never returned: Close() after the deadline is missing")
	}
	close(release)
}
