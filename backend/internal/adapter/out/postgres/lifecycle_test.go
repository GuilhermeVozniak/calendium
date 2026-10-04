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

// TestUsersFKDeletePolicy pins the cascade audit structurally: every foreign
// key that references users(id) must delete-cascade or set-null, and every
// user-pointing column name from the audit must actually carry such a FK. A
// future migration that forgets ON DELETE CASCADE fails here, not in prod.
func TestUsersFKDeletePolicy(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `
		SELECT c.conrelid::regclass::text, a.attname, c.confdeltype
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	policy := map[string]string{} // "table.column" -> confdeltype
	for rows.Next() {
		var table, column, deltype string
		if err := rows.Scan(&table, &column, &deltype); err != nil {
			t.Fatal(err)
		}
		policy[table+"."+column] = deltype
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for ref, deltype := range policy {
		if deltype != "c" && deltype != "n" {
			t.Errorf("%s references users with delete action %q, want CASCADE (c) or SET NULL (n)", ref, deltype)
		}
	}
	if policy["teams.created_by"] != "n" {
		t.Errorf("teams.created_by delete action = %q, want SET NULL (n)", policy["teams.created_by"])
	}
	if policy["user_preferences.user_id"] != "c" {
		t.Errorf("user_preferences.user_id delete action = %q, want CASCADE (c)", policy["user_preferences.user_id"])
	}

	// Every user-pointing column name used anywhere in the schema must be
	// covered by one of those FKs (the orphan class user_preferences was in).
	cols, err := db.QueryContext(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND column_name IN ('user_id', 'principal_id', 'assistant_id', 'grantee_user_id',
		                      'actor_id', 'author_id', 'created_by', 'invited_by')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cols.Close() }()
	for cols.Next() {
		var table, column string
		if err := cols.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		if _, ok := policy[table+"."+column]; !ok {
			t.Errorf("%s.%s names a user but has no foreign key to users(id)", table, column)
		}
	}
	if err := cols.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestPurgeCascadeCoverage seeds u1 across the owned tables (plus u2 sharing
// a team and a delegation), deletes u1 through the repo, and asserts u1's
// rows are gone everywhere, the shared team survives with created_by NULL,
// and u2's rows are intact.
func TestPurgeCascadeCoverage(t *testing.T) {
	st, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	seedUser(t, st, "u1")
	seedUser(t, st, "u2")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// --- u1's rows ---
	must(st.Subscriptions().Upsert(ctx, domain.Subscription{UserID: "u1", Status: domain.SubscriptionActive}))
	acct := seedAccount(t, st, "u1")
	must(st.OAuthStates().Create(ctx, port.OAuthState{State: "st1", UserID: "u1", Provider: domain.ProviderGoogle, ExpiresAt: now.Add(time.Hour)}))
	_, err := st.Labels().Upsert(ctx, domain.Label{AccountID: acct.ID, ProviderLabelID: "L1", Name: "Work", Kind: domain.LabelKindUser})
	must(err)
	th := seedThread(t, st, acct.ID, now)
	_, err = st.Messages().Upsert(ctx, domain.Message{
		ThreadID: th.ID, AccountID: acct.ID, ProviderMessageID: "pm1",
		From: domain.EmailAddress{Email: "a@example.com"}, Subject: "hi", SentAt: now,
	})
	must(err)
	_, err = st.Drafts().Create(ctx, domain.Draft{AccountID: acct.ID, Subject: "draft"})
	must(err)
	_, err = st.Snippets().Create(ctx, domain.Snippet{UserID: "u1", Name: "s", BodyHTML: "<p>x</p>"})
	must(err)
	cal := seedCalendar(t, st, acct.ID)
	ev, err := st.Events().Upsert(ctx, domain.Event{CalendarID: cal.ID, ProviderEventID: "pe1", Title: "e", Start: now, End: now.Add(time.Hour), Status: domain.EventConfirmed})
	must(err)
	_, err = st.Devices().Upsert(ctx, domain.NotificationDevice{UserID: "u1", Platform: domain.PlatformIOS, Token: "tok-u1"})
	must(err)
	must(st.Prefs().Save(ctx, "u1", domain.UserPrefs{}))
	must(st.UserPreferences().Put(ctx, "u1", port.UserPreferences{Theme: "ocean"}))
	must(st.UserSettings().Upsert(ctx, domain.UserSettings{UserID: "u1", TimeZone: "UTC", WorkingHours: []domain.AvailabilityWindow{}}))
	_, err = st.EventNotes().Upsert(ctx, domain.EventNote{EventID: ev.ID, UserID: "u1", BodyMD: "note"})
	must(err)
	_, err = st.Tasks().Create(ctx, domain.Task{UserID: "u1", Title: "task", Source: domain.TaskSourceLocal})
	must(err)
	_, err = st.Classifiers().Create(ctx, domain.AiClassifier{UserID: "u1", Name: "rule", Prompt: "p", LabelName: "L", Enabled: true})
	must(err)
	_, err = st.AiUsage().IncrementAndCheck(ctx, "u1", now, 10)
	must(err)
	_, err = st.BookingLinks().Create(ctx, domain.BookingLink{UserID: "u1", Slug: "u1-call", Title: "Call", CalendarID: cal.ID, DurationMinutes: 30, TimeZone: "UTC"})
	must(err)
	must(st.CalendarPrefs().Upsert(ctx, domain.DefaultCalendarPrefs("u1")))
	_, err = st.EventTemplates().Create(ctx, "u1", domain.EventTemplate{Name: "t", Title: "t", DurationMinutes: 30})
	must(err)
	_, err = st.CalendarSets().Create(ctx, "u1", domain.CalendarSet{Name: "set"})
	must(err)
	must(st.SyncStates().Save(ctx, port.SyncState{AccountID: acct.ID, Resource: "mail", Cursor: "c1"}))
	ok, _, err := st.UserExports().Claim(ctx, "u1", now, time.Hour)
	must(err)
	if !ok {
		t.Fatal("export claim refused on a fresh user")
	}

	// --- shared with u2 ---
	teams := NewTeamRepo(st)
	shared, err := teams.Create(ctx, domain.Team{Name: "Shared", CreatedBy: "u1"}, domain.TeamMember{UserID: "u1", Role: domain.TeamRoleOwner})
	must(err)
	must(teams.UpsertMember(ctx, domain.TeamMember{TeamID: shared.ID, UserID: "u2", Role: domain.TeamRoleOwner}))
	_, err = NewDelegationRepo(st).Create(ctx, domain.Delegation{PrincipalID: "u1", AssistantID: "u2", Scopes: []domain.DelegationScope{domain.ScopeMailRead}, Status: domain.DelegationActive})
	must(err)
	// --- u2's own rows (must survive) ---
	theirAcct := seedAccount(t, st, "u2")
	_, err = st.Snippets().Create(ctx, domain.Snippet{UserID: "u2", Name: "theirs", BodyHTML: "<p>y</p>"})
	must(err)

	if err := st.Users().Delete(ctx, "u1"); err != nil {
		t.Fatalf("Users.Delete: %v", err)
	}

	owned := []struct{ table, column, value string }{
		{"users", "id", "u1"}, {"subscriptions", "user_id", "u1"}, {"connected_accounts", "user_id", "u1"},
		{"oauth_states", "user_id", "u1"}, {"labels", "account_id", acct.ID}, {"threads", "account_id", acct.ID},
		{"messages", "account_id", acct.ID}, {"drafts", "account_id", acct.ID}, {"snippets", "user_id", "u1"},
		{"calendars", "account_id", acct.ID}, {"events", "calendar_id", cal.ID}, {"devices", "user_id", "u1"},
		{"user_prefs", "user_id", "u1"}, {"user_preferences", "user_id", "u1"}, {"user_settings", "user_id", "u1"},
		{"event_notes", "user_id", "u1"}, {"tasks", "user_id", "u1"}, {"ai_classifiers", "user_id", "u1"},
		{"ai_usage", "user_id", "u1"}, {"booking_links", "user_id", "u1"}, {"calendar_prefs", "user_id", "u1"},
		{"event_templates", "user_id", "u1"}, {"calendar_sets", "user_id", "u1"}, {"sync_state", "account_id", acct.ID},
		{"user_exports", "user_id", "u1"}, {"team_members", "user_id", "u1"}, {"delegations", "principal_id", "u1"},
	}
	for _, o := range owned {
		var n int
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`, o.table, o.column), o.value).Scan(&n); err != nil {
			t.Fatalf("count %s.%s: %v", o.table, o.column, err)
		}
		if n != 0 {
			t.Errorf("%s still has %d row(s) for the deleted user", o.table, n)
		}
	}
	var createdBy sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT created_by FROM teams WHERE id = $1`, shared.ID).Scan(&createdBy); err != nil {
		t.Fatalf("shared team must survive: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("shared team created_by = %q, want NULL", createdBy.String)
	}
	if _, err := teams.GetMember(ctx, shared.ID, "u2"); err != nil {
		t.Fatalf("u2's membership must survive: %v", err)
	}
	if _, err := st.Accounts().GetByID(ctx, theirAcct.ID); err != nil {
		t.Fatalf("u2's account must survive: %v", err)
	}
	theirs, err := st.Snippets().ListByUser(ctx, "u2")
	if err != nil || len(theirs) != 1 {
		t.Fatalf("u2's snippets = %v err=%v, want 1", theirs, err)
	}
}

// TestNonUserScopedTablesStayUnlinked documents why two tables that hold
// no users(id) reference are deliberately left out of the purge cascade,
// and pins their columns so adding a user-pointing column forces a revisit:
//   - billing_events (0027, piece 1): the Paddle webhook idempotency ledger,
//     keyed by notification/event ids only (spec cascade audit: "retained").
//     The subscriber link lives in subscriptions, which cascades.
//   - "rateLimit" (0028, piece 2): Better Auth's per-IP auth counters,
//     keyed by IP+path, pruned after the longest rule window (600 s).
func TestNonUserScopedTablesStayUnlinked(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()
	want := map[string]string{
		"billing_events": "event_id,event_type,notification_id,occurred_at,received_at",
		"rateLimit":      "count,id,key,lastRequest",
	}
	for table, cols := range want {
		var got string
		if err := db.QueryRowContext(ctx, `
			SELECT coalesce(string_agg(column_name::text, ',' ORDER BY column_name::text), '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`, table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != cols {
			t.Errorf("%s columns = %q, want %q — if it now stores a user reference, add a users FK with ON DELETE CASCADE (and extend TestPurgeCascadeCoverage)", table, got, cols)
		}
		var fks int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_constraint
			WHERE contype = 'f' AND conrelid = format('%I', $1::text)::regclass`, table).Scan(&fks); err != nil {
			t.Fatal(err)
		}
		if fks != 0 {
			t.Errorf("%s has %d foreign key(s), want 0 (not user-scoped)", table, fks)
		}
	}
}
