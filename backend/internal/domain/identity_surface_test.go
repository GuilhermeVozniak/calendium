package domain

// F2 identity surface: wire-shape tests for the display-identity fields
// added to Comment, Snippet, and Delegation. TeamMember's shape is covered
// in team_test.go.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommentJSONShapeIdentity(t *testing.T) {
	b, err := json.Marshal(Comment{ID: "c1", AuthorID: "u1", AuthorName: "Ada", Mentions: []string{}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"authorId":"u1"`, `"authorName":"Ada"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("Comment JSON = %s, want %s", s, want)
		}
	}
	if strings.Contains(s, "deletedAt") || strings.Contains(s, "DeletedAt") {
		t.Fatalf("Comment JSON leaks the soft-delete marker: %s", s)
	}
	// Honesty: an unresolved name serializes as the empty string, present
	// but never fabricated.
	b, _ = json.Marshal(Comment{ID: "c2", AuthorID: "u9", Mentions: []string{}})
	if !strings.Contains(string(b), `"authorName":""`) {
		t.Fatalf("Comment JSON = %s, want empty authorName serialized", b)
	}
}

func TestSnippetJSONShapeIdentity(t *testing.T) {
	b, err := json.Marshal(Snippet{ID: "s1", UserID: "u1", Name: "n", CanDelete: true})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{`"authorId":"u1"`, `"canDelete":true`} {
		if !strings.Contains(s, want) {
			t.Fatalf("Snippet JSON = %s, want %s", s, want)
		}
	}
	b, _ = json.Marshal(Snippet{ID: "s2", UserID: "u1", Name: "n"})
	if !strings.Contains(string(b), `"canDelete":false`) {
		t.Fatalf("Snippet JSON = %s, want canDelete=false serialized (fail closed)", b)
	}
}

func TestDelegationJSONShapeIdentity(t *testing.T) {
	b, err := json.Marshal(Delegation{
		ID: "d1", PrincipalID: "p1", AssistantID: "a1",
		PrincipalName: "Pat", PrincipalEmail: "pat@example.com",
		AssistantName: "Alex", AssistantEmail: "alex@example.com",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"principalName":"Pat"`, `"principalEmail":"pat@example.com"`,
		`"assistantName":"Alex"`, `"assistantEmail":"alex@example.com"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("Delegation JSON = %s, want %s", s, want)
		}
	}
}
