package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMentions(t *testing.T) {
	members := map[string]string{
		"ada@example.com": "u-ada",
		"bob@example.com": "u-bob",
	}

	tests := []struct {
		name string
		body string
		want []string
	}{
		{"single mention", "ping @ada@example.com please", []string{"u-ada"}},
		{"two mentions in order", "@bob@example.com then @ada@example.com", []string{"u-bob", "u-ada"}},
		{"case insensitive", "hi @Ada@Example.COM", []string{"u-ada"}},
		{"duplicate deduped", "@ada@example.com and again @ada@example.com", []string{"u-ada"}},
		{"non-member dropped silently", "see @ghost@example.com", []string{}},
		{"plain email is not a mention", "mail ada@example.com directly", []string{}},
		{"no mentions", "nothing to see", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.body, members)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMentions(%q) = %v, want %v", tt.body, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseMentions(%q) = %v, want %v", tt.body, got, tt.want)
				}
			}
		})
	}
}

func TestValidateCommentBody(t *testing.T) {
	if err := ValidateCommentBody("hello"); err != nil {
		t.Fatalf("valid body: %v", err)
	}
	if err := ValidateCommentBody(strings.Repeat("x", MaxCommentBodyChars)); err != nil {
		t.Fatalf("body at cap: %v", err)
	}
	if err := ValidateCommentBody(strings.Repeat("x", MaxCommentBodyChars+1)); !errors.Is(err, ErrValidation) {
		t.Fatalf("over cap err = %v, want ErrValidation", err)
	}
	// Rune count, not bytes: multibyte characters at the cap are fine.
	if err := ValidateCommentBody(strings.Repeat("é", MaxCommentBodyChars)); err != nil {
		t.Fatalf("multibyte body at cap: %v", err)
	}
	for _, blank := range []string{"", "   ", "\n\t"} {
		if err := ValidateCommentBody(blank); !errors.Is(err, ErrValidation) {
			t.Fatalf("blank %q err = %v, want ErrValidation", blank, err)
		}
	}
}
