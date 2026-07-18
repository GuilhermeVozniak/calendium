package domain

import (
	"encoding/json"
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

func TestValidateSlug(t *testing.T) {
	valid := []string{"a", "ab", "my-booking-link", "a1-b2-c3", "123", "x0x"}
	for _, s := range valid {
		s := s
		t.Run("valid/"+s, func(t *testing.T) {
			if err := ValidateSlug(s); err != nil {
				t.Fatalf("ValidateSlug(%q) unexpected err: %v", s, err)
			}
		})
	}

	invalid := []struct{ slug, label string }{
		{"", "empty"},
		{"Abc", "uppercase"},
		{"ABC", "all-uppercase"},
		{"-abc", "leading-hyphen"},
		{"abc-", "trailing-hyphen"},
		{"a_b", "underscore"},
		{"a b", "space"},
		{"a.b", "dot"},
	}
	for _, tc := range invalid {
		tc := tc
		t.Run("invalid/"+tc.label, func(t *testing.T) {
			if err := ValidateSlug(tc.slug); !errors.Is(err, ErrValidation) {
				t.Fatalf("ValidateSlug(%q) err = %v, want ErrValidation", tc.slug, err)
			}
		})
	}

	// 65-char slug (exceeds the 64-char max).
	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	t.Run("invalid/too-long", func(t *testing.T) {
		if err := ValidateSlug(long); !errors.Is(err, ErrValidation) {
			t.Fatalf("ValidateSlug(65-char) err = %v, want ErrValidation", err)
		}
	})

	// Reserved slugs.
	for _, s := range []string{"api", "www", "book", "admin", "app", "settings", "pricing", "docs", "signin", "poll"} {
		s := s
		t.Run("reserved/"+s, func(t *testing.T) {
			if err := ValidateSlug(s); !errors.Is(err, ErrValidation) {
				t.Fatalf("ValidateSlug(%q) err = %v, want ErrValidation (reserved)", s, err)
			}
		})
	}
}

func TestAvailabilityWindowValidate(t *testing.T) {
	tests := []struct {
		name    string
		window  AvailabilityWindow
		wantErr bool
	}{
		{"valid", AvailabilityWindow{Weekday: 1, Start: "09:00", End: "17:00"}, false},
		{"valid sunday", AvailabilityWindow{Weekday: 0, Start: "00:00", End: "23:59"}, false},
		{"valid saturday", AvailabilityWindow{Weekday: 6, Start: "08:30", End: "12:15"}, false},
		{"weekday negative", AvailabilityWindow{Weekday: -1, Start: "09:00", End: "17:00"}, true},
		{"weekday too large", AvailabilityWindow{Weekday: 7, Start: "09:00", End: "17:00"}, true},
		{"bad start hour out of range", AvailabilityWindow{Weekday: 1, Start: "25:00", End: "17:00"}, true},
		{"bad start garbage", AvailabilityWindow{Weekday: 1, Start: "abc", End: "17:00"}, true},
		{"bad end format", AvailabilityWindow{Weekday: 1, Start: "09:00", End: "5pm"}, true},
		{"bad end minute out of range", AvailabilityWindow{Weekday: 1, Start: "09:00", End: "17:60"}, true},
		{"end equals start", AvailabilityWindow{Weekday: 1, Start: "09:00", End: "09:00"}, true},
		{"end before start", AvailabilityWindow{Weekday: 1, Start: "17:00", End: "09:00"}, true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := tt.window.Validate()
			if tt.wantErr && !errors.Is(err, ErrValidation) {
				t.Fatalf("Validate() err = %v, want ErrValidation", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() unexpected err: %v", err)
			}
		})
	}
}

// jsonKeys unmarshal-decodes b into a map and reports its top-level keys, for
// asserting which fields serialize.
func jsonKeys(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	return m
}

func TestReactionJSONRoundTrip(t *testing.T) {
	created := time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC)
	r := Reaction{
		ID:        "reaction_1",
		MessageID: "msg_1",
		UserID:    "user_1",
		Emoji:     "thumbsup",
		Delivery:  "local",
		CreatedAt: created,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m := jsonKeys(t, b)
	if _, ok := m["userId"]; ok {
		t.Fatalf("Reaction JSON = %s, want no userId key (UserID is json:\"-\")", b)
	}
	for _, key := range []string{"id", "messageId", "emoji", "delivery", "createdAt"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("Reaction JSON = %s, want key %q", b, key)
		}
	}

	var got Reaction
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.UserID != "" {
		t.Fatalf("round-tripped UserID = %q, want empty (not serialized)", got.UserID)
	}
	got.UserID = r.UserID // restore before comparing the rest
	if got != r {
		t.Fatalf("round-tripped Reaction = %+v, want %+v", got, r)
	}
}

func TestOpenEventJSONRoundTrip(t *testing.T) {
	name := "Ada Lovelace"
	e := OpenEvent{
		MessageID:  "msg_1",
		ThreadID:   "thread_1",
		AccountID:  "account_1",
		Subject:    "Re: Proposal",
		Recipients: []EmailAddress{{Name: &name, Email: "ada@example.com"}},
		OpenedAt:   time.Date(2026, time.July, 18, 9, 30, 0, 0, time.UTC),
		SentAt:     time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got OpenEvent
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.OpenedAt.Equal(e.OpenedAt) || !got.SentAt.Equal(e.SentAt) {
		t.Fatalf("round-tripped times = %+v, want %+v", got, e)
	}
	got.OpenedAt, got.SentAt = e.OpenedAt, e.SentAt
	if len(got.Recipients) != 1 || got.Recipients[0].Email != e.Recipients[0].Email {
		t.Fatalf("round-tripped Recipients = %+v, want %+v", got.Recipients, e.Recipients)
	}
	if got.MessageID != e.MessageID || got.ThreadID != e.ThreadID || got.AccountID != e.AccountID || got.Subject != e.Subject {
		t.Fatalf("round-tripped OpenEvent = %+v, want %+v", got, e)
	}
}

func TestAttachmentHitJSONRoundTrip(t *testing.T) {
	h := AttachmentHit{
		Attachment: Attachment{
			ID:                   "att_1",
			Filename:             "invoice.pdf",
			MimeType:             "application/pdf",
			SizeBytes:            1024,
			ProviderAttachmentID: "provider-secret-id",
		},
		MessageID:     "msg_1",
		ThreadID:      "thread_1",
		ThreadSubject: "Invoice attached",
		From:          EmailAddress{Email: "billing@example.com"},
		SentAt:        time.Date(2026, time.July, 18, 8, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m := jsonKeys(t, b)
	if _, ok := m["providerAttachmentId"]; ok {
		t.Fatalf("AttachmentHit JSON = %s, want no providerAttachmentId key", b)
	}
	for _, key := range []string{"id", "filename", "mimeType", "sizeBytes", "messageId", "threadId", "threadSubject", "from", "sentAt"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("AttachmentHit JSON = %s, want key %q", b, key)
		}
	}

	var got AttachmentHit
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ProviderAttachmentID != "" {
		t.Fatalf("round-tripped ProviderAttachmentID = %q, want empty (not serialized)", got.ProviderAttachmentID)
	}
	if got.ID != h.ID || got.Filename != h.Filename || got.MessageID != h.MessageID || got.ThreadSubject != h.ThreadSubject || got.From.Email != h.From.Email {
		t.Fatalf("round-tripped AttachmentHit = %+v, want %+v", got, h)
	}
}

func TestSendSuggestionJSONRoundTrip(t *testing.T) {
	s := SendSuggestion{
		Email:          "ada@example.com",
		SuggestedAt:    time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC),
		UTCOffsetHours: -5,
		Confidence:     0.82,
		SampleSize:     12,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got SendSuggestion
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.SuggestedAt.Equal(s.SuggestedAt) {
		t.Fatalf("round-tripped SuggestedAt = %v, want %v", got.SuggestedAt, s.SuggestedAt)
	}
	got.SuggestedAt = s.SuggestedAt
	if got != s {
		t.Fatalf("round-tripped SendSuggestion = %+v, want %+v", got, s)
	}
}

func TestContactSummaryJSONRoundTrip(t *testing.T) {
	name := "Ada Lovelace"
	lastMsg := time.Date(2026, time.July, 17, 14, 0, 0, 0, time.UTC)
	cs := ContactSummary{
		Email:         "ada@example.com",
		Name:          &name,
		Domain:        "example.com",
		ThreadCount:   4,
		MessageCount:  10,
		LastMessageAt: &lastMsg,
		RecentThreads: []Thread{{ID: "thread_1", Subject: "Hello"}},
	}
	b, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ContactSummary
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Email != cs.Email || got.Domain != cs.Domain || got.ThreadCount != cs.ThreadCount || got.MessageCount != cs.MessageCount {
		t.Fatalf("round-tripped ContactSummary = %+v, want %+v", got, cs)
	}
	if got.Name == nil || *got.Name != *cs.Name {
		t.Fatalf("round-tripped Name = %v, want %v", got.Name, *cs.Name)
	}
	if got.LastMessageAt == nil || !got.LastMessageAt.Equal(*cs.LastMessageAt) {
		t.Fatalf("round-tripped LastMessageAt = %v, want %v", got.LastMessageAt, *cs.LastMessageAt)
	}
	if len(got.RecentThreads) != 1 || got.RecentThreads[0].ID != cs.RecentThreads[0].ID {
		t.Fatalf("round-tripped RecentThreads = %+v, want %+v", got.RecentThreads, cs.RecentThreads)
	}
}

func TestConnectedAccountSignatureAutoBccJSON(t *testing.T) {
	a := ConnectedAccount{
		ID:            "account_1",
		UserID:        "user_1",
		Provider:      ProviderGoogle,
		Email:         "ada@example.com",
		Status:        AccountActive,
		SignatureHTML: "<p>Best, Ada</p>",
		AutoBcc:       []string{"archive@example.com"},
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	m := jsonKeys(t, b)
	if _, ok := m["userId"]; ok {
		t.Fatalf("ConnectedAccount JSON = %s, want no userId key (UserID is json:\"-\")", b)
	}
	sig, ok := m["signatureHtml"]
	if !ok || sig != a.SignatureHTML {
		t.Fatalf("ConnectedAccount JSON signatureHtml = %v, want %q", sig, a.SignatureHTML)
	}
	bcc, ok := m["autoBcc"].([]any)
	if !ok || len(bcc) != 1 || bcc[0] != "archive@example.com" {
		t.Fatalf("ConnectedAccount JSON autoBcc = %v, want [archive@example.com]", m["autoBcc"])
	}

	var got ConnectedAccount
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SignatureHTML != a.SignatureHTML {
		t.Fatalf("round-tripped SignatureHTML = %q, want %q", got.SignatureHTML, a.SignatureHTML)
	}
	if len(got.AutoBcc) != 1 || got.AutoBcc[0] != a.AutoBcc[0] {
		t.Fatalf("round-tripped AutoBcc = %v, want %v", got.AutoBcc, a.AutoBcc)
	}
}

func TestMessageReactionsDefaultEmptySlice(t *testing.T) {
	m := Message{ID: "msg_1", Reactions: []Reaction{}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	keys := jsonKeys(t, b)
	if _, ok := keys["reactions"]; !ok {
		t.Fatalf("Message JSON = %s, want key \"reactions\"", b)
	}
	var got Message
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Reactions == nil || len(got.Reactions) != 0 {
		t.Fatalf("round-tripped Reactions = %v, want empty non-nil slice", got.Reactions)
	}
}
