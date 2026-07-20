package service

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

func TestNewID(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := newID()
		if len(id) != 32 {
			t.Fatalf("len(newID()) = %d, want 32", len(id))
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("newID() = %q is not hex: %v", id, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("newID() collision on %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRandomToken(t *testing.T) {
	for _, n := range []int{0, 1, 16, 32} {
		tok := randomToken(n)
		if len(tok) != 2*n { // hex-encoded n bytes
			t.Fatalf("len(randomToken(%d)) = %d, want %d", n, len(tok), 2*n)
		}
		if _, err := hex.DecodeString(tok); err != nil {
			t.Fatalf("randomToken(%d) = %q is not hex: %v", n, tok, err)
		}
	}
	if randomToken(16) == randomToken(16) {
		t.Fatal("randomToken is not random across calls")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name, in string
		n        int
		want     string
	}{
		{"shorter than n unchanged", "hi", 5, "hi"},
		{"equal to n unchanged", "hello", 5, "hello"},
		{"longer truncated with ellipsis", "hello world", 5, "hello…"},
		{"n zero keeps only ellipsis", "abc", 0, "…"},
		{"multibyte within limit unchanged", "café 🎉", 6, "café 🎉"},
		{"cut lands on a rune boundary, never mid-rune", "café 🎉 party", 6, "café 🎉…"},
		{"emoji-only body truncates whole emoji", "🎉🎉🎉🎉", 2, "🎉🎉…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.n); got != tt.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"no args", nil, ""},
		{"all empty", []string{"", ""}, ""},
		{"first wins", []string{"a", "b"}, "a"},
		{"skips leading empties", []string{"", "", "c"}, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.in...); got != tt.want {
				t.Fatalf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Ownership invariant: a resource owned by another user is indistinguishable
// from a missing one (404, never 403/ErrUnauthorized).
func TestOwnershipHelpers404OnForeign(t *testing.T) {
	ctx := context.Background()
	accounts := newAccountRepo()
	threads := newThreadRepo()
	drafts := newDraftRepo(accounts)

	if _, err := accounts.Create(ctx, domain.ConnectedAccount{ID: "a1", UserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := threads.Upsert(ctx, domain.Thread{ID: "t1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := drafts.Create(ctx, domain.Draft{ID: "d1", AccountID: "a1"}); err != nil {
		t.Fatal(err)
	}

	// Owner resolves all three.
	if _, err := ownedAccount(ctx, accounts, "owner", "a1"); err != nil {
		t.Fatalf("owner account: %v", err)
	}
	if _, _, err := ownedThread(ctx, threads, accounts, "owner", "t1"); err != nil {
		t.Fatalf("owner thread: %v", err)
	}
	if _, _, err := ownedDraft(ctx, drafts, accounts, "owner", "d1"); err != nil {
		t.Fatalf("owner draft: %v", err)
	}

	// A stranger gets ErrNotFound, never ErrUnauthorized.
	if _, err := ownedAccount(ctx, accounts, "intruder", "a1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign account err = %v, want ErrNotFound", err)
	}
	if _, _, err := ownedThread(ctx, threads, accounts, "intruder", "t1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign thread err = %v, want ErrNotFound", err)
	}
	if _, _, err := ownedDraft(ctx, drafts, accounts, "intruder", "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign draft err = %v, want ErrNotFound", err)
	}

	// Genuinely missing ids also 404.
	if _, err := ownedAccount(ctx, accounts, "owner", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing account err = %v, want ErrNotFound", err)
	}
}
