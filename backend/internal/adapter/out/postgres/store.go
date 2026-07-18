// Package postgres implements every repository port in
// internal/port/driven.go on database/sql (pgx stdlib driver, no ORM). It
// also provides AES-256-GCM encryption at rest for provider OAuth tokens
// and a context-propagated port.TxRunner.
package postgres

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"fmt"

	"calendium/backend/internal/port"
)

// Store is the Postgres persistence adapter. A single Store backs every
// repo port via the typed accessors below; all repos share the same
// *sql.DB and join transactions started by RunInTx.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD // provider-token encryption; set via SetTokenEncryptionKey
}

// NewStore wraps db. Call SetTokenEncryptionKey before using the provider
// token vault (AccountRepo.SaveTokens / GetTokens).
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Typed accessors — one per driven repo port.
func (s *Store) Users() port.UserRepo                   { return userRepo{s} }
func (s *Store) Subscriptions() port.SubscriptionRepo   { return subscriptionRepo{s} }
func (s *Store) StripeEvents() port.StripeEventRepo     { return stripeEventRepo{s} }
func (s *Store) Accounts() port.AccountRepo             { return accountRepo{s} }
func (s *Store) OAuthStates() port.OAuthStateRepo       { return oauthStateRepo{s} }
func (s *Store) SyncStates() port.SyncStateRepo         { return syncStateRepo{s} }
func (s *Store) Devices() port.DeviceRepo               { return deviceRepo{s} }
func (s *Store) Threads() port.ThreadRepo               { return threadRepo{s} }
func (s *Store) Messages() port.MessageRepo             { return messageRepo{s} }
func (s *Store) Labels() port.LabelRepo                 { return labelRepo{s} }
func (s *Store) Drafts() port.DraftRepo                 { return draftRepo{s} }
func (s *Store) Snippets() port.SnippetRepo             { return snippetRepo{s} }
func (s *Store) Calendars() port.CalendarRepo           { return calendarRepo{s} }
func (s *Store) Events() port.EventRepo                 { return eventRepo{s} }
func (s *Store) EventTemplates() port.EventTemplateRepo { return eventTemplateRepo{s} }
func (s *Store) CalendarSets() port.CalendarSetRepo     { return calendarSetRepo{s} }
func (s *Store) Prefs() port.PrefsRepo                  { return prefsRepo{s} }

var (
	_ port.TxRunner          = (*Store)(nil)
	_ port.UserRepo          = userRepo{}
	_ port.SubscriptionRepo  = subscriptionRepo{}
	_ port.StripeEventRepo   = stripeEventRepo{}
	_ port.AccountRepo       = accountRepo{}
	_ port.OAuthStateRepo    = oauthStateRepo{}
	_ port.SyncStateRepo     = syncStateRepo{}
	_ port.DeviceRepo        = deviceRepo{}
	_ port.ThreadRepo        = threadRepo{}
	_ port.MessageRepo       = messageRepo{}
	_ port.LabelRepo         = labelRepo{}
	_ port.DraftRepo         = draftRepo{}
	_ port.SnippetRepo       = snippetRepo{}
	_ port.CalendarRepo      = calendarRepo{}
	_ port.EventRepo         = eventRepo{}
	_ port.EventTemplateRepo = eventTemplateRepo{}
	_ port.CalendarSetRepo   = calendarSetRepo{}
	_ port.PrefsRepo         = prefsRepo{}
)

type (
	userRepo          struct{ *Store }
	subscriptionRepo  struct{ *Store }
	stripeEventRepo   struct{ *Store }
	accountRepo       struct{ *Store }
	oauthStateRepo    struct{ *Store }
	syncStateRepo     struct{ *Store }
	deviceRepo        struct{ *Store }
	threadRepo        struct{ *Store }
	messageRepo       struct{ *Store }
	labelRepo         struct{ *Store }
	draftRepo         struct{ *Store }
	snippetRepo       struct{ *Store }
	calendarRepo      struct{ *Store }
	eventRepo         struct{ *Store }
	eventTemplateRepo struct{ *Store }
	calendarSetRepo   struct{ *Store }
	prefsRepo         struct{ *Store }
)

// --- transactions -----------------------------------------------------------

type txKey struct{}

// querier is satisfied by both *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// q returns the transaction bound to ctx, or the pool.
func (s *Store) q(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return s.db
}

// RunInTx implements port.TxRunner. Nested calls join the outer transaction.
func (s *Store) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: commit tx: %w", err)
	}
	return nil
}
