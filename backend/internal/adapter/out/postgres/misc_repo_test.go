package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- BillingEventRepo ------------------------------------------------------

func TestBillingEventRepoRecordDedupByNotificationID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	first, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 1: %v", err)
	}
	if !first {
		t.Fatal("first Record must report first = true")
	}
	again, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_1", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 2: %v", err)
	}
	if again {
		t.Fatal("same notification id must report first = false")
	}
	// A Paddle replay: same event id, NEW notification id -> recorded as first
	// (the service's occurred_at guard decides whether it changes anything).
	replay, err := st.BillingEvents().Record(ctx, port.SubscriptionEvent{NotificationID: "ntf_2", EventID: "evt_1", Type: "subscription.updated", OccurredAt: at})
	if err != nil {
		t.Fatalf("Record 3: %v", err)
	}
	if !replay {
		t.Fatal("a new notification id for a replayed event id must be first = true")
	}
}

// The webhook pipeline records the notification and applies it in one tx:
// when the apply fails, the marker must roll back so Paddle's retry is
// processed (first = true) instead of being swallowed as a duplicate.
func TestBillingEventRepoRecordRollsBackWithFailedApply(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	ev := port.SubscriptionEvent{NotificationID: "ntf_rb", EventID: "evt_rb", Type: "subscription.updated", OccurredAt: at}
	applyErr := errors.New("apply failed")

	err := st.RunInTx(ctx, func(ctx context.Context) error {
		first, err := st.BillingEvents().Record(ctx, ev)
		if err != nil || !first {
			t.Fatalf("Record in tx: first=%v err=%v", first, err)
		}
		if _, err := st.Subscriptions().UpsertIfNewer(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive, LastEventAt: &at}); err != nil {
			t.Fatalf("UpsertIfNewer in tx: %v", err)
		}
		return applyErr
	})
	if !errors.Is(err, applyErr) {
		t.Fatalf("RunInTx = %v, want the apply error", err)
	}
	if _, err := st.Subscriptions().GetByUserID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("apply must roll back, GetByUserID err = %v", err)
	}
	retry, err := st.BillingEvents().Record(ctx, ev)
	if err != nil {
		t.Fatalf("Record retry: %v", err)
	}
	if !retry {
		t.Fatal("the marker must roll back with the failed apply so the retry is first = true")
	}
}

// --- OAuthStateRepo --------------------------------------------------------------

func TestOAuthStateRepoCreateAndConsume(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	state := newID()
	if err := st.OAuthStates().Create(ctx, port.OAuthState{
		State:        state,
		UserID:       "u1",
		Provider:     domain.ProviderGoogle,
		RedirectURL:  "https://app.example.com/callback",
		CodeVerifier: "verifier-123",
		ExpiresAt:    time.Now().Add(10 * time.Minute).UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	consumed, err := st.OAuthStates().Consume(ctx, state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.UserID != "u1" || consumed.Provider != domain.ProviderGoogle ||
		consumed.RedirectURL != "https://app.example.com/callback" || consumed.CodeVerifier != "verifier-123" {
		t.Fatalf("consumed = %+v", consumed)
	}

	if _, err := st.OAuthStates().Consume(ctx, state); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Consume: err = %v, want ErrNotFound", err)
	}
}

func TestOAuthStateRepoConsumeNeverCreated(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.OAuthStates().Consume(context.Background(), "never-existed"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestOAuthStateRepoEmptyCodeVerifier(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	state := newID()
	if err := st.OAuthStates().Create(ctx, port.OAuthState{
		State:       state,
		UserID:      "u1",
		Provider:    domain.ProviderGoogle,
		RedirectURL: "https://app.example.com/callback",
		ExpiresAt:   time.Now().Add(10 * time.Minute).UTC().Truncate(time.Microsecond),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	consumed, err := st.OAuthStates().Consume(ctx, state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.CodeVerifier != "" {
		t.Fatalf("CodeVerifier = %q, want empty", consumed.CodeVerifier)
	}
}

// --- SyncStateRepo -----------------------------------------------------------

func TestSyncStateRepoSaveGetUpsert(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	if err := st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "mail", Cursor: "c1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.SyncStates().Get(ctx, acct.ID, "mail")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cursor != "c1" {
		t.Fatalf("Cursor = %q, want c1", got.Cursor)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt must be non-zero")
	}

	if err := st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "mail", Cursor: "c2"}); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	got2, err := st.SyncStates().Get(ctx, acct.ID, "mail")
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got2.Cursor != "c2" {
		t.Fatalf("Cursor after upsert = %q, want c2", got2.Cursor)
	}
}

func TestSyncStateRepoGetMissing(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	if _, err := st.SyncStates().Get(ctx, acct.ID, "unknown-resource"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSyncStateRepoDeleteByAccount(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	acct := seedAccount(t, st, "u1")

	if err := st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "mail", Cursor: "c1"}); err != nil {
		t.Fatalf("Save mail: %v", err)
	}
	if err := st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "calendars", Cursor: "c2"}); err != nil {
		t.Fatalf("Save calendars: %v", err)
	}

	if err := st.SyncStates().DeleteByAccount(ctx, acct.ID); err != nil {
		t.Fatalf("DeleteByAccount: %v", err)
	}
	if _, err := st.SyncStates().Get(ctx, acct.ID, "mail"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get mail after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := st.SyncStates().Get(ctx, acct.ID, "calendars"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get calendars after delete: err = %v, want ErrNotFound", err)
	}

	if err := st.SyncStates().DeleteByAccount(ctx, "no-such-account"); err != nil {
		t.Fatalf("DeleteByAccount on empty account: err = %v, want nil", err)
	}
}

// --- DeviceRepo --------------------------------------------------------------

func TestDeviceRepoUpsertNewAndGetByID(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	d, err := st.Devices().Upsert(ctx, domain.NotificationDevice{
		UserID: "u1", Platform: domain.PlatformIOS, Token: "tok-A",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if d.ID == "" {
		t.Fatal("Upsert did not assign an id")
	}
	if d.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be non-zero")
	}

	got, err := st.Devices().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Platform != domain.PlatformIOS || got.Token != "tok-A" {
		t.Fatalf("got = %+v", got)
	}
}

func TestDeviceRepoUpsertSameTokenIsIdempotent(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	first, err := st.Devices().Upsert(ctx, domain.NotificationDevice{
		UserID: "u1", Platform: domain.PlatformIOS, Token: "tok-A",
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}

	second, err := st.Devices().Upsert(ctx, domain.NotificationDevice{
		UserID: "u1", Platform: domain.PlatformMacOS, Token: "tok-A",
	})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("Upsert on same (userID, token) changed id: %s -> %s", first.ID, second.ID)
	}
	if second.Platform != domain.PlatformMacOS {
		t.Fatalf("Platform = %q, want macos", second.Platform)
	}

	list, err := st.Devices().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByUser = %+v, want exactly one row", list)
	}
}

func TestDeviceRepoListByUserOrdered(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	a, err := st.Devices().Upsert(ctx, domain.NotificationDevice{UserID: "u1", Platform: domain.PlatformIOS, Token: "tok-A"})
	if err != nil {
		t.Fatalf("Upsert a: %v", err)
	}
	b, err := st.Devices().Upsert(ctx, domain.NotificationDevice{UserID: "u1", Platform: domain.PlatformAndroid, Token: "tok-B"})
	if err != nil {
		t.Fatalf("Upsert b: %v", err)
	}

	list, err := st.Devices().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("ListByUser = %+v, want [a, b] ordered by created_at", list)
	}
}

func TestDeviceRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")

	d, err := st.Devices().Upsert(ctx, domain.NotificationDevice{UserID: "u1", Platform: domain.PlatformWeb, Token: "tok-Z"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := st.Devices().Delete(ctx, d.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Devices().GetByID(ctx, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrNotFound", err)
	}
	if err := st.Devices().Delete(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
	if _, err := st.Devices().GetByID(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID unknown: err = %v, want ErrNotFound", err)
	}
}
