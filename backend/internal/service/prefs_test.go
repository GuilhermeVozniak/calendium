package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"calendium/backend/internal/domain"
)

func TestPrefsRoundTripAndValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewPrefsService(newPrefsRepo(), newCalendarPrefsRepo(), newSubscriptionRepo(), nil, newClock(time.Now()), true)

	// Absent prefs come back as the zero value, not an error.
	got, err := svc.GetPrefs(ctx, "u1")
	if err != nil {
		t.Fatalf("GetPrefs(empty): %v", err)
	}
	if len(got.SplitOrder) != 0 {
		t.Fatalf("SplitOrder = %v, want empty", got.SplitOrder)
	}

	want := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitVIP, domain.SplitImportant, domain.SplitOther}}
	saved, err := svc.UpdatePrefs(ctx, "u1", want)
	if err != nil {
		t.Fatalf("UpdatePrefs: %v", err)
	}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved = %+v, want %+v", saved, want)
	}
	got, err = svc.GetPrefs(ctx, "u1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("GetPrefs = %+v, %v", got, err)
	}

	// Unknown and duplicate splits are validation errors.
	if _, err := svc.UpdatePrefs(ctx, "u1", domain.UserPrefs{SplitOrder: []domain.InboxSplit{"bogus"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown split err = %v, want ErrValidation", err)
	}
	dup := domain.UserPrefs{SplitOrder: []domain.InboxSplit{domain.SplitVIP, domain.SplitVIP}}
	if _, err := svc.UpdatePrefs(ctx, "u1", dup); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("duplicate split err = %v, want ErrValidation", err)
	}
}
