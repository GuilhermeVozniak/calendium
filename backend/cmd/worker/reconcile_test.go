package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"calendium/backend/internal/port"
)

type reconcileStub struct {
	port.BillingService
	err error
}

func (s reconcileStub) ReconcileSubscriptions(context.Context) error { return s.err }

func TestReconcilePassDoesNotLogShutdownCancellation(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	reconcilePass(context.Background(), logger, reconcileStub{err: context.Canceled})
	if buf.Len() != 0 {
		t.Fatalf("shutdown mid-pass must not be logged, got %q", buf.String())
	}

	reconcilePass(context.Background(), logger, reconcileStub{err: errors.New("db down")})
	if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "db down") {
		t.Fatalf("real failures must be logged at error, got %q", buf.String())
	}
}
