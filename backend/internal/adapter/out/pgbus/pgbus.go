// Package pgbus bridges port.CollabEvents across processes over Postgres
// LISTEN/NOTIFY (M2 follow-up F1). The in-process broker
// (adapter/out/eventbus) only reaches subscribers inside the same process,
// so events published by cmd/worker — share.updated on sync-delivered
// messages, team activity.updated — never reached the API's SSE clients.
//
// Topology (deliberately one-directional to avoid double delivery):
//
//   - cmd/worker wires its services' Bus to Publisher, which NOTIFYs a
//     compact {topic,type} envelope on one channel. The worker keeps no
//     local subscribers.
//   - cmd/api keeps the in-memory bus for its SSE subscribers and runs
//     Listener.Run, which LISTENs on a dedicated connection and
//     republishes each envelope into that bus. API-local publishes go
//     straight to the in-memory bus and never touch Postgres, so an API
//     event can never arrive twice.
//
// Doctrine: ids only. The envelope carries topic + type and nothing else —
// payloads never cross Postgres (a worker-side payload is dropped at the
// publisher; Postgres caps NOTIFY payloads at ~8KB regardless), and SSE
// clients refetch through their authorized endpoints on any event.
// Delivery is best-effort: a notification arriving while the listener is
// reconnecting is lost, matching the in-memory broker's drop-on-full-buffer
// contract — clients converge via refetch.
package pgbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"calendium/backend/internal/port"
)

// channel is the single NOTIFY channel every Calendium event rides.
const channel = "calendium_events"

// maxEnvelopeBytes guards the Postgres NOTIFY payload cap (8000 bytes).
// A {topic,type} envelope is a few dozen bytes, so hitting this means a
// corrupt topic — drop and log rather than error the caller.
const maxEnvelopeBytes = 7500

// publishTimeout bounds one pg_notify round-trip; Publish has no context
// by contract (port.EventBus is fire-and-forget).
const publishTimeout = 5 * time.Second

// envelope is the compact cross-process wire form of a CollabEvent.
// Payload is intentionally absent (ids-only doctrine).
type envelope struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
}

// ---------------------------------------------------------------------------
// Publisher (worker side)
// ---------------------------------------------------------------------------

// Publisher implements port.EventBus by NOTIFYing envelopes to Postgres.
// It is the worker's bus: Publish is best-effort (failures are logged,
// never returned — the mutation the event describes is already durable and
// clients refetch), and Subscribe is inert because the worker keeps no
// local subscribers.
type Publisher struct {
	db     *sql.DB
	logger *slog.Logger
}

var _ port.EventBus = (*Publisher)(nil)

// NewPublisher returns a Publisher over the given pool. A nil logger
// falls back to slog.Default().
func NewPublisher(db *sql.DB, logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{db: db, logger: logger}
}

// Publish NOTIFYs {topic,type} on the shared channel. The event's Payload
// never crosses Postgres; API-side subscribers receive the envelope only
// and clients refetch content through authorized endpoints.
func (p *Publisher) Publish(ev port.CollabEvent) {
	body, err := json.Marshal(envelope{Topic: ev.Topic, Type: ev.Type})
	if err != nil { // unreachable for two plain strings; belt and braces
		p.logger.Error("pgbus: marshal envelope", "error", err)
		return
	}
	if len(body) > maxEnvelopeBytes {
		p.logger.Error("pgbus: dropping oversized envelope",
			"topic_len", len(ev.Topic), "type_len", len(ev.Type))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if _, err := p.db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, string(body)); err != nil {
		p.logger.Error("pgbus: publish", "topic", ev.Topic, "type", ev.Type, "error", err)
	}
}

// Subscribe satisfies port.EventBus but never delivers: the worker has no
// SSE surface, so nothing subscribes on this side of the bridge. The
// returned channel stays silent until cancel closes it (idempotent).
func (p *Publisher) Subscribe([]string) (<-chan port.CollabEvent, func()) {
	ch := make(chan port.CollabEvent)
	var once sync.Once
	return ch, func() { once.Do(func() { close(ch) }) }
}

// ---------------------------------------------------------------------------
// Listener (API side)
// ---------------------------------------------------------------------------

// Listener LISTENs on a dedicated Postgres connection and republishes each
// received envelope into a local (in-memory) port.EventBus. Run reconnects
// with capped exponential backoff on any connection loss and never crashes
// the process; notifications sent while disconnected are lost by design
// (clients refetch).
type Listener struct {
	local  port.EventBus
	logger *slog.Logger

	// listen connects, LISTENs, and blocks delivering payloads to handle
	// until the connection fails or ctx ends. Injected for backoff tests.
	listen func(ctx context.Context, handle func(payload string)) error

	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// NewListener returns a Listener that connects to dsn with its own
// dedicated pgx connection (LISTEN cannot ride the pooled database/sql
// handle — notifications are delivered per-connection). A nil logger falls
// back to slog.Default().
func NewListener(dsn string, local port.EventBus, logger *slog.Logger) *Listener {
	if logger == nil {
		logger = slog.Default()
	}
	l := &Listener{
		local:          local,
		logger:         logger,
		initialBackoff: time.Second,
		maxBackoff:     30 * time.Second,
	}
	l.listen = func(ctx context.Context, handle func(string)) error {
		return listenPg(ctx, dsn, handle)
	}
	return l
}

// Run blocks until ctx ends, maintaining the LISTEN connection. Every
// disconnect is logged and retried after a backoff that doubles from
// initialBackoff up to maxBackoff; a connection that stayed healthy longer
// than maxBackoff resets the ladder.
func (l *Listener) Run(ctx context.Context) {
	backoff := l.initialBackoff
	for {
		start := time.Now()
		err := l.listen(ctx, l.handle)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > l.maxBackoff {
			backoff = l.initialBackoff
		}
		l.logger.Error("pgbus: listener disconnected; retrying",
			"error", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > l.maxBackoff {
			backoff = l.maxBackoff
		}
	}
}

// handle parses one notification payload and republishes it locally.
// Malformed or incomplete envelopes are logged and dropped — a bad payload
// must never take the listener (or the process) down.
func (l *Listener) handle(payload string) {
	var env envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		l.logger.Warn("pgbus: dropping malformed notification", "error", err)
		return
	}
	if env.Topic == "" || env.Type == "" {
		l.logger.Warn("pgbus: dropping notification without topic/type")
		return
	}
	l.local.Publish(port.CollabEvent{Topic: env.Topic, Type: env.Type})
}

// listenPg is the real connection loop: dedicated pgx connection, LISTEN,
// then block on WaitForNotification until the connection or ctx dies.
func listenPg(ctx context.Context, dsn string, handle func(payload string)) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	// channel is a package constant identifier, never user input.
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		handle(n.Payload)
	}
}
