package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
	"calendium/backend/internal/service"
)

// tombUsers is a port.UserRepo double whose Upsert refuses a tombstoned id
// the way the SQL does (deleted_users consulted in the same statement).
type tombUsers struct {
	byID       map[string]domain.User
	tombstones map[string]bool
	upserts    int
}

func (r *tombUsers) Upsert(_ context.Context, u domain.User) (domain.User, error) {
	r.upserts++
	if r.tombstones[u.ID] {
		return domain.User{}, domain.ErrUserDeleted
	}
	r.byID[u.ID] = u
	return u, nil
}

func (r *tombUsers) GetByID(_ context.Context, id string) (domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (r *tombUsers) Delete(_ context.Context, id string) error {
	if _, ok := r.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

func (r *tombUsers) Tombstone(_ context.Context, id string) error {
	r.tombstones[id] = true
	return nil
}

var _ port.UserRepo = (*tombUsers)(nil)

type tombPrefs struct{}

func (tombPrefs) Get(context.Context, string) (port.UserPreferences, error) {
	return port.UserPreferences{}, nil
}
func (tombPrefs) Put(context.Context, string, port.UserPreferences) error { return nil }

type tombClock struct{}

func (tombClock) Now() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }

// A still-valid JWT for a purged user (mobile sign-out's device unregister,
// a background query) is answered 401 and never re-creates the users row.
func TestRequireAuthRejectsTombstonedSubject(t *testing.T) {
	h := newHarness(t)
	repo := &tombUsers{byID: map[string]domain.User{}, tombstones: map[string]bool{defaultUserID: true}}
	h.deps.Users = service.NewUserService(repo, tombPrefs{}, tombClock{})

	rec := h.authed(http.MethodDelete, "/v1/devices/dev-1", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec); got.Code != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", got.Code)
	}
	if _, ok := repo.byID[defaultUserID]; ok {
		t.Fatal("the purged user's row was re-created")
	}
	if repo.upserts != 1 {
		t.Fatalf("upserts = %d, want exactly one (single guarded statement)", repo.upserts)
	}

	// A live subject on the same stack still authenticates.
	delete(repo.tombstones, defaultUserID)
	if rec := h.authed(http.MethodGet, "/v1/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("live user GET /v1/me = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}
