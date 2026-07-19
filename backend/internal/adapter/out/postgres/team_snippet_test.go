package postgres

// Team-scoped snippet persistence (M2.7 Task 11): the team_id column added
// by 0015_team_snippets.sql, ListByUser's personal-only scope, ListByTeams,
// and the ON DELETE CASCADE from teams.

import (
	"context"
	"errors"
	"testing"

	"calendium/backend/internal/domain"
)

func TestSnippetRepoTeamRoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	team := seedTeam(t, st, "u1", "Team A")

	created, err := st.Snippets().Create(ctx, domain.Snippet{
		UserID: "u1", TeamID: &team.ID, Name: "Team greeting", BodyHTML: "<p>hi team</p>",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := st.Snippets().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TeamID == nil || *got.TeamID != team.ID {
		t.Fatalf("TeamID = %v, want %s", got.TeamID, team.ID)
	}
	if got.UserID != "u1" || got.Name != "Team greeting" {
		t.Fatalf("snippet = %+v", got)
	}
}

func TestSnippetRepoCreateUnknownTeamFails(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	ghost := "no-such-team"

	if _, err := st.Snippets().Create(ctx, domain.Snippet{
		UserID: "u1", TeamID: &ghost, Name: "n", BodyHTML: "b",
	}); err == nil {
		t.Fatal("Create with unknown team succeeded, want FK error")
	}
}

func TestSnippetRepoListByUserExcludesTeamSnippets(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	team := seedTeam(t, st, "u1", "Team A")

	if _, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "personal", BodyHTML: "b"}); err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	if _, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", TeamID: &team.ID, Name: "team", BodyHTML: "b"}); err != nil {
		t.Fatalf("Create team: %v", err)
	}

	list, err := st.Snippets().ListByUser(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 1 || list[0].Name != "personal" {
		t.Fatalf("ListByUser = %+v, want only the personal snippet", list)
	}
}

func TestSnippetRepoListByTeams(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teamA := seedTeam(t, st, "u1", "Team A")
	teamB := seedTeam(t, st, "u1", "Team B")
	teamC := seedTeam(t, st, "u2", "Team C") // not queried — cross-tenant

	seed := []domain.Snippet{
		{UserID: "u1", TeamID: &teamA.ID, Name: "Zeta", BodyHTML: "b"},
		{UserID: "u1", TeamID: &teamB.ID, Name: "Alpha", BodyHTML: "b"},
		{UserID: "u2", TeamID: &teamC.ID, Name: "Foreign", BodyHTML: "b"},
		{UserID: "u1", Name: "Personal", BodyHTML: "b"},
	}
	for _, sn := range seed {
		if _, err := st.Snippets().Create(ctx, sn); err != nil {
			t.Fatalf("Create %s: %v", sn.Name, err)
		}
	}

	list, err := st.Snippets().ListByTeams(ctx, []string{teamA.ID, teamB.ID})
	if err != nil {
		t.Fatalf("ListByTeams: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("ListByTeams = %+v, want [Alpha, Zeta] (name-ordered, no cross-tenant rows)", list)
	}

	empty, err := st.Snippets().ListByTeams(ctx, nil)
	if err != nil {
		t.Fatalf("ListByTeams(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("ListByTeams(nil) = %v, want non-nil empty slice", empty)
	}
}

func TestTeamDeleteCascadesSnippets(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	team := seedTeam(t, st, "u1", "Team A")

	teamSnip, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", TeamID: &team.ID, Name: "team", BodyHTML: "b"})
	if err != nil {
		t.Fatalf("Create team snippet: %v", err)
	}
	personal, err := st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "personal", BodyHTML: "b"})
	if err != nil {
		t.Fatalf("Create personal snippet: %v", err)
	}

	if err := NewTeamRepo(st).Delete(ctx, team.ID); err != nil {
		t.Fatalf("Delete team: %v", err)
	}
	if _, err := st.Snippets().GetByID(ctx, teamSnip.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("team snippet after cascade = %v, want ErrNotFound", err)
	}
	if _, err := st.Snippets().GetByID(ctx, personal.ID); err != nil {
		t.Fatalf("personal snippet must survive the cascade: %v", err)
	}
}
