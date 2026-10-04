package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func TestUserExportClaim(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	ok, _, err := st.UserExports().Claim(ctx, "u1", now, time.Hour)
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v, want ok", ok, err)
	}
	ok, retryAt, err := st.UserExports().Claim(ctx, "u1", now.Add(10*time.Minute), time.Hour)
	if err != nil || ok {
		t.Fatalf("second claim inside the window: ok=%v err=%v, want refused", ok, err)
	}
	if !retryAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("retryAt = %v, want %v", retryAt, now.Add(time.Hour))
	}
	var started time.Time
	if err := db.QueryRowContext(ctx, `SELECT started_at FROM user_exports WHERE user_id = 'u1'`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if !started.Equal(now) {
		t.Fatalf("a refused claim must not move started_at: got %v, want %v", started, now)
	}
	ok, _, err = st.UserExports().Claim(ctx, "u1", now.Add(time.Hour), time.Hour)
	if err != nil || !ok {
		t.Fatalf("claim exactly one window later: ok=%v err=%v, want ok", ok, err)
	}
}

func TestUserExportsCascadeFromUsers(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if ok, _, err := st.UserExports().Claim(ctx, "u1", time.Now(), time.Hour); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_exports WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_exports rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestUserPreferencesCascade(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.UserPreferences().Put(ctx, "u1", port.UserPreferences{Theme: "ocean"}); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conname = 'user_preferences_user_id_fkey' AND confdeltype = 'c'`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("user_preferences_user_id_fkey ON DELETE CASCADE present = %d err=%v, want 1", fk, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM user_preferences WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user_preferences rows after user delete = %d err=%v, want 0", n, err)
	}
}

func TestTeamsCreatedBySetNull(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teams := NewTeamRepo(st)
	team, err := teams.Create(ctx, domain.Team{Name: "Design", CreatedBy: "u1"},
		domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: team.ID, UserID: "u2", Role: domain.TeamRoleOwner}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("deleting a team creator must no longer be RESTRICTed: %v", err)
	}
	var createdBy sql.NullString
	var members int
	if err := db.QueryRowContext(ctx, `SELECT created_by FROM teams WHERE id = $1`, team.ID).Scan(&createdBy); err != nil {
		t.Fatalf("team must survive its creator: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("created_by = %q, want NULL", createdBy.String)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE team_id = $1`, team.ID).Scan(&members); err != nil || members != 1 {
		t.Fatalf("surviving members = %d err=%v, want 1 (u2)", members, err)
	}
}

func TestUserRepoDelete(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	if err := st.Users().Delete(ctx, "u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Users().GetByID(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID after Delete err = %v, want ErrNotFound", err)
	}
	if err := st.Users().Delete(ctx, "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Delete err = %v, want ErrNotFound", err)
	}
}

func TestTeamRepoListMembershipsAndNullCreator(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	teams := NewTeamRepo(st)
	a, err := teams.Create(ctx, domain.Team{Name: "A", CreatedBy: "u2"}, domain.TeamMember{UserID: "u2", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	b, err := teams.Create(ctx, domain.Team{Name: "B", CreatedBy: "u1"}, domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	if err != nil {
		t.Fatal(err)
	}
	if err := teams.UpsertMember(ctx, domain.TeamMember{TeamID: a.ID, UserID: "u1", Role: domain.TeamRoleMember}); err != nil {
		t.Fatal(err)
	}
	got, err := teams.ListMemberships(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("memberships = %d, want 2", len(got))
	}
	roles := map[string]domain.TeamRole{}
	for _, m := range got {
		if m.UserID != "u1" {
			t.Fatalf("membership for %q leaked into u1's list", m.UserID)
		}
		roles[m.TeamID] = m.Role
	}
	if roles[a.ID] != domain.TeamRoleMember || roles[b.ID] != domain.TeamRoleOwner {
		t.Fatalf("roles = %v", roles)
	}
	if _, err := db.ExecContext(ctx, `UPDATE teams SET created_by = NULL WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	team, err := teams.GetByID(ctx, b.ID)
	if err != nil || team.CreatedBy != "" {
		t.Fatalf("GetByID with NULL created_by = (%+v, %v), want CreatedBy \"\"", team, err)
	}
	list, err := teams.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tm := range list {
		if tm.ID == b.ID && tm.CreatedBy != "" {
			t.Fatalf("ListByUser scanned NULL created_by as %q", tm.CreatedBy)
		}
	}
}

func TestThreadRepoListByAccountPage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, seedThread(t, st, mine.ID, now.Add(time.Duration(i)*time.Minute)).ID)
	}
	seedThread(t, st, theirs.ID, now)
	sort.Strings(ids)

	page1, err := st.Threads().ListByAccountPage(ctx, mine.ID, "", 2)
	if err != nil || len(page1) != 2 || page1[0].ID != ids[0] || page1[1].ID != ids[1] {
		t.Fatalf("page1 = %v err=%v, want ids %v", page1, err, ids[:2])
	}
	page2, err := st.Threads().ListByAccountPage(ctx, mine.ID, page1[1].ID, 2)
	if err != nil || len(page2) != 1 || page2[0].ID != ids[2] {
		t.Fatalf("page2 = %v err=%v, want [%s]", page2, err, ids[2])
	}
	page3, err := st.Threads().ListByAccountPage(ctx, mine.ID, page2[0].ID, 2)
	if err != nil || len(page3) != 0 {
		t.Fatalf("page3 = %v err=%v, want empty", page3, err)
	}
}

func TestEventRepoListByUserPage(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	myCal := seedCalendar(t, st, mine.ID)
	theirCal := seedCalendar(t, st, theirs.ID)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	ids := []string{}
	for i := 0; i < 3; i++ {
		e, err := st.Events().Upsert(ctx, domain.Event{
			CalendarID: myCal.ID, ProviderEventID: fmt.Sprintf("pe%d", i), Title: "mine",
			Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	if _, err := st.Events().Upsert(ctx, domain.Event{
		CalendarID: theirCal.ID, ProviderEventID: "pe-other", Title: "theirs",
		Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed,
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)

	page1, err := st.Events().ListByUserPage(ctx, "u1", "", 2)
	if err != nil || len(page1) != 2 || page1[0].ID != ids[0] || page1[1].ID != ids[1] {
		t.Fatalf("page1 = %v err=%v", page1, err)
	}
	page2, err := st.Events().ListByUserPage(ctx, "u1", page1[1].ID, 2)
	if err != nil || len(page2) != 1 || page2[0].ID != ids[2] {
		t.Fatalf("page2 = %v err=%v", page2, err)
	}
	for _, e := range append(page1, page2...) {
		if e.Title != "mine" {
			t.Fatalf("another user's event leaked: %+v", e)
		}
	}
}

func TestEventNoteRepoListByUser(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	mine := seedAccount(t, st, "u1")
	theirs := seedAccount(t, st, "u2")
	myCal := seedCalendar(t, st, mine.ID)
	theirCal := seedCalendar(t, st, theirs.ID)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	e1, _ := st.Events().Upsert(ctx, domain.Event{CalendarID: myCal.ID, ProviderEventID: "pe1", Title: "a", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	e2, _ := st.Events().Upsert(ctx, domain.Event{CalendarID: theirCal.ID, ProviderEventID: "pe2", Title: "b", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{EventID: e1.ID, UserID: "u1", BodyMD: "mine", Links: []string{"https://x.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EventNotes().Upsert(ctx, domain.EventNote{EventID: e2.ID, UserID: "u2", BodyMD: "theirs"}); err != nil {
		t.Fatal(err)
	}
	notes, err := st.EventNotes().ListByUser(ctx, "u1")
	if err != nil || len(notes) != 1 || notes[0].BodyMD != "mine" || len(notes[0].Links) != 1 {
		t.Fatalf("ListByUser = %+v err=%v, want the one u1 note with its link", notes, err)
	}
}
