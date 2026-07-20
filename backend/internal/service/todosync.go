package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// TodoSyncDeps wires the worker's todo-mirror sync pass (M2.8 Task 10).
type TodoSyncDeps struct {
	Integrations port.IntegrationRepo
	Tasks        port.TaskRepo
	SyncState    port.SyncStateRepo
	// Prefs resolves each connection owner's CalendarPrefs timezone so the
	// vendor adapter can interpret floating due datetimes on the user's wall
	// clock. Optional — nil falls back to UTC.
	Prefs port.CalendarPrefsRepo
	// Providers holds the configured todo-tool adapters keyed by task source.
	Providers map[domain.TaskSource]port.TodoProvider
	// OAuth refreshes vendor tokens on 401 (exactly one retry per page),
	// keyed by integration vendor.
	OAuth  map[domain.IntegrationVendor]port.OAuthGateway
	Clock  port.Clock
	Logger *slog.Logger
}

// TodoSyncService mirrors external todos into the tasks table. One pass walks
// every connection of every configured vendor: incremental SyncTasks from the
// stored cursor, upsert by external id (vendor fields refresh; local planning
// state — Scheduled*, Position, CompletedAt — is preserved), delete rows the
// vendor completed or removed, persist the cursor. Connections fail
// independently: a broken grant is logged and marked Status=error on the
// connection without stalling the fleet. Users without a connection cost
// nothing — only rows returned by ListByVendor trigger vendor calls.
type TodoSyncService struct {
	integrations port.IntegrationRepo
	tasks        port.TaskRepo
	syncState    port.SyncStateRepo
	prefs        port.CalendarPrefsRepo
	providers    map[domain.TaskSource]port.TodoProvider
	oauth        map[domain.IntegrationVendor]port.OAuthGateway
	clock        port.Clock
	logger       *slog.Logger
}

func NewTodoSyncService(d TodoSyncDeps) *TodoSyncService {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &TodoSyncService{
		integrations: d.Integrations,
		tasks:        d.Tasks,
		syncState:    d.SyncState,
		prefs:        d.Prefs,
		providers:    d.Providers,
		oauth:        d.OAuth,
		clock:        d.Clock,
		logger:       d.Logger,
	}
}

// vendorForTaskSource maps a mirrored task source to its integration vendor.
func vendorForTaskSource(s domain.TaskSource) (domain.IntegrationVendor, bool) {
	switch s {
	case domain.TaskSourceTodoist:
		return domain.IntegrationTodoist, true
	default:
		return "", false
	}
}

// SyncTodos runs one incremental pass over every connection of every
// configured todo vendor. Per-connection failures are logged and recorded on
// the connection (Status=error, LastError) and never abort the pass; only
// listing a vendor's connections failing is returned to the caller.
func (s *TodoSyncService) SyncTodos(ctx context.Context) error {
	var firstErr error
	for source, provider := range s.providers {
		vendor, ok := vendorForTaskSource(source)
		if !ok {
			continue
		}
		conns, err := s.integrations.ListByVendor(ctx, vendor)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("list %s connections: %w", vendor, err)
			}
			continue
		}
		for _, conn := range conns {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := s.syncConnection(ctx, source, vendor, provider, conn); err != nil {
				// Log + continue: one broken grant must not stall the rest.
				s.logger.Error("todo sync: connection failed",
					"connection_id", conn.ID, "vendor", vendor, "error", err)
				s.markError(ctx, conn, err)
				continue
			}
			s.markHealthy(ctx, conn)
		}
	}
	return firstErr
}

// syncConnection drains the vendor delta for one connection, applying each
// page and persisting its cursor before asking for the next.
func (s *TodoSyncService) syncConnection(ctx context.Context, source domain.TaskSource, vendor domain.IntegrationVendor, provider port.TodoProvider, conn domain.IntegrationConnection) error {
	tokens, err := s.integrations.GetTokens(ctx, conn.ID)
	if errors.Is(err, domain.ErrNotFound) {
		// Disconnected between ListByVendor and now — nothing to sync, and
		// nothing to record on the (gone) connection.
		return nil
	}
	if err != nil {
		return err
	}

	cursor := ""
	switch st, err := s.syncState.Get(ctx, conn.ID, string(vendor)); {
	case err == nil:
		cursor = st.Cursor
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}

	// One prefs read per connection per pass — cheap, and it keeps floating
	// vendor due datetimes on the owner's wall clock (M2 review minor).
	loc := s.userLocation(ctx, conn.UserID)

	for {
		page, err := s.syncPage(ctx, vendor, provider, conn, &tokens, cursor, loc)
		if err != nil {
			return err
		}
		if err := s.applyPage(ctx, source, conn, page); err != nil {
			return err
		}
		if err := s.syncState.Save(ctx, port.SyncState{
			AccountID: conn.ID,
			Resource:  string(vendor),
			Cursor:    page.NextCursor,
			UpdatedAt: s.clock.Now(),
		}); err != nil {
			return err
		}
		if !page.HasMore {
			return nil
		}
		cursor = page.NextCursor
	}
}

// syncPage fetches one vendor page; a 401 refreshes through the vendor's
// OAuth gateway exactly once, persists the new tokens (encrypted at rest by
// the repo), and retries.
func (s *TodoSyncService) syncPage(ctx context.Context, vendor domain.IntegrationVendor, provider port.TodoProvider, conn domain.IntegrationConnection, tokens *port.TokenSet, cursor string, loc *time.Location) (port.TodoSyncPage, error) {
	page, err := provider.SyncTasks(ctx, tokens.AccessToken, cursor, loc)
	if err == nil || !errors.Is(err, domain.ErrUnauthorized) {
		return page, err
	}
	gw, ok := s.oauth[vendor]
	if !ok {
		return port.TodoSyncPage{}, err
	}
	tok, rerr := gw.Refresh(ctx, tokens.RefreshToken)
	if rerr != nil {
		return port.TodoSyncPage{}, fmt.Errorf("refresh %s token: %w", vendor, rerr)
	}
	ts := tok.TokenSet
	if ts.RefreshToken == "" {
		// Vendors commonly omit the refresh token on refresh — keep ours.
		ts.RefreshToken = tokens.RefreshToken
	}
	if uerr := s.integrations.SaveTokens(ctx, conn.ID, ts); uerr != nil {
		return port.TodoSyncPage{}, uerr
	}
	*tokens = ts
	return provider.SyncTasks(ctx, ts.AccessToken, cursor, loc)
}

// userLocation resolves the connection owner's CalendarPrefs timezone.
// Best-effort by design: a nil Prefs repo, a repo error, or an unloadable
// zone all fall back to UTC — a due-time approximation must never fail the
// sync pass.
func (s *TodoSyncService) userLocation(ctx context.Context, userID string) *time.Location {
	if s.prefs == nil {
		return time.UTC
	}
	p, err := s.prefs.Get(ctx, userID)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(p.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// applyPage mirrors one sync page into the tasks table, scoped to the
// connection's user (a foreign user's row with the same external id is
// unreachable — GetByExternalID is user-scoped).
func (s *TodoSyncService) applyPage(ctx context.Context, source domain.TaskSource, conn domain.IntegrationConnection, page port.TodoSyncPage) error {
	now := s.clock.Now()
	nextPos := -1.0 // max position, computed lazily on the first create
	for _, incoming := range page.Tasks {
		existing, err := s.tasks.GetByExternalID(ctx, conn.UserID, source, incoming.ExternalID)
		switch {
		case err == nil:
			// Vendor-owned fields refresh; local planning state (Scheduled*,
			// Position, CompletedAt) is preserved — the vendor doesn't own it.
			existing.Title = incoming.Title
			existing.Notes = incoming.Notes
			existing.Due = incoming.Due
			existing.AllDayDue = incoming.AllDayDue
			existing.SourceURL = incoming.SourceURL
			existing.UpdatedAt = now
			if err := s.tasks.Update(ctx, existing); err != nil {
				return err
			}
		case errors.Is(err, domain.ErrNotFound):
			if nextPos < 0 {
				all, lerr := s.tasks.List(ctx, port.TaskQuery{UserID: conn.UserID, IncludeCompleted: true})
				if lerr != nil {
					return lerr
				}
				nextPos = 0
				for _, t := range all {
					if t.Position > nextPos {
						nextPos = t.Position
					}
				}
			}
			nextPos += positionStep
			t := incoming
			t.ID = ""
			t.UserID = conn.UserID
			t.Source = source
			t.Position = nextPos
			t.CreatedAt = now
			t.UpdatedAt = now
			if _, err := s.tasks.Create(ctx, t); err != nil {
				return err
			}
		default:
			return err
		}
	}
	for _, externalID := range page.DeletedIDs {
		existing, err := s.tasks.GetByExternalID(ctx, conn.UserID, source, externalID)
		if errors.Is(err, domain.ErrNotFound) {
			continue // never mirrored, or already gone
		}
		if err != nil {
			return err
		}
		if err := s.tasks.Delete(ctx, existing.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}

// markError records a failed pass on the connection so the integrations UI
// can surface it. A concurrently deleted connection is not an error.
func (s *TodoSyncService) markError(ctx context.Context, conn domain.IntegrationConnection, syncErr error) {
	msg := syncErr.Error()
	conn.Status = domain.IntegrationStatusError
	conn.LastError = &msg
	if err := s.integrations.Update(ctx, conn); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.logger.Error("todo sync: record connection error", "connection_id", conn.ID, "error", err)
	}
}

// markHealthy clears a previously recorded error after a clean pass.
func (s *TodoSyncService) markHealthy(ctx context.Context, conn domain.IntegrationConnection) {
	if conn.Status == domain.IntegrationStatusActive && conn.LastError == nil {
		return
	}
	conn.Status = domain.IntegrationStatusActive
	conn.LastError = nil
	if err := s.integrations.Update(ctx, conn); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.logger.Error("todo sync: clear connection error", "connection_id", conn.ID, "error", err)
	}
}
