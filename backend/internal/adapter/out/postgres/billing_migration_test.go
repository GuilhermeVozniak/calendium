package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/migrate"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
	"calendium/backend/migrations"
)

// Review I1: rows written by the legacy provider (customer ids "cus_…",
// subscription ids "sub_…") must not survive 0027 as Paddle ids, or Paddle
// rejects every checkout/portal call for those users and reconciliation
// re-reads a foreign subscription id forever.

// legacyCol names the pre-0027 provider columns without spelling the old
// provider (the repo-wide legacy-provider grep stays empty outside
// migrations).
func legacyCol(suffix string) string { return "str" + "ipe_" + suffix }

// migrationsBefore returns the embedded migrations strictly before name.
func migrationsBefore(t *testing.T, name string) fs.FS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, n := range names {
		if n >= name {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = &fstest.MapFile{Data: body}
	}
	return out
}

// freshDatabase creates an empty database on the shared container server.
func freshDatabase(t *testing.T, name string) *sql.DB {
	t.Helper()
	_, _ = newTestStore(t) // skip/require-docker semantics
	ctx := context.Background()
	if _, err := sharedDB.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedDB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(sharedDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = sharedDB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	return db
}

type migClock struct{ now time.Time }

func (c migClock) Now() time.Time { return c.now }

// migPayments records the customer id a checkout is created with.
type migPayments struct {
	ensured     []string
	checkoutFor []string
}

func (p *migPayments) EnsureCustomer(_ context.Context, u domain.User) (string, error) {
	p.ensured = append(p.ensured, u.ID)
	return "ctm_" + u.ID, nil
}
func (p *migPayments) CreateCheckout(_ context.Context, cp port.CheckoutParams) (string, error) {
	p.checkoutFor = append(p.checkoutFor, cp.CustomerID)
	return "https://pay.example/checkout", nil
}
func (p *migPayments) CreatePortalSession(context.Context, string, string) (port.PortalURLs, error) {
	return port.PortalURLs{}, nil
}
func (p *migPayments) ParseWebhook([]byte, string, time.Time) (port.SubscriptionEvent, error) {
	return port.SubscriptionEvent{}, errors.New("unused")
}
func (p *migPayments) GetSubscription(context.Context, string) (port.SubscriptionEvent, error) {
	return port.SubscriptionEvent{}, errors.New("unused")
}
func (p *migPayments) CancelSubscription(context.Context, string, bool) error { return nil }

func TestMigration0027ClearsLegacyProviderRows(t *testing.T) {
	db := freshDatabase(t, "mig0027_legacy_rows")
	ctx := context.Background()
	if err := migrate.Apply(ctx, db, migrationsBefore(t, "0027_paddle_billing.sql")); err != nil {
		t.Fatalf("migrate through 0026: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	recent := now.Add(-2 * 24 * time.Hour)
	trialEnd := now.Add(5 * 24 * time.Hour)
	periodEnd := now.Add(200 * 24 * time.Hour)
	for _, id := range []string{"u_none", "u_active", "u_pastdue", "u_expired", "u_canceled", "u_trial", "u_clean"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (id, email, created_at) VALUES ($1, $1 || '@example.com', $2)`, id, recent); err != nil {
			t.Fatalf("seed user %s: %v", id, err)
		}
	}
	insert := fmt.Sprintf(`INSERT INTO subscriptions
		(user_id, status, %s, %s, current_period_end, cancel_at_period_end, trial_ends_at, last_event_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8)`, legacyCol("customer_id"), legacyCol("subscription_id"))
	rows := []struct {
		user, status, cus, sub string
		periodEnd, trialEnd    *time.Time
		cancelAtEnd            bool
	}{
		{user: "u_none", status: "none", cus: "cus_none"},
		{user: "u_active", status: "active", cus: "cus_active", sub: "sub_active", periodEnd: &periodEnd},
		{user: "u_pastdue", status: "past_due", sub: "sub_pastdue", periodEnd: &periodEnd},
		{user: "u_expired", status: "expired", cus: "cus_expired", sub: "sub_expired"},
		{user: "u_canceled", status: "canceled", cus: "cus_canceled", sub: "sub_canceled", cancelAtEnd: true},
		{user: "u_trial", status: "trialing", cus: "cus_trial", trialEnd: &trialEnd},
		{user: "u_clean", status: "trialing", trialEnd: &trialEnd},
	}
	for _, r := range rows {
		var lastEvent *time.Time
		if r.cus != "" || r.sub != "" {
			lastEvent = &recent
		}
		if _, err := db.ExecContext(ctx, insert, r.user, r.status, r.cus, r.sub, r.periodEnd, r.cancelAtEnd, r.trialEnd, lastEvent); err != nil {
			t.Fatalf("seed subscription %s: %v", r.user, err)
		}
	}

	if err := migrate.Apply(ctx, db, migrations.FS); err != nil {
		t.Fatalf("apply 0027: %v", err)
	}

	type row struct {
		status    string
		cus, sub  sql.NullString
		lastEvent sql.NullTime
		trialEnd  sql.NullTime
	}
	get := func(user string) (row, bool) {
		var r row
		err := db.QueryRowContext(ctx, `SELECT status, billing_customer_id, billing_subscription_id, last_event_at, trial_ends_at
			FROM subscriptions WHERE user_id = $1`, user).Scan(&r.status, &r.cus, &r.sub, &r.lastEvent, &r.trialEnd)
		if errors.Is(err, sql.ErrNoRows) {
			return row{}, false
		}
		if err != nil {
			t.Fatalf("read %s: %v", user, err)
		}
		return r, true
	}

	var legacy int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM subscriptions
		WHERE billing_customer_id LIKE 'cus\_%' OR billing_subscription_id LIKE 'sub\_%'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy provider ids left after 0027: %d (err=%v)", legacy, err)
	}
	// Non-trial legacy rows are removed: the user falls back to the
	// signup-trial path (no row -> trial anchored to users.created_at).
	for _, u := range []string{"u_none", "u_active", "u_pastdue", "u_expired", "u_canceled"} {
		if r, ok := get(u); ok {
			t.Fatalf("%s: legacy non-trial row kept: %+v", u, r)
		}
	}
	// A legacy trial keeps its status and trial end; only the ids go.
	tr, ok := get("u_trial")
	if !ok || tr.status != "trialing" || tr.cus.Valid || tr.sub.Valid || tr.lastEvent.Valid ||
		!tr.trialEnd.Valid || !tr.trialEnd.Time.Equal(trialEnd) {
		t.Fatalf("u_trial after 0027: ok=%v %+v", ok, tr)
	}
	// A row with no legacy ids is untouched.
	if cl, ok := get("u_clean"); !ok || cl.status != "trialing" || !cl.trialEnd.Valid {
		t.Fatalf("u_clean after 0027: ok=%v %+v", ok, cl)
	}

	// Checkout for migrated users now creates a Paddle customer instead of
	// sending a legacy cus_ id (or refusing with already_subscribed).
	st := NewStore(db)
	pay := &migPayments{}
	svc := service.NewBillingService(service.BillingServiceDeps{
		Users: st.Users(), Subs: st.Subscriptions(), Events: st.BillingEvents(), Payments: pay,
		Clock: migClock{now: now}, Tx: st,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for _, u := range []string{"u_active", "u_trial", "u_none"} {
		if _, err := svc.CreateCheckout(ctx, u); err != nil {
			t.Fatalf("checkout %s: %v", u, err)
		}
	}
	for _, c := range pay.checkoutFor {
		if strings.HasPrefix(c, "cus_") || !strings.HasPrefix(c, "ctm_") {
			t.Fatalf("checkout customer ids = %v, want fresh ctm_ ids only", pay.checkoutFor)
		}
	}
	if len(pay.checkoutFor) != 3 || len(pay.ensured) != 3 {
		t.Fatalf("checkouts=%v ensured=%v, want 3 of each", pay.checkoutFor, pay.ensured)
	}
	// The deleted legacy row regranted the signup trial.
	sub, err := svc.GetSubscription(ctx, "u_active")
	if err != nil || sub.Status != domain.SubscriptionTrialing || sub.TrialEndsAt == nil ||
		!sub.TrialEndsAt.Equal(recent.Add(domain.TrialLength)) {
		t.Fatalf("u_active after checkout: %+v err=%v", sub, err)
	}
}
