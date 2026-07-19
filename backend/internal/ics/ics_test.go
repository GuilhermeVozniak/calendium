package ics

import (
	"slices"
	"strings"
	"testing"
)

func TestUnfold(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"crlf", "A:1\r\nB:2\r\n", []string{"A:1", "B:2"}},
		{"bare lf", "A:1\nB:2", []string{"A:1", "B:2"}},
		{"mixed endings", "A:1\r\nB:2\nC:3", []string{"A:1", "B:2", "C:3"}},
		{"space fold", "SUMMARY:Hello\n  world", []string{"SUMMARY:Hello world"}},
		{"tab fold", "DESC:a\n\tb", []string{"DESC:ab"}},
		{"multi fold", "A:1\n 2\n 3", []string{"A:123"}},
		{"orphan continuation", " A:1", []string{"A:1"}},
		{"blank lines skipped", "A:1\n\r\n\nB:2", []string{"A:1", "B:2"}},
		{"empty input", "", nil},
		{"only blanks", "\n\r\n\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unfold([]byte(tc.in))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("unfold(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnfoldHugeFold(t *testing.T) {
	const parts = 20000
	var b strings.Builder
	b.WriteString("SUMMARY:x")
	for range parts {
		b.WriteString("\n xxxxx")
	}
	lines := unfold([]byte(b.String()))
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if want := len("SUMMARY:x") + parts*5; len(lines[0]) != want {
		t.Fatalf("joined line length = %d, want %d", len(lines[0]), want)
	}
}

func TestParseContentLine(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantName   string
		wantParams map[string]string
		wantValue  string
		wantOK     bool
	}{
		{
			name: "simple", in: "SUMMARY:Hello, world",
			wantName: "SUMMARY", wantParams: map[string]string{}, wantValue: "Hello, world", wantOK: true,
		},
		{
			name: "lowercase name and param", in: "dtstart;tzid=Europe/Lisbon:20260101T093000",
			wantName: "DTSTART", wantParams: map[string]string{"TZID": "Europe/Lisbon"}, wantValue: "20260101T093000", wantOK: true,
		},
		{
			name: "quoted param with semicolon", in: `ATTENDEE;CN="Doe; John";ROLE=REQ-PARTICIPANT:mailto:doe@example.com`,
			wantName: "ATTENDEE", wantParams: map[string]string{"CN": "Doe; John", "ROLE": "REQ-PARTICIPANT"}, wantValue: "mailto:doe@example.com", wantOK: true,
		},
		{
			name: "quoted param with colon", in: `X-COLON;P="a:b":v`,
			wantName: "X-COLON", wantParams: map[string]string{"P": "a:b"}, wantValue: "v", wantOK: true,
		},
		{
			name: "empty value", in: "X-EMPTY:",
			wantName: "X-EMPTY", wantParams: map[string]string{}, wantValue: "", wantOK: true,
		},
		{
			name: "param without equals skipped", in: "SUMMARY;FLAG:v",
			wantName: "SUMMARY", wantParams: map[string]string{}, wantValue: "v", wantOK: true,
		},
		{name: "no colon", in: "no colon here", wantOK: false},
		{name: "empty name", in: ":leading", wantOK: false},
		{name: "params but empty name", in: ";TZID=UTC:v", wantOK: false},
		{name: "unterminated quote hides colon", in: `X;P="a:v`, wantOK: false},
		{name: "empty line", in: "", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl, ok := parseContentLine(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("parseContentLine(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if cl.name != tc.wantName {
				t.Errorf("name = %q, want %q", cl.name, tc.wantName)
			}
			if cl.value != tc.wantValue {
				t.Errorf("value = %q, want %q", cl.value, tc.wantValue)
			}
			if len(cl.params) != len(tc.wantParams) {
				t.Errorf("params = %v, want %v", cl.params, tc.wantParams)
			}
			for k, v := range tc.wantParams {
				if cl.params[k] != v {
					t.Errorf("params[%q] = %q, want %q", k, cl.params[k], v)
				}
			}
		})
	}
}

func TestUnescapeText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"newline lower", `line1\nline2`, "line1\nline2"},
		{"newline upper", `line1\Nline2`, "line1\nline2"},
		{"backslash", `a\\b`, `a\b`},
		{"comma and semicolon", `comma\, semi\; done`, "comma, semi; done"},
		{"unknown escape keeps char", `bad\x`, "badx"},
		{"trailing backslash kept", `trailing\`, `trailing\`},
		{"plain passthrough", "ação — sem escapes", "ação — sem escapes"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unescapeText(tc.in); got != tc.want {
				t.Fatalf("unescapeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
