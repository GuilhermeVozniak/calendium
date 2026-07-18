package domain

import (
	"errors"
	"testing"
	"time"
)

// checkValidator exercises a Parse* validator: every value in valid must
// round-trip (parse ok and stringify back to the input), and every value in
// invalid must fail with ErrValidation and yield the zero enum value.
func checkValidator[T ~string](t *testing.T, name string, parse func(string) (T, error), valid, invalid []string) {
	t.Helper()
	for _, s := range valid {
		s := s
		t.Run(name+"/valid/"+s, func(t *testing.T) {
			got, err := parse(s)
			if err != nil {
				t.Fatalf("%s(%q) unexpected err: %v", name, s, err)
			}
			if string(got) != s {
				t.Fatalf("%s(%q) = %q, want round-trip %q", name, s, string(got), s)
			}
		})
	}
	for _, s := range invalid {
		s := s
		label := s
		if label == "" {
			label = "<empty>"
		}
		t.Run(name+"/invalid/"+label, func(t *testing.T) {
			got, err := parse(s)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("%s(%q) err = %v, want ErrValidation", name, s, err)
			}
			if string(got) != "" {
				t.Fatalf("%s(%q) = %q, want zero value on error", name, s, string(got))
			}
		})
	}
}

func TestParseProvider(t *testing.T) {
	checkValidator(t, "ParseProvider", ParseProvider,
		[]string{"google", "microsoft"},
		[]string{"", "Google", "GOOGLE", "yahoo", "gmail", " google"})
}

func TestParseAiAction(t *testing.T) {
	checkValidator(t, "ParseAiAction", ParseAiAction,
		[]string{"compose", "reply", "summarize", "ask", "improve", "shorten", "simplify", "fix_grammar", "change_tone"},
		[]string{"", "Compose", "translate", "summarise", "answer"})
}

func TestParseAiJobKind(t *testing.T) {
	checkValidator(t, "ParseAiJobKind", ParseAiJobKind,
		[]string{"thread_summary", "instant_replies", "auto_draft", "classify", "reminder_detect", "voice_profile"},
		[]string{"", "ThreadSummary", "thread-summary", "unknown"})
}

func TestParseRsvpStatus(t *testing.T) {
	checkValidator(t, "ParseRsvpStatus", ParseRsvpStatus,
		[]string{"accepted", "declined", "tentative", "needs_action"},
		[]string{"", "maybe", "needsAction", "needs-action", "accept"})
}

func TestParseInboxSplit(t *testing.T) {
	checkValidator(t, "ParseInboxSplit", ParseInboxSplit,
		[]string{"important", "vip", "team", "calendar", "news", "social", "other"},
		[]string{"", "VIP", "inbox", "spam", "starred", "promotions"})
}

func TestParseThreadAction(t *testing.T) {
	checkValidator(t, "ParseThreadAction", ParseThreadAction,
		[]string{"archive", "trash", "star", "unstar", "read", "unread", "spam", "move_to_inbox"},
		[]string{"", "delete", "moveToInbox", "move-to-inbox", "snooze", "Archive"})
}

func TestParseDevicePlatform(t *testing.T) {
	checkValidator(t, "ParseDevicePlatform", ParseDevicePlatform,
		[]string{"ios", "android", "web", "macos", "windows", "linux"},
		[]string{"", "iOS", "macOS", "blackberry", "tvos", "desktop"})
}

func TestSubscriptionHasAccess(t *testing.T) {
	// Fixed reference instant; no time.Now() anywhere in the assertions.
	periodEnd := time.Date(2026, time.July, 7, 12, 0, 0, 0, time.UTC)
	graceEnd := periodEnd.Add(PastDueGrace) // periodEnd + 7*24h

	ptr := func(tm time.Time) *time.Time { return &tm }

	tests := []struct {
		name string
		sub  Subscription
		now  time.Time
		want bool
	}{
		{
			name: "trialing always has access",
			sub:  Subscription{Status: SubscriptionTrialing},
			now:  periodEnd,
			want: true,
		},
		{
			name: "active always has access",
			sub:  Subscription{Status: SubscriptionActive},
			now:  periodEnd,
			want: true,
		},
		{
			name: "past_due with nil period end has no access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: nil},
			now:  periodEnd,
			want: false,
		},
		{
			name: "past_due just before end+grace keeps access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd.Add(-time.Nanosecond),
			want: true,
		},
		{
			name: "past_due exactly at end+grace loses access (Before is strict)",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd,
			want: false,
		},
		{
			name: "past_due after end+grace loses access",
			sub:  Subscription{Status: SubscriptionPastDue, CurrentPeriodEnd: ptr(periodEnd)},
			now:  graceEnd.Add(time.Hour),
			want: false,
		},
		{
			// Future period end is irrelevant once canceled — status wins.
			name: "canceled has no access even with future period end",
			sub:  Subscription{Status: SubscriptionCanceled, CurrentPeriodEnd: ptr(graceEnd.Add(365 * 24 * time.Hour))},
			now:  periodEnd,
			want: false,
		},
		{
			name: "expired has no access",
			sub:  Subscription{Status: SubscriptionExpired},
			now:  periodEnd,
			want: false,
		},
		{
			name: "none has no access",
			sub:  Subscription{Status: SubscriptionNone},
			now:  periodEnd,
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.HasAccess(tt.now); got != tt.want {
				t.Fatalf("HasAccess(%v) with status %q = %v, want %v", tt.now, tt.sub.Status, got, tt.want)
			}
		})
	}
}

func TestPageZeroValue(t *testing.T) {
	var p Page[Thread]
	if p.Items != nil {
		t.Fatalf("zero Page.Items = %v, want nil", p.Items)
	}
	if p.NextCursor != nil {
		t.Fatalf("zero Page.NextCursor = %v, want nil", p.NextCursor)
	}
}

func TestPagePopulated(t *testing.T) {
	cursor := "next-page-token"
	p := Page[int]{
		Items:      []int{1, 2, 3},
		NextCursor: &cursor,
	}
	if len(p.Items) != 3 || p.Items[0] != 1 || p.Items[2] != 3 {
		t.Fatalf("Page.Items = %v, want [1 2 3]", p.Items)
	}
	if p.NextCursor == nil {
		t.Fatalf("Page.NextCursor = nil, want non-nil")
	}
	if *p.NextCursor != cursor {
		t.Fatalf("*Page.NextCursor = %q, want %q", *p.NextCursor, cursor)
	}
}
