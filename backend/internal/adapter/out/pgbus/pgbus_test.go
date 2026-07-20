package pgbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"calendium/backend/internal/adapter/out/eventbus"
	"calendium/backend/internal/port"
)

var (
	sharedDSN string
	sharedDB  *sql.DB
	// dockerErr != nil means the container could not start (Docker
	// unavailable); integration tests skip on it unless REQUIRE_DOCKER is
	// set, in which case they fail loudly.
	dockerErr error
)

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

func runSuite(m *testing.M) int {
	ctx := context.Background()

	// No migrations: LISTEN/NOTIFY needs no schema at all.
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("calendium_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		dockerErr = err
		return m.Run()
	}
	defer func() { _ = ctr.Terminate(context.Background()) }()

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		dockerErr = err
		return m.Run()
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		dockerErr = err
		return m.Run()
	}
	defer db.Close()
	sharedDSN, sharedDB = dsn, db
	return m.Run()
}

func requireDocker(t *testing.T) {
	t.Helper()
	if dockerErr == nil {
		return
	}
	if os.Getenv("REQUIRE_DOCKER") != "" {
		t.Fatalf("REQUIRE_DOCKER set but Docker unavailable: %v", dockerErr)
	}
	t.Skipf("docker unavailable: %v", dockerErr)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startListener runs a Listener against the shared container with fast
// backoff, cleaned up with the test.
func startListener(t *testing.T, local port.EventBus) *Listener {
	t.Helper()
	l := NewListener(sharedDSN, local, discardLogger())
	l.initialBackoff = 10 * time.Millisecond
	l.maxBackoff = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go l.Run(ctx)
	return l
}

// publishUntilReceived repeatedly NOTIFYs ev until ch yields an event or
// the deadline passes; LISTEN activation races the first publishes, so a
// retry loop is the honest way to wait for the bridge to come up.
func publishUntilReceived(t *testing.T, pub *Publisher, ev port.CollabEvent, ch <-chan port.CollabEvent) port.CollabEvent {
	t.Helper()
	deadline := time.After(15 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case got := <-ch:
			return got
		case <-tick.C:
			pub.Publish(ev)
		case <-deadline:
			t.Fatal("timed out waiting for the event to cross the bridge")
			return port.CollabEvent{}
		}
	}
}

// --- integration: worker publisher → NOTIFY → listener → local bus --------

func TestRoundTripWorkerToAPI(t *testing.T) {
	requireDocker(t)
	local := eventbus.New()
	startListener(t, local)

	ch, cancel := local.Subscribe([]string{"share:s1"})
	defer cancel()

	pub := NewPublisher(sharedDB, discardLogger())
	got := publishUntilReceived(t, pub, port.CollabEvent{
		Topic:   "share:s1",
		Type:    "share.updated",
		Payload: json.RawMessage(`{"never":"crosses postgres"}`),
	}, ch)

	if got.Topic != "share:s1" || got.Type != "share.updated" {
		t.Fatalf("bridged event = %+v, want share:s1/share.updated", got)
	}
	// Ids-only doctrine: the payload must NOT survive the bridge.
	if len(got.Payload) != 0 {
		t.Fatalf("payload crossed the bridge: %s", got.Payload)
	}
}

func TestListenerReconnectsAfterConnectionKill(t *testing.T) {
	requireDocker(t)
	local := eventbus.New()
	startListener(t, local)

	ch, cancel := local.Subscribe([]string{"team:t1"})
	defer cancel()
	pub := NewPublisher(sharedDB, discardLogger())

	// Prove the bridge is up, then kill the LISTEN backend server-side.
	publishUntilReceived(t, pub, port.CollabEvent{Topic: "team:t1", Type: "activity.updated"}, ch)
	_, err := sharedDB.Exec(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE pid <> pg_backend_pid() AND query ILIKE 'LISTEN%'`)
	if err != nil {
		t.Fatalf("terminate listen backend: %v", err)
	}

	// Drain anything already buffered, then require a fresh round-trip:
	// only a reconnected LISTEN can deliver it.
	for drained := false; !drained; {
		select {
		case <-ch:
		default:
			drained = true
		}
	}
	got := publishUntilReceived(t, pub, port.CollabEvent{Topic: "team:t1", Type: "activity.updated"}, ch)
	if got.Topic != "team:t1" || got.Type != "activity.updated" {
		t.Fatalf("post-reconnect event = %+v", got)
	}
}

func TestNoDoubleDeliveryForAPILocalPublishes(t *testing.T) {
	requireDocker(t)
	local := eventbus.New()
	startListener(t, local)

	// Make sure LISTEN is actually active before publishing locally, so a
	// hypothetical loopback would have every chance to double-deliver.
	waitForActiveListen(t)

	ch, cancel := local.Subscribe([]string{"user:u1"})
	defer cancel()
	local.Publish(port.CollabEvent{Topic: "user:u1", Type: "mention", Payload: json.RawMessage(`{"id":"m1"}`)})

	select {
	case got := <-ch:
		if got.Type != "mention" || string(got.Payload) != `{"id":"m1"}` {
			t.Fatalf("local event mangled: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local publish not delivered")
	}
	// API-local publishes never touch Postgres: no echo may arrive.
	select {
	case got := <-ch:
		t.Fatalf("event delivered twice: %+v", got)
	case <-time.After(500 * time.Millisecond):
	}
}

func waitForActiveListen(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		err := sharedDB.QueryRow(
			`SELECT count(*) FROM pg_stat_activity WHERE query ILIKE 'LISTEN%'`).Scan(&n)
		if err == nil && n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("listener never reached LISTEN")
}

// --- unit: malformed payloads, backoff loop, publisher edge cases ----------

func TestHandleDropsMalformedPayloads(t *testing.T) {
	local := eventbus.New()
	ch, cancel := local.Subscribe([]string{"team:t1"})
	defer cancel()
	l := &Listener{local: local, logger: discardLogger()}

	for _, payload := range []string{
		"",
		"not json",
		"{",
		`{"topic":"team:t1"}`,        // missing type
		`{"type":"comment.created"}`, // missing topic
		`{"topic":"","type":""}`,     // empty both
		`["topic","type"]`,           // wrong shape
		strings.Repeat("x", 9000),    // oversized garbage
	} {
		l.handle(payload) // must not panic, must not publish
	}
	select {
	case ev := <-ch:
		t.Fatalf("malformed payload published an event: %+v", ev)
	default:
	}

	// A valid envelope still goes through after the garbage.
	l.handle(`{"topic":"team:t1","type":"comment.created"}`)
	select {
	case ev := <-ch:
		if ev.Topic != "team:t1" || ev.Type != "comment.created" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("valid envelope not delivered")
	}
}

func TestRunRetriesWithBackoffAndStopsOnCancel(t *testing.T) {
	var calls atomic.Int32
	l := &Listener{
		local:          eventbus.New(),
		logger:         discardLogger(),
		initialBackoff: time.Millisecond,
		maxBackoff:     4 * time.Millisecond,
		listen: func(ctx context.Context, _ func(string)) error {
			calls.Add(1)
			return errors.New("connection refused")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()

	// The loop must keep retrying (never crash, never give up).
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got < 5 {
		t.Fatalf("want >= 5 listen attempts, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestRunReturnsWhenListenEndsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &Listener{
		local:          eventbus.New(),
		logger:         discardLogger(),
		initialBackoff: time.Millisecond,
		maxBackoff:     2 * time.Millisecond,
		listen: func(ctx context.Context, _ func(string)) error {
			cancel() // simulate shutdown arriving while connected
			return ctx.Err()
		},
	}
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on canceled context")
	}
}

func TestPublisherSubscribeIsInert(t *testing.T) {
	pub := NewPublisher(nil, discardLogger())
	ch, cancel := pub.Subscribe([]string{"team:t1"})
	select {
	case ev := <-ch:
		t.Fatalf("inert channel delivered %+v", ev)
	default:
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after cancel")
	}
	cancel() // idempotent
}

func TestPublisherDropsOversizedEnvelopeWithoutTouchingDB(t *testing.T) {
	// db is nil: reaching ExecContext would panic, so surviving this call
	// proves the size guard fires first.
	pub := NewPublisher(nil, discardLogger())
	pub.Publish(port.CollabEvent{Topic: strings.Repeat("t", maxEnvelopeBytes), Type: "x"})
}
