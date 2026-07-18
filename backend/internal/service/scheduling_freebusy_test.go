package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- SchedulingService.GuestFreeBusy fixture ---------------------------------

func TestGuestFreeBusy_EmailCountValidation(t *testing.T) {
	f := newPollFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		emails []string
	}{
		{"zero emails", nil},
		{"too many emails", func() []string {
			emails := make([]string, 21)
			for i := range emails {
				emails[i] = "guest@example.com"
			}
			return emails
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.GuestFreeBusy(ctx, "u1", port.FreeBusyRequest{
				Emails: tc.emails, From: base, To: base.Add(time.Hour),
			})
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("GuestFreeBusy() err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestGuestFreeBusy_SpanValidation(t *testing.T) {
	f := newPollFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	t.Run("to before from", func(t *testing.T) {
		_, err := f.svc.GuestFreeBusy(ctx, "u1", port.FreeBusyRequest{
			Emails: []string{"guest@example.com"}, From: base, To: base.Add(-time.Hour),
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("GuestFreeBusy() err = %v, want ErrValidation", err)
		}
	})

	t.Run("span over 14 days", func(t *testing.T) {
		_, err := f.svc.GuestFreeBusy(ctx, "u1", port.FreeBusyRequest{
			Emails: []string{"guest@example.com"}, From: base, To: base.Add(15 * 24 * time.Hour),
		})
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("GuestFreeBusy() err = %v, want ErrValidation", err)
		}
	})
}

func TestGuestFreeBusy_MergesProviderResult(t *testing.T) {
	f := newPollFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	from, to := base, base.Add(24*time.Hour)

	f.calProv.freeBusyResult = map[string][]domain.BusyInterval{
		"Guest@Example.com": {{Start: from.Add(time.Hour), End: from.Add(2 * time.Hour)}},
	}

	got, err := f.svc.GuestFreeBusy(ctx, "u1", port.FreeBusyRequest{
		Emails: []string{"guest@example.com"}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("GuestFreeBusy: %v", err)
	}
	intervals, ok := got["guest@example.com"]
	if !ok || len(intervals) != 1 {
		t.Fatalf("GuestFreeBusy() = %+v, want one interval keyed by lowercased email", got)
	}
	if !f.calProv.lastFreeBusyFrom.Equal(from) || !f.calProv.lastFreeBusyTo.Equal(to) {
		t.Fatalf("provider FreeBusy called with from=%v to=%v, want %v/%v",
			f.calProv.lastFreeBusyFrom, f.calProv.lastFreeBusyTo, from, to)
	}
}

func TestGuestFreeBusy_SkipsAccountOnProviderError(t *testing.T) {
	f := newPollFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	f.calProv.freeBusyErr = errors.New("provider unavailable")

	got, err := f.svc.GuestFreeBusy(ctx, "u1", port.FreeBusyRequest{
		Emails: []string{"guest@example.com"}, From: base, To: base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("GuestFreeBusy: %v, want nil (provider errors are skipped, not fatal)", err)
	}
	if len(got) != 0 {
		t.Fatalf("GuestFreeBusy() = %+v, want empty result when the only account's provider errors", got)
	}
}
