package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// serveOptions carries the SHUTDOWN_* knobs.
type serveOptions struct {
	shutdownTimeout time.Duration
	drainDelay      time.Duration
}

// serve runs srv on ln until ctx is cancelled (SIGTERM/SIGINT), then
// drains in the documented order: close(drain) so /readyz answers 503 and
// the SSE streams end → sleep drainDelay so a balancer stops routing →
// srv.Shutdown bounded by shutdownTimeout → on deadline log and
// srv.Close(). It returns nil on a clean stop and the listener error
// otherwise; the caller closes the DB afterwards.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server, ln net.Listener, drain chan struct{}, opts serveOptions) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("api: shutdown requested; draining",
		"drain_delay", opts.drainDelay.String(), "timeout", opts.shutdownTimeout.String())
	close(drain)
	if opts.drainDelay > 0 {
		time.Sleep(opts.drainDelay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), opts.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("api: shutdown deadline exceeded; closing open connections", "error", err)
		_ = srv.Close()
	}
	<-errCh // Serve has returned (ErrServerClosed)
	return nil
}
