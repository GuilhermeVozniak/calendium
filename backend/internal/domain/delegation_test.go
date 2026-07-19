package domain

import (
	"errors"
	"testing"
)

func TestParseDelegationScope(t *testing.T) {
	for _, valid := range []string{"mail_read", "mail_write", "calendar_read", "calendar_write"} {
		got, err := ParseDelegationScope(valid)
		if err != nil {
			t.Fatalf("ParseDelegationScope(%q): unexpected error %v", valid, err)
		}
		if string(got) != valid {
			t.Fatalf("ParseDelegationScope(%q) = %q", valid, got)
		}
	}
	for _, invalid := range []string{"", "mail", "MAIL_READ", "billing_read", "*"} {
		if _, err := ParseDelegationScope(invalid); !errors.Is(err, ErrValidation) {
			t.Fatalf("ParseDelegationScope(%q): want ErrValidation, got %v", invalid, err)
		}
	}
}

func TestDelegationHasScope(t *testing.T) {
	d := Delegation{Scopes: []DelegationScope{ScopeMailRead, ScopeCalendarWrite}}
	if !d.HasScope(ScopeMailRead) || !d.HasScope(ScopeCalendarWrite) {
		t.Fatal("HasScope: listed scopes must match")
	}
	if d.HasScope(ScopeMailWrite) || d.HasScope(ScopeCalendarRead) {
		t.Fatal("HasScope: unlisted scopes must not match")
	}
	if (Delegation{}).HasScope(ScopeMailRead) {
		t.Fatal("HasScope: empty grant must match nothing")
	}
}
