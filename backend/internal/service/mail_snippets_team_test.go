package service

// Team-scoped snippets (M2.7 Task 11): create requires membership (any
// role), listing merges personal + all-my-teams snippets without
// duplicates, and mutation of a team snippet is author-or-admin+.
// Cross-tenant negatives: non-members always see ErrNotFound (team
// existence is never leaked), under-privileged members see ErrForbidden.

import (
	"context"
	"errors"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// seedSnippetTeam creates a team with the given owner plus extra members.
func seedSnippetTeam(t *testing.T, f *mailFixture, teamID, ownerID string, members map[string]domain.TeamRole) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	_, err := f.teams.Create(ctx, domain.Team{ID: teamID, Name: teamID, CreatedBy: ownerID, CreatedAt: now},
		domain.TeamMember{TeamID: teamID, UserID: ownerID, Role: domain.TeamRoleOwner, JoinedAt: now})
	if err != nil {
		t.Fatalf("seed team %s: %v", teamID, err)
	}
	for userID, role := range members {
		if err := f.teams.UpsertMember(ctx, domain.TeamMember{TeamID: teamID, UserID: userID, Role: role, JoinedAt: now}); err != nil {
			t.Fatalf("seed member %s: %v", userID, err)
		}
	}
}

func strp(s string) *string { return &s }

func TestCreateTeamSnippet(t *testing.T) {
	ctx := context.Background()

	t.Run("non-member is ErrNotFound (cross-tenant: team existence never leaks)", func(t *testing.T) {
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "owner", nil)

		_, err := f.svc.CreateSnippet(ctx, "outsider", port.SnippetInput{Name: "n", BodyHTML: "b", TeamID: strp("t1")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unknown team is ErrNotFound", func(t *testing.T) {
		f := newMailFixture(t)

		_, err := f.svc.CreateSnippet(ctx, "u1", port.SnippetInput{Name: "n", BodyHTML: "b", TeamID: strp("ghost")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("plain member may contribute (any role)", func(t *testing.T) {
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "owner", map[string]domain.TeamRole{"u1": domain.TeamRoleMember})

		got, err := f.svc.CreateSnippet(ctx, "u1", port.SnippetInput{Name: "n", BodyHTML: "b", TeamID: strp("t1")})
		if err != nil {
			t.Fatalf("CreateSnippet: %v", err)
		}
		if got.TeamID == nil || *got.TeamID != "t1" {
			t.Fatalf("TeamID = %v, want t1", got.TeamID)
		}
		if got.UserID != "u1" {
			t.Fatalf("UserID = %q, want u1 (author)", got.UserID)
		}
	})

	t.Run("service wired without Teams rejects team scope as ErrNotFound", func(t *testing.T) {
		svc := NewMailService(MailServiceDeps{Snippets: newSnippetRepo(), Clock: SystemClock{}, SelfHosted: true})

		_, err := svc.CreateSnippet(ctx, "u1", port.SnippetInput{Name: "n", BodyHTML: "b", TeamID: strp("t1")})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestListSnippetsMergesPersonalAndTeams(t *testing.T) {
	ctx := context.Background()

	t.Run("personal + all my teams, no duplicates", func(t *testing.T) {
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "u1", nil) // u1 owns t1
		seedSnippetTeam(t, f, "t2", "owner2", map[string]domain.TeamRole{"u1": domain.TeamRoleMember})
		seed := []domain.Snippet{
			{ID: "s-personal", UserID: "u1", Name: "mine", BodyHTML: "b"},
			{ID: "s-t1", UserID: "u1", TeamID: strp("t1"), Name: "authored by me", BodyHTML: "b"},
			{ID: "s-t2", UserID: "owner2", TeamID: strp("t2"), Name: "someone else's", BodyHTML: "b"},
			{ID: "s-foreign", UserID: "other", Name: "foreign personal", BodyHTML: "b"},
		}
		for _, sn := range seed {
			if _, err := f.snippets.Create(ctx, sn); err != nil {
				t.Fatalf("seed %s: %v", sn.ID, err)
			}
		}

		got, err := f.svc.ListSnippets(ctx, "u1")
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		seen := map[string]int{}
		for _, sn := range got {
			seen[sn.ID]++
		}
		if len(got) != 3 || seen["s-personal"] != 1 || seen["s-t1"] != 1 || seen["s-t2"] != 1 {
			t.Fatalf("got %v, want exactly {s-personal, s-t1, s-t2} once each", seen)
		}
	})

	t.Run("team snippets are invisible to non-members (cross-tenant)", func(t *testing.T) {
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "owner", nil)
		if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s-t1", UserID: "owner", TeamID: strp("t1"), Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed: %v", err)
		}

		got, err := f.svc.ListSnippets(ctx, "outsider")
		if err != nil {
			t.Fatalf("ListSnippets: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got = %+v, want empty for non-member", got)
		}
		if got == nil {
			t.Fatal("want non-nil empty slice")
		}
	})
}

func TestUpdateTeamSnippet(t *testing.T) {
	ctx := context.Background()
	in := port.SnippetInput{Name: "n2", BodyHTML: "b2"}

	seed := func(t *testing.T) *mailFixture {
		t.Helper()
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "owner", map[string]domain.TeamRole{
			"author": domain.TeamRoleMember,
			"peer":   domain.TeamRoleMember,
			"admin":  domain.TeamRoleAdmin,
		})
		if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s1", UserID: "author", TeamID: strp("t1"), Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}
		return f
	}

	t.Run("fellow member (non-author) is ErrForbidden", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.UpdateSnippet(ctx, "peer", "s1", in); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("non-member is ErrNotFound (cross-tenant)", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.UpdateSnippet(ctx, "outsider", "s1", in); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("author may edit", func(t *testing.T) {
		f := seed(t)
		got, err := f.svc.UpdateSnippet(ctx, "author", "s1", in)
		if err != nil {
			t.Fatalf("UpdateSnippet: %v", err)
		}
		if got.Name != "n2" || got.BodyHTML != "b2" {
			t.Fatalf("snippet = %+v", got)
		}
	})

	t.Run("admin may edit another member's snippet", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.UpdateSnippet(ctx, "admin", "s1", in); err != nil {
			t.Fatalf("UpdateSnippet: %v", err)
		}
	})

	t.Run("owner may edit another member's snippet", func(t *testing.T) {
		f := seed(t)
		if _, err := f.svc.UpdateSnippet(ctx, "owner", "s1", in); err != nil {
			t.Fatalf("UpdateSnippet: %v", err)
		}
	})

	t.Run("scope is immutable: update never re-homes the snippet", func(t *testing.T) {
		f := seed(t)
		got, err := f.svc.UpdateSnippet(ctx, "author", "s1", port.SnippetInput{Name: "n2", BodyHTML: "b2", TeamID: strp("t-elsewhere")})
		if err != nil {
			t.Fatalf("UpdateSnippet: %v", err)
		}
		if got.TeamID == nil || *got.TeamID != "t1" {
			t.Fatalf("TeamID = %v, want t1 (unchanged)", got.TeamID)
		}
	})
}

func TestDeleteTeamSnippet(t *testing.T) {
	ctx := context.Background()

	seed := func(t *testing.T) *mailFixture {
		t.Helper()
		f := newMailFixture(t)
		seedSnippetTeam(t, f, "t1", "owner", map[string]domain.TeamRole{
			"author": domain.TeamRoleMember,
			"peer":   domain.TeamRoleMember,
			"admin":  domain.TeamRoleAdmin,
		})
		if _, err := f.snippets.Create(ctx, domain.Snippet{ID: "s1", UserID: "author", TeamID: strp("t1"), Name: "n", BodyHTML: "b"}); err != nil {
			t.Fatalf("seed snippet: %v", err)
		}
		return f
	}

	t.Run("fellow member (non-author) is ErrForbidden and keeps the snippet", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.DeleteSnippet(ctx, "peer", "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if _, err := f.snippets.GetByID(ctx, "s1"); err != nil {
			t.Fatalf("snippet should survive: %v", err)
		}
	})

	t.Run("non-member is ErrNotFound (cross-tenant)", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.DeleteSnippet(ctx, "outsider", "s1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("author may delete", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.DeleteSnippet(ctx, "author", "s1"); err != nil {
			t.Fatalf("DeleteSnippet: %v", err)
		}
		if _, err := f.snippets.GetByID(ctx, "s1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound after delete", err)
		}
	})

	t.Run("admin may delete another member's snippet", func(t *testing.T) {
		f := seed(t)
		if err := f.svc.DeleteSnippet(ctx, "admin", "s1"); err != nil {
			t.Fatalf("DeleteSnippet: %v", err)
		}
	})
}
