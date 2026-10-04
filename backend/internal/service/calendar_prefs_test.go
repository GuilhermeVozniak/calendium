package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// fakeCalendarPrefsRepo is an in-memory port.CalendarPrefsRepo mirroring the
// real repo's contract: Get synthesizes defaults on a missing row.
type fakeCalendarPrefsRepo struct {
	byUser  map[string]domain.CalendarPrefs
	getErr  error
	saveErr error
}

var _ port.CalendarPrefsRepo = (*fakeCalendarPrefsRepo)(nil)

func newCalendarPrefsRepo() *fakeCalendarPrefsRepo {
	return &fakeCalendarPrefsRepo{byUser: map[string]domain.CalendarPrefs{}}
}

func (r *fakeCalendarPrefsRepo) Get(_ context.Context, userID string) (domain.CalendarPrefs, error) {
	if r.getErr != nil {
		return domain.CalendarPrefs{}, r.getErr
	}
	if p, ok := r.byUser[userID]; ok {
		return p, nil
	}
	return domain.DefaultCalendarPrefs(userID), nil
}

func (r *fakeCalendarPrefsRepo) Upsert(_ context.Context, p domain.CalendarPrefs) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.byUser[p.UserID] = p
	return nil
}

func (r *fakeCalendarPrefsRepo) ListAutomated(context.Context) ([]domain.CalendarPrefs, error) {
	out := []domain.CalendarPrefs{}
	for _, p := range r.byUser {
		if p.AutomationEnabled() {
			out = append(out, p)
		}
	}
	return out, nil
}

func newCalendarPrefsService(repo *fakeCalendarPrefsRepo, entitled bool) *PrefsService {
	subs := newSubscriptionRepo()
	if entitled {
		if err := subs.Upsert(context.Background(), domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}); err != nil {
			panic(err)
		}
	}
	return NewPrefsService(newPrefsRepo(), repo, subs, nil, newClock(time.Now()), false)
}

// TestCalendarPrefsServiceGetDefaults: never-saved users get the defaults
// document straight through.
func TestCalendarPrefsServiceGetDefaults(t *testing.T) {
	svc := newCalendarPrefsService(newCalendarPrefsRepo(), true)
	got, err := svc.GetCalendarPrefs(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetCalendarPrefs: %v", err)
	}
	if got.TimeZone != "UTC" || got.WorkdayStartMinutes != 540 || got.TravelMode != domain.TravelDriving {
		t.Fatalf("defaults = %+v", got)
	}
}

// TestCalendarPrefsServicePatchMerge covers the read-merge-write pipeline:
// only patched fields change, the merged doc persists, and a second patch
// merges over the first (not over defaults).
func TestCalendarPrefsServicePatchMerge(t *testing.T) {
	repo := newCalendarPrefsRepo()
	svc := newCalendarPrefsService(repo, true)
	ctx := context.Background()

	goal := 12 * 60
	tz := "Europe/Amsterdam"
	got, err := svc.UpdateCalendarPrefs(ctx, "u1", domain.CalendarPrefsPatch{
		FocusGoalMinutesPerWeek: &goal,
		TimeZone:                &tz,
	})
	if err != nil {
		t.Fatalf("UpdateCalendarPrefs: %v", err)
	}
	if got.FocusGoalMinutesPerWeek != goal || got.TimeZone != tz {
		t.Fatalf("patched = %+v", got)
	}
	if got.WorkdayStartMinutes != 540 || got.WorkdayEndMinutes != 1020 {
		t.Fatalf("untouched fields must keep defaults: %+v", got)
	}
	if saved, ok := repo.byUser["u1"]; !ok || saved.FocusGoalMinutesPerWeek != goal {
		t.Fatalf("merged doc not persisted: %+v", repo.byUser)
	}

	buffer := 15
	got2, err := svc.UpdateCalendarPrefs(ctx, "u1", domain.CalendarPrefsPatch{AutoBufferMinutes: &buffer})
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if got2.AutoBufferMinutes != 15 || got2.FocusGoalMinutesPerWeek != goal || got2.TimeZone != tz {
		t.Fatalf("second patch must merge over the first: %+v", got2)
	}

	// Round trip through the getter.
	got3, err := svc.GetCalendarPrefs(ctx, "u1")
	if err != nil {
		t.Fatalf("GetCalendarPrefs after patches: %v", err)
	}
	if got3.AutoBufferMinutes != 15 || got3.FocusGoalMinutesPerWeek != goal {
		t.Fatalf("get after patches = %+v", got3)
	}
}

// TestCalendarPrefsServiceValidation: an invalid merged document is
// ErrValidation and nothing persists.
func TestCalendarPrefsServiceValidation(t *testing.T) {
	repo := newCalendarPrefsRepo()
	svc := newCalendarPrefsService(repo, true)
	ctx := context.Background()

	bad := 3 // buffers are 0 or 5..30
	if _, err := svc.UpdateCalendarPrefs(ctx, "u1", domain.CalendarPrefsPatch{AutoBufferMinutes: &bad}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	tz := "Not/AZone"
	if _, err := svc.UpdateCalendarPrefs(ctx, "u1", domain.CalendarPrefsPatch{TimeZone: &tz}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if len(repo.byUser) != 0 {
		t.Fatalf("invalid patches must not persist: %+v", repo.byUser)
	}
}

// TestCalendarPrefsServiceEntitlement: both calendar-prefs methods are
// paywalled (402 without an active subscription) — unlike the ungated
// split-order prefs on the same service.
func TestCalendarPrefsServiceEntitlement(t *testing.T) {
	svc := newCalendarPrefsService(newCalendarPrefsRepo(), false)
	ctx := context.Background()

	if _, err := svc.GetCalendarPrefs(ctx, "u1"); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("GetCalendarPrefs err = %v, want ErrPaymentRequired", err)
	}
	goal := 300
	if _, err := svc.UpdateCalendarPrefs(ctx, "u1", domain.CalendarPrefsPatch{FocusGoalMinutesPerWeek: &goal}); !errors.Is(err, domain.ErrPaymentRequired) {
		t.Fatalf("UpdateCalendarPrefs err = %v, want ErrPaymentRequired", err)
	}

	// The split-order surface stays ungated on the same service instance.
	if _, err := svc.GetPrefs(ctx, "u1"); err != nil {
		t.Fatalf("GetPrefs must not be paywalled, got %v", err)
	}
}
