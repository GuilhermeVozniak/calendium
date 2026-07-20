package postgres

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/migrate"
	"calendium/backend/migrations"
)

// testKey is a deterministic 32-byte AES-256-GCM key for the token vault.
var testKey = []byte("0123456789abcdef0123456789abcdef") // len == 32

var (
	sharedStore *Store
	sharedDB    *sql.DB
	// dockerErr != nil means the container could not start (Docker
	// unavailable); every test t.Skip()s on it.
	dockerErr error
)

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

func runSuite(m *testing.M) int {
	ctx := context.Background()

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
		// Container start failure ~always means Docker is unavailable. Record
		// it and run: the tests will skip themselves.
		dockerErr = err
		return m.Run()
	}
	defer func() { _ = ctr.Terminate(context.Background()) }()

	// Past this point the container is up, so any error is a real defect in
	// our migration/store wiring — fail loudly, do not skip.
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("postgres test: connection string: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("postgres test: open db: %v", err)
	}
	defer db.Close()
	if err := migrate.Apply(ctx, db, migrations.FS); err != nil {
		log.Fatalf("postgres test: migrate: %v", err)
	}
	st := NewStore(db)
	if err := st.SetTokenEncryptionKey(testKey); err != nil {
		log.Fatalf("postgres test: token key: %v", err)
	}
	sharedStore, sharedDB = st, db
	return m.Run()
}

// newTestStore returns the shared store + raw db, skipping when Docker is
// unavailable and truncating every application table for isolation.
// Migrations are applied once in TestMain and are NOT re-run here.
func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	if dockerErr != nil {
		if os.Getenv("REQUIRE_DOCKER") != "" {
			t.Fatalf("REQUIRE_DOCKER is set but the postgres container failed to start: %v", dockerErr)
		}
		t.Skipf("skipping postgres repo tests: Docker unavailable: %v", dockerErr)
	}
	truncateAll(t)
	return sharedStore, sharedDB
}

// truncateAll wipes app data (CASCADE makes FK order irrelevant). The Better
// Auth tables and schema_migrations are intentionally preserved.
func truncateAll(t *testing.T) {
	t.Helper()
	_, err := sharedDB.Exec(`TRUNCATE
		users, subscriptions, stripe_events, connected_accounts, oauth_states,
		labels, threads, thread_labels, messages, attachments, drafts, snippets,
		calendars, events, devices, sync_state, user_prefs, event_templates, calendar_sets,
		ai_jobs, ai_classifiers, voice_profiles, ai_usage,
		booking_links, bookings, meeting_polls, poll_votes, time_proposals, user_settings,
		message_reactions, teams, team_members, team_invitations, thread_shares,
		calendar_shares, audit_entries, thread_comments, delegations, team_thread_activity,
		tasks, calendar_prefs, travel_alerts
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// --- seed helpers (satisfy the FK chain users → accounts → threads/calendars) ---

func seedUser(t *testing.T, st *Store, id string) domain.User {
	t.Helper()
	u, err := st.Users().Upsert(context.Background(), domain.User{ID: id, Email: id + "@example.com"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func seedAccount(t *testing.T, st *Store, userID string) domain.ConnectedAccount {
	t.Helper()
	a, err := st.Accounts().Create(context.Background(), domain.ConnectedAccount{
		UserID:   userID,
		Provider: domain.ProviderGoogle,
		Email:    userID + "+google@example.com",
		Status:   domain.AccountActive,
		Scopes:   []string{"mail.read"},
	})
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return a
}

func seedThread(t *testing.T, st *Store, accountID string, lastMsg time.Time) domain.Thread {
	t.Helper()
	th, err := st.Threads().Upsert(context.Background(), domain.Thread{
		AccountID:        accountID,
		ProviderThreadID: newID(),
		Subject:          "hello",
		Split:            domain.SplitImportant,
		InInbox:          true,
		Unread:           true,
		MessageCount:     1,
		LastMessageAt:    lastMsg.UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	return th
}

func seedCalendar(t *testing.T, st *Store, accountID string) domain.Calendar {
	t.Helper()
	c, err := st.Calendars().Upsert(context.Background(), domain.Calendar{
		AccountID:          accountID,
		ProviderCalendarID: newID(),
		Name:               "Primary",
		IsPrimary:          true,
		CanWrite:           true,
	})
	if err != nil {
		t.Fatalf("seed calendar: %v", err)
	}
	return c
}
