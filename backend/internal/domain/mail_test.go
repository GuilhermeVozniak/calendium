package domain

import (
	"errors"
	"testing"
)

func TestParseThreadView(t *testing.T) {
	for _, s := range []string{"starred", "snoozed", "sent"} {
		v, err := ParseThreadView(s)
		if err != nil || string(v) != s {
			t.Fatalf("ParseThreadView(%q) = %q, %v; want %q, nil", s, v, err, s)
		}
	}
	// Pseudo-view names are not label ids and label ids are not views.
	for _, s := range []string{"", "labelId", "drafts", "important"} {
		if _, err := ParseThreadView(s); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParseThreadView(%q) err = %v, want ErrValidation", s, err)
		}
	}
}
