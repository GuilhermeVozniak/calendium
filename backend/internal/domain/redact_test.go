package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedactEmail(t *testing.T) {
	cases := map[string]string{
		"ada@example.com":    "***@example.com",
		"Ada.L@Mail.Example": "***@mail.example",
		"no-at-sign":         "***",
		"":                   "***",
		"weird@":             "***",
	}
	for in, want := range cases {
		if got := RedactEmail(in); got != want {
			t.Errorf("RedactEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactEmailsInError(t *testing.T) {
	sentinel := errors.New("550 5.1.1 <Bob@Example.com>: recipient rejected")
	wrapped := fmt.Errorf("smtp: RCPT TO bob@example.com: %w", sentinel)

	err := RedactEmailsInError(wrapped, "bob@example.com")
	if strings.Contains(strings.ToLower(err.Error()), "bob@example.com") {
		t.Fatalf("address survived redaction: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "***@example.com") {
		t.Fatalf("want the domain kept, got %q", err.Error())
	}
	if !errors.Is(err, sentinel) {
		t.Fatal("redaction must keep the error chain (errors.Is)")
	}
	if RedactEmailsInError(nil, "x@y.z") != nil {
		t.Fatal("nil stays nil")
	}
	if plain := errors.New("dial tcp: refused"); RedactEmailsInError(plain, "x@y.z") != plain {
		t.Fatal("an error without the address is returned as is")
	}
}
