package domain

import (
	"errors"
	"testing"
)

func TestNewCalendarSubscriptionHTTPSOnly(t *testing.T) {
	tests := []struct {
		name string
		url  string
		ok   bool
	}{
		{"https accepted", "https://example.com/holidays.ics", true},
		{"https with query accepted", "https://example.com/cal?type=ics", true},
		{"whitespace trimmed", "  https://example.com/a.ics  ", true},
		{"plain http rejected", "http://example.com/holidays.ics", false},
		{"webcal rejected", "webcal://example.com/holidays.ics", false},
		{"file rejected", "file:///etc/passwd", false},
		{"scheme-relative rejected", "//example.com/holidays.ics", false},
		{"relative path rejected", "/holidays.ics", false},
		{"empty rejected", "", false},
		{"garbage rejected", "::::not a url", false},
		{"https without host rejected", "https://", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := NewCalendarSubscription("u1", tt.url, "", "")
			if tt.ok {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if sub.UserID != "u1" || !sub.IsVisible {
					t.Fatalf("sub = %+v, want UserID=u1 IsVisible=true", sub)
				}
				if sub.Color != DefaultSubscriptionColor {
					t.Fatalf("Color = %q, want default %q", sub.Color, DefaultSubscriptionColor)
				}
				return
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestNewCalendarSubscriptionKeepsExplicitColor(t *testing.T) {
	sub, err := NewCalendarSubscription("u1", "https://example.com/a.ics", "Team", "#ff0000")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sub.Color != "#ff0000" {
		t.Fatalf("Color = %q, want #ff0000", sub.Color)
	}
	if sub.Name != "Team" {
		t.Fatalf("Name = %q, want Team", sub.Name)
	}
}

func TestCalendarSubscriptionResolveName(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		feedName string
		want     string
	}{
		{"user label wins", "My Holidays", "US Holidays", "My Holidays"},
		{"feed X-WR-CALNAME fallback", "", "US Holidays", "US Holidays"},
		{"feed name trimmed", "", "  US Holidays  ", "US Holidays"},
		{"host fallback", "", "", "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := NewCalendarSubscription("u1", "https://example.com/a.ics", tt.userName, "")
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			sub.ResolveName(tt.feedName)
			if sub.Name != tt.want {
				t.Fatalf("Name = %q, want %q", sub.Name, tt.want)
			}
		})
	}
}
